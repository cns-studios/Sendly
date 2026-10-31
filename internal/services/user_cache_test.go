package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"sendly/internal/config"
	"sendly/internal/models"
	"sendly/internal/storage"
)

// memoryUserStore keeps users in a map but applies the same state rules as
// storage.Postgres (storage.NextUserState, storage.DeletedUserState); the
// SQL around them is covered by the live storage tests.
type memoryUserStore struct {
	mu     sync.Mutex
	users  map[int64]models.User
	events map[string]time.Time
	jobs   int
}

func newMemoryUserStore() *memoryUserStore {
	return &memoryUserStore{users: map[int64]models.User{}, events: map[string]time.Time{}}
}

func (m *memoryUserStore) GetUser(_ context.Context, cnsUserID int64) (*models.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[cnsUserID]
	if !ok {
		return nil, models.ErrUserNotFound
	}
	return &u, nil
}

func (m *memoryUserStore) RecordCNSProfile(_ context.Context, profile models.CNSUserProfile, locked *bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var cur *models.User
	if u, ok := m.users[profile.CNSUserID]; ok {
		if u.Status == models.UserStatusDeleted {
			return nil
		}
		cur = &u
	}
	m.users[profile.CNSUserID] = storage.NextUserState(cur, profile, locked, time.Now())
	return nil
}

func (m *memoryUserStore) MarkUserDeleted(_ context.Context, cnsUserID int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[cnsUserID]
	if ok && u.Status == models.UserStatusDeleted {
		return false, nil
	}
	var cur *models.User
	if ok {
		cur = &u
	}
	m.users[cnsUserID] = storage.DeletedUserState(cur, cnsUserID, time.Now())
	return true, nil
}

func (m *memoryUserStore) ListUsersToVerify(_ context.Context, syncedBefore time.Time, after storage.UserSyncCursor, limit int) ([]models.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []models.User
	for _, u := range m.users {
		afterCursor := u.LastSyncedAt.After(after.LastSyncedAt) ||
			(u.LastSyncedAt.Equal(after.LastSyncedAt) && u.CNSUserID > after.CNSUserID)
		if u.Status != models.UserStatusDeleted && u.LastSyncedAt.Before(syncedBefore) && afterCursor {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastSyncedAt.Equal(out[j].LastSyncedAt) {
			return out[i].LastSyncedAt.Before(out[j].LastSyncedAt)
		}
		return out[i].CNSUserID < out[j].CNSUserID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *memoryUserStore) DeactivateStaleUsers(_ context.Context, staleBefore time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var count int64
	for id, u := range m.users {
		if u.Status == models.UserStatusActive && u.LastSyncedAt.Before(staleBefore) {
			u.Status = models.UserStatusInactive
			u.InactiveReason = sql.NullString{String: models.InactiveReasonStale, Valid: true}
			m.users[id] = u
			count++
		}
	}
	return count, nil
}

func (m *memoryUserStore) PurgeCNSWebhookEvents(_ context.Context, cutoff time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for id, at := range m.events {
		if at.Before(cutoff) {
			delete(m.events, id)
			n++
		}
	}
	return n, nil
}

func (m *memoryUserStore) RunJob(ctx context.Context, _ string, _ time.Duration, fn func(context.Context)) (bool, error) {
	m.jobs++
	fn(ctx)
	return true, nil
}

// fakeCNS runs an httptest server implementing just enough of CNS's
// service-to-service surface and users:read directory to test the client
// and sync logic against.
type fakeCNS struct {
	server        *httptest.Server
	mu            sync.Mutex
	profile       CNSServiceProfile
	profileCalls  int
	patchCalls    int
	lastPatchBody map[string]any
	// directory holds the live CNS users by id; ids missing from it are
	// deleted. batchHides makes the batch lookup leave ids out that the
	// single lookup still returns. directoryStatus, when set, is returned
	// by every directory call instead.
	directory       map[int64]CNSPublicProfile
	batchHides      map[int64]bool
	directoryStatus int
	directoryBody   string
	batchCalls      int
	singleCalls     int
}

func newFakeCNS(t *testing.T, profile CNSServiceProfile) *fakeCNS {
	t.Helper()
	f := &fakeCNS{profile: profile, directory: map[int64]CNSPublicProfile{}, batchHides: map[int64]bool{}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
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
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/users"):
			if f.directoryStatus != 0 {
				w.WriteHeader(f.directoryStatus)
				_, _ = w.Write([]byte(f.directoryBody))
				return
			}
			if r.URL.Path == "/api/users" {
				f.batchCalls++
				users := []CNSPublicProfile{}
				for _, raw := range strings.Split(r.URL.Query().Get("ids"), ",") {
					id, _ := strconv.ParseInt(raw, 10, 64)
					if p, ok := f.directory[id]; ok && !f.batchHides[id] {
						users = append(users, p)
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"users": users})
				return
			}
			f.singleCalls++
			id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/users/"), 10, 64)
			p, ok := f.directory[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"user not found"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(p)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeCNS) setUser(id int64, username string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.directory[id] = CNSPublicProfile{ID: id, Username: username}
}

func testConfig(cnsURL string) *config.Config {
	return &config.Config{
		CNSServiceAPIURL:  cnsURL,
		CNSAuthURL:        cnsURL,
		CNSAuthServiceKey: "test-service-key",
		CNSServiceSlug:    "sendly",

		UserCacheTTL:                24 * time.Hour,
		UserCacheReconcileInterval:  time.Hour,
		UserCacheStaleAfter:         30 * 24 * time.Hour,
		UserCacheRecheck:            24 * time.Hour,
		UserCacheMaxDeletionsPerRun: 25,
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

func dueUser(id int64, username string) models.User {
	at := time.Now().Add(-48 * time.Hour)
	return models.User{
		CNSUserID: id, Username: username, Status: models.UserStatusActive, LastSyncedAt: at,
		ProfileObservedAt: sql.NullTime{Time: at, Valid: true},
	}
}

func TestSyncIfStaleNeverResurrectsDeletedUser(t *testing.T) {
	fake := newFakeCNS(t, CNSServiceProfile{ID: 7, Username: "ghost", IsActive: true})
	cfg := testConfig(fake.server.URL)
	store := newMemoryUserStore()
	store.users[7] = storage.DeletedUserState(nil, 7, time.Now().Add(-48*time.Hour))
	uc := NewUserCache(cfg, store, NewCNSClient(cfg))

	uc.SyncIfStale(context.Background(), 7, "token")

	if fake.profileCalls != 0 {
		t.Fatalf("a deleted user must not be re-fetched, got %d calls", fake.profileCalls)
	}
	if u, _ := store.GetUser(context.Background(), 7); u.Status != models.UserStatusDeleted || u.Username != "" {
		t.Fatalf("tombstone changed: %+v", u)
	}
}

func TestVerifyWithDirectoryRefreshesAndDeletes(t *testing.T) {
	fake := newFakeCNS(t, CNSServiceProfile{})
	fake.setUser(1, "alice-renamed")
	cfg := testConfig(fake.server.URL)
	store := newMemoryUserStore()
	store.users[1] = dueUser(1, "alice")
	store.users[2] = dueUser(2, "bob") // deleted on CNS
	fresh := dueUser(3, "carol")
	fresh.LastSyncedAt = time.Now()
	store.users[3] = fresh
	uc := NewUserCache(cfg, store, NewCNSClient(cfg))

	result := uc.VerifyWithDirectory(context.Background(), time.Now().Add(-cfg.UserCacheRecheck), 25)

	if result.Err != nil || result.Confirmed != 1 || result.Deleted != 1 || result.MassDeletionHeld {
		t.Fatalf("unexpected result: %+v", result)
	}
	if u, _ := store.GetUser(context.Background(), 1); u.Username != "alice-renamed" || time.Since(u.LastSyncedAt) > time.Minute {
		t.Fatalf("rename not picked up: %+v", u)
	}
	if u, _ := store.GetUser(context.Background(), 2); u.Status != models.UserStatusDeleted || u.Username != "" {
		t.Fatalf("deleted user not erased: %+v", u)
	}
	if u, _ := store.GetUser(context.Background(), 3); u.Username != "carol" || u.Status != models.UserStatusActive {
		t.Fatalf("a user that isn't due was touched: %+v", u)
	}
	if fake.batchCalls != 1 || fake.singleCalls != 1 {
		t.Fatalf("expected 1 batch and 1 single lookup, got %d and %d", fake.batchCalls, fake.singleCalls)
	}

	// Nothing is due any more.
	again := uc.VerifyWithDirectory(context.Background(), time.Now().Add(-cfg.UserCacheRecheck), 25)
	if again.Confirmed != 0 || again.Deleted != 0 || fake.batchCalls != 1 {
		t.Fatalf("second pass did work: %+v (batch calls %d)", again, fake.batchCalls)
	}
}

func TestVerifyWithDirectoryConfirmsBeforeDeleting(t *testing.T) {
	fake := newFakeCNS(t, CNSServiceProfile{})
	fake.setUser(4, "dave")
	fake.batchHides[4] = true
	cfg := testConfig(fake.server.URL)
	store := newMemoryUserStore()
	store.users[4] = dueUser(4, "dave")
	uc := NewUserCache(cfg, store, NewCNSClient(cfg))

	result := uc.VerifyWithDirectory(context.Background(), time.Now().Add(-cfg.UserCacheRecheck), 25)

	if result.Deleted != 0 || result.Confirmed != 1 {
		t.Fatalf("a user the batch left out but CNS still has must not be deleted: %+v", result)
	}
	if u, _ := store.GetUser(context.Background(), 4); u.Status != models.UserStatusActive || u.Username != "dave" {
		t.Fatalf("user changed: %+v", u)
	}
}

func TestVerifyWithDirectoryHoldsMassDeletion(t *testing.T) {
	fake := newFakeCNS(t, CNSServiceProfile{})
	cfg := testConfig(fake.server.URL)
	store := newMemoryUserStore()
	for id := int64(1); id <= 30; id++ {
		store.users[id] = dueUser(id, "user"+strconv.FormatInt(id, 10))
	}
	uc := NewUserCache(cfg, store, NewCNSClient(cfg))

	held := uc.VerifyWithDirectory(context.Background(), time.Now().Add(-cfg.UserCacheRecheck), 25)
	if !held.MassDeletionHeld || held.Missing != 30 || held.Deleted != 0 || fake.singleCalls != 0 {
		t.Fatalf("expected the deletions to be held: %+v (single calls %d)", held, fake.singleCalls)
	}
	for id := int64(1); id <= 30; id++ {
		if u, _ := store.GetUser(context.Background(), id); u.Status != models.UserStatusActive {
			t.Fatalf("user %d changed while held: %+v", id, u)
		}
	}

	released := uc.VerifyWithDirectory(context.Background(), time.Now().Add(-cfg.UserCacheRecheck), -1)
	if released.Deleted != 30 || released.MassDeletionHeld {
		t.Fatalf("an unlimited run should delete them all: %+v", released)
	}
}

func TestVerifyWithDirectoryStopsOnCNSErrors(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   error
	}{
		{http.StatusForbidden, `{"error":{"code":"insufficient_scope"}}`, ErrCNSDirectoryDenied},
		{http.StatusTooManyRequests, `{"error":{"code":"rate_limited"}}`, ErrCNSRateLimited},
		{http.StatusBadGateway, `bad gateway`, nil},
	}
	for _, tc := range cases {
		fake := newFakeCNS(t, CNSServiceProfile{})
		fake.directoryStatus, fake.directoryBody = tc.status, tc.body
		cfg := testConfig(fake.server.URL)
		store := newMemoryUserStore()
		store.users[1] = dueUser(1, "alice")
		before := store.users[1]
		uc := NewUserCache(cfg, store, NewCNSClient(cfg))

		result := uc.VerifyWithDirectory(context.Background(), time.Now().Add(-cfg.UserCacheRecheck), 25)

		if result.Err == nil || (tc.want != nil && !errors.Is(result.Err, tc.want)) {
			t.Fatalf("status %d: unexpected error %v", tc.status, result.Err)
		}
		if after := store.users[1]; after != before {
			t.Fatalf("status %d: row changed: %+v", tc.status, after)
		}
	}
}

func TestGetPublicUserOnlyTrustsCNSNotFound(t *testing.T) {
	fake := newFakeCNS(t, CNSServiceProfile{})
	client := NewCNSClient(testConfig(fake.server.URL))

	if _, err := client.GetPublicUser(context.Background(), 99); !errors.Is(err, ErrCNSUserNotFound) {
		t.Fatalf("CNS's not_found should map to ErrCNSUserNotFound, got %v", err)
	}

	fake.directoryStatus, fake.directoryBody = http.StatusNotFound, `<html>404 page not found</html>`
	if _, err := client.GetPublicUser(context.Background(), 99); err == nil || errors.Is(err, ErrCNSUserNotFound) {
		t.Fatalf("a 404 that isn't CNS's must be an error, not a deletion: %v", err)
	}
	fake.directoryBody = `{"error":{"code":"route_not_found"}}`
	if _, err := client.GetPublicUser(context.Background(), 99); err == nil || errors.Is(err, ErrCNSUserNotFound) {
		t.Fatalf("a 404 with another code must be an error, not a deletion: %v", err)
	}
}

func TestVerifyWithDirectoryPagesThroughLargeCaches(t *testing.T) {
	fake := newFakeCNS(t, CNSServiceProfile{})
	cfg := testConfig(fake.server.URL)
	store := newMemoryUserStore()
	for id := int64(1); id <= 250; id++ {
		fake.setUser(id, "u"+strconv.FormatInt(id, 10))
		store.users[id] = dueUser(id, "u"+strconv.FormatInt(id, 10))
	}
	uc := NewUserCache(cfg, store, NewCNSClient(cfg))

	result := uc.VerifyWithDirectory(context.Background(), time.Now().Add(-cfg.UserCacheRecheck), 25)

	if result.Confirmed != 250 || fake.batchCalls != 3 {
		t.Fatalf("expected 250 confirmations in 3 batches, got %+v in %d", result, fake.batchCalls)
	}
}

func TestReconcileRevivesStaleUserCNSStillHas(t *testing.T) {
	fake := newFakeCNS(t, CNSServiceProfile{})
	fake.setUser(5, "erin")
	cfg := testConfig(fake.server.URL)
	store := newMemoryUserStore()
	stale := dueUser(5, "erin")
	stale.LastSyncedAt = time.Now().Add(-40 * 24 * time.Hour)
	stale.Status = models.UserStatusInactive
	stale.InactiveReason = sql.NullString{String: models.InactiveReasonStale, Valid: true}
	store.users[5] = stale
	uc := NewUserCache(cfg, store, NewCNSClient(cfg))

	uc.runIfDue()

	if store.jobs != 1 {
		t.Fatalf("reconciliation should run through RunJob, ran %d times", store.jobs)
	}
	if u, _ := store.GetUser(context.Background(), 5); u.Status != models.UserStatusActive {
		t.Fatalf("CNS confirmed the user, so the stale row should be active again: %+v", u)
	}
}
