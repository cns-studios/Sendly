package cnshttp

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func get(url string) func(context.Context) (*http.Request, error) {
	return func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	}
}

func statusServer(t *testing.T, statuses ...int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1)) - 1
		w.WriteHeader(statuses[min(n, len(statuses)-1)])
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestDoRetriesUnavailableOnce(t *testing.T) {
	srv, calls := statusServer(t, http.StatusServiceUnavailable, http.StatusOK)
	resp, err := Do(context.Background(), nil, false, get(srv.URL))
	if err != nil || resp.StatusCode != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("got %v, %v after %d calls", resp, err, calls.Load())
	}
}

func TestDoGivesUpAfterOneRetry(t *testing.T) {
	srv, calls := statusServer(t, http.StatusServiceUnavailable)
	resp, err := Do(context.Background(), nil, true, get(srv.URL))
	if err != nil || resp.StatusCode != http.StatusServiceUnavailable || calls.Load() != 2 {
		t.Fatalf("got %v, %v after %d calls", resp, err, calls.Load())
	}
}

func TestDoRetriesBadGatewayOnlyWhenIdempotent(t *testing.T) {
	srv, calls := statusServer(t, http.StatusBadGateway, http.StatusOK)
	resp, _ := Do(context.Background(), nil, false, get(srv.URL))
	if resp.StatusCode != http.StatusBadGateway || calls.Load() != 1 {
		t.Fatalf("non-idempotent: status %d after %d calls", resp.StatusCode, calls.Load())
	}
	calls.Store(0)
	resp, _ = Do(context.Background(), nil, true, get(srv.URL))
	if resp.StatusCode != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("idempotent: status %d after %d calls", resp.StatusCode, calls.Load())
	}
}

func TestDoNeverRetriesClientErrors(t *testing.T) {
	srv, calls := statusServer(t, http.StatusUnauthorized, http.StatusOK)
	resp, _ := Do(context.Background(), nil, true, get(srv.URL))
	if resp.StatusCode != http.StatusUnauthorized || calls.Load() != 1 {
		t.Fatalf("status %d after %d calls", resp.StatusCode, calls.Load())
	}
}

func TestDoRetriesRefusedConnection(t *testing.T) {
	// A listener that is closed again: the port refuses connections.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + ln.Addr().String()
	ln.Close()

	var builds atomic.Int32
	_, err = Do(context.Background(), nil, false, func(ctx context.Context) (*http.Request, error) {
		builds.Add(1)
		return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	})
	if err == nil || builds.Load() != 2 {
		t.Fatalf("got %v after %d attempts, want an error after 2", err, builds.Load())
	}
}

func TestDoForwardsClientIP(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("CF-Connecting-IP"))
	}))
	t.Cleanup(srv.Close)

	ctx := WithClientIP(context.Background(), "203.0.113.7")
	if _, err := Do(ctx, nil, true, get(srv.URL)); err != nil {
		t.Fatal(err)
	}
	if got.Load() != "203.0.113.7" {
		t.Fatalf("CF-Connecting-IP = %q", got.Load())
	}

	if WithClientIP(context.Background(), "unknown").Value(clientIPKey{}) != nil {
		t.Fatal("an unparseable IP must not be recorded")
	}
}
