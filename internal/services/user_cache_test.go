package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sendly/internal/config"
	"sendly/internal/models"
)

type memoryUserStore struct {
	users map[int64]models.User
}

func newMemoryUserStore() *memoryUserStore {
	return &memoryUserStore{users: map[int64]models.User{}}
}

func (m *memoryUserStore) GetUser(_ context.Context, cnsUserID int64) (*models.User, error) {
	u, ok := m.users[cnsUserID]
	if !ok {
		return nil, models.ErrUserNotFound
	}
	return &u, nil
}

func (m *memoryUserStore) UpsertUser(_ context.Context, user *models.User) error {
	stored := *user
	stored.LastSyncedAt = time.Now()
	m.users[user.CNSUserID] = stored
	return nil
}

func (m *memoryUserStore) DeactivateStaleUsers(_ context.Context, staleBefore time.Time) (int64, error) {
	var count int64
	for id, u := range m.users {
		if u.Status == models.UserStatusActive && u.LastSyncedAt.Before(staleBefore) {
			u.Status = models.UserStatusInactive
			m.users[id] = u
			count++
		}
	}
	return count, nil
}

// fakeCNS runs an httptest server implementing just enough of CNS's
// service-to-service surface to test the client and sync logic against.
type fakeCNS struct {
	server        *httptest.Server
	profile       CNSServiceProfile
	profileCalls  int
	patchCalls    int
	lastPatchBody map[string]any
}

func newFakeCNS(t *testing.T, profile CNSServiceProfile) *fakeCNS {
	t.Helper()
	f := &fakeCNS{profile: profile}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Service-Key") != "test-service-key" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/service/me":
			f.profileCalls++
			_ = json.NewEncoder(w).Encode(f.profile)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/data/sendly/":
			f.patchCalls++
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.lastPatchBody = body
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "ok", "data": body})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func testConfig(cnsURL string) *config.Config {
	return &config.Config{
		CNSServiceAPIURL:  cnsURL,
		CNSAuthServiceKey: "test-service-key",
		CNSServiceSlug:    "sendly",

		UserCacheTTL:               24 * time.Hour,
		UserCacheReconcileInterval: time.Hour,
		UserCacheStaleAfter:        30 * 24 * time.Hour,
	}
}

func TestSyncIfStaleUpsertsMissingUser(t *testing.T) {
	fake := newFakeCNS(t, CNSServiceProfile{ID: 42, Username: "alice", IsActive: true})
	fake.profile.Profile.Avatar = "https://cdn.example/alice.png"
	cfg := testConfig(fake.server.URL)
	store := newMemoryUserStore()
	uc := NewUserCache(cfg, store, NewCNSClient(cfg))

	uc.SyncIfStale(context.Background(), 42, "token")

	user, err := store.GetUser(context.Background(), 42)
	if err != nil {
		t.Fatalf("expected user to be cached, got error: %v", err)
	}
	if user.Username != "alice" || user.Status != models.UserStatusActive {
		t.Fatalf("unexpected cached user: %+v", user)
	}
	if !user.AvatarURL.Valid || user.AvatarURL.String != "https://cdn.example/alice.png" {
		t.Fatalf("unexpected avatar: %+v", user.AvatarURL)
	}
	if fake.profileCalls != 1 {
		t.Fatalf("expected exactly 1 profile call, got %d", fake.profileCalls)
	}
	if fake.patchCalls != 1 {
		t.Fatalf("expected exactly 1 service-data patch, got %d", fake.patchCalls)
	}
	if fake.lastPatchBody["status"] != string(models.UserStatusActive) {
		t.Fatalf("expected patched status to be active, got %v", fake.lastPatchBody["status"])
	}
}

func TestSyncIfStaleSkipsFreshUser(t *testing.T) {
	fake := newFakeCNS(t, CNSServiceProfile{ID: 7, Username: "bob", IsActive: true})
	cfg := testConfig(fake.server.URL)
	store := newMemoryUserStore()
	store.users[7] = models.User{CNSUserID: 7, Username: "bob", Status: models.UserStatusActive, LastSyncedAt: time.Now()}
	uc := NewUserCache(cfg, store, NewCNSClient(cfg))

	uc.SyncIfStale(context.Background(), 7, "token")

	if fake.profileCalls != 0 {
		t.Fatalf("expected no CNS call for a fresh cache row, got %d", fake.profileCalls)
	}
}

func TestSyncIfStaleResyncsExpiredUser(t *testing.T) {
	fake := newFakeCNS(t, CNSServiceProfile{ID: 7, Username: "bob-renamed", IsActive: true})
	cfg := testConfig(fake.server.URL)
	store := newMemoryUserStore()
	store.users[7] = models.User{
		CNSUserID: 7, Username: "bob", Status: models.UserStatusActive,
		LastSyncedAt: time.Now().Add(-25 * time.Hour),
	}
	uc := NewUserCache(cfg, store, NewCNSClient(cfg))

	uc.SyncIfStale(context.Background(), 7, "token")

	if fake.profileCalls != 1 {
		t.Fatalf("expected a resync for an expired cache row, got %d calls", fake.profileCalls)
	}
	user, _ := store.GetUser(context.Background(), 7)
	if user.Username != "bob-renamed" {
		t.Fatalf("expected refreshed username, got %q", user.Username)
	}
}

func TestSyncIfStaleMarksLockedUserInactive(t *testing.T) {
	fake := newFakeCNS(t, CNSServiceProfile{ID: 9, Username: "carl", IsActive: true, Locked: true})
	cfg := testConfig(fake.server.URL)
	store := newMemoryUserStore()
	uc := NewUserCache(cfg, store, NewCNSClient(cfg))

	uc.SyncIfStale(context.Background(), 9, "token")

	user, err := store.GetUser(context.Background(), 9)
	if err != nil {
		t.Fatalf("expected user to be cached, got error: %v", err)
	}
	if user.Status != models.UserStatusInactive {
		t.Fatalf("expected a locked CNS account to sync as inactive, got %q", user.Status)
	}
}

func TestReconcileDeactivatesStaleUsersOnly(t *testing.T) {
	cfg := testConfig("")
	store := newMemoryUserStore()
	store.users[1] = models.User{CNSUserID: 1, Status: models.UserStatusActive, LastSyncedAt: time.Now().Add(-40 * 24 * time.Hour)}
	store.users[2] = models.User{CNSUserID: 2, Status: models.UserStatusActive, LastSyncedAt: time.Now()}
	uc := NewUserCache(cfg, store, NewCNSClient(cfg))

	uc.ForceReconcile()

	stale, _ := store.GetUser(context.Background(), 1)
	if stale.Status != models.UserStatusInactive {
		t.Fatalf("expected stale user to be deactivated, got %q", stale.Status)
	}
	fresh, _ := store.GetUser(context.Background(), 2)
	if fresh.Status != models.UserStatusActive {
		t.Fatalf("expected fresh user to remain active, got %q", fresh.Status)
	}
}
