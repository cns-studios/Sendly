package middleware

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sendly/internal/config"
)

// memoryRefreshStore behaves like storage.Postgres.SharedRefresh: one caller
// per token refreshes, the others wait and get its sealed result.
type memoryRefreshStore struct {
	mu      sync.Mutex
	results map[string][]byte
	down    bool
}

func (s *memoryRefreshStore) SharedRefresh(_ context.Context, tokenID []byte, _ time.Duration, refresh func() ([]byte, error)) ([]byte, bool, error) {
	if s.down {
		return nil, false, errors.New("database unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if sealed, ok := s.results[string(tokenID)]; ok {
		return sealed, true, nil
	}
	sealed, err := refresh()
	if err != nil {
		return nil, false, err
	}
	s.results[string(tokenID)] = sealed
	return sealed, false, nil
}

func countingCNS(t *testing.T, status int) (*config.Config, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(status)
		w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`))
	}))
	t.Cleanup(server.Close)
	return &config.Config{CNSAuthURL: server.URL, CNSAuthClientID: "sendly"}, &calls
}

// Concurrent requests carrying the same refresh token, spread over replicas,
// must redeem it with CNS once: a second redemption revokes the session.
func TestSharedRefreshRedeemsTokenOnce(t *testing.T) {
	store := &memoryRefreshStore{results: map[string][]byte{}}
	SetRefreshStore(store)
	t.Cleanup(func() { SetRefreshStore(nil) })
	cfg, calls := countingCNS(t, http.StatusOK)

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := RefreshAccessToken(context.Background(), cfg, "old-refresh")
			if err != nil {
				t.Error(err)
				return
			}
			if result.AccessToken != "new-access" || result.RefreshToken != "new-refresh" {
				t.Errorf("got %+v", result)
			}
		}()
	}
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("CNS refresh called %d times, want 1", calls.Load())
	}
	for _, sealed := range store.results {
		if bytes.Contains(sealed, []byte("new-refresh")) || bytes.Contains(sealed, []byte("new-access")) {
			t.Fatal("stored refresh result holds tokens in the clear")
		}
	}
}

func TestSharedRefreshKeepsDeadTokenErrors(t *testing.T) {
	SetRefreshStore(&memoryRefreshStore{results: map[string][]byte{}})
	t.Cleanup(func() { SetRefreshStore(nil) })
	cfg, _ := countingCNS(t, http.StatusUnauthorized)

	_, err := RefreshAccessToken(context.Background(), cfg, "revoked")
	if !IsDeadTokenError(err) {
		t.Fatalf("got %v, want a dead token error", err)
	}
}

func TestSharedRefreshFallsBackWhenStoreIsDown(t *testing.T) {
	SetRefreshStore(&memoryRefreshStore{down: true})
	t.Cleanup(func() { SetRefreshStore(nil) })
	cfg, calls := countingCNS(t, http.StatusOK)

	result, err := RefreshAccessToken(context.Background(), cfg, "fallback-"+time.Now().String())
	if err != nil || result.AccessToken != "new-access" || calls.Load() != 1 {
		t.Fatalf("got %+v, %v after %d calls", result, err, calls.Load())
	}
}
