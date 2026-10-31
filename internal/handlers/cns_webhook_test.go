package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"sendly/internal/models"

	"github.com/gin-gonic/gin"
)

type fakeWebhookStore struct {
	updated []models.CNSUserProfile
	deleted []int64
	events  []string
	err     error
}

func (f *fakeWebhookStore) ApplyCNSUserUpdated(_ context.Context, eventID string, p models.CNSUserProfile) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	f.events = append(f.events, eventID)
	f.updated = append(f.updated, p)
	return true, nil
}

func (f *fakeWebhookStore) ApplyCNSUserDeleted(_ context.Context, eventID string, id int64) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	f.events = append(f.events, eventID)
	f.deleted = append(f.deleted, id)
	return true, nil
}

const testWebhookSecret = "whsec_test_secret"

func signCNS(secret string, ts int64, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10) + "." + body))
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func postWebhook(t *testing.T, h *CNSWebhookHandler, body string, header func(http.Header)) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/webhooks/cns", h.Handle)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/cns", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	ts := time.Now().Unix()
	req.Header.Set("CNS-Webhook-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("CNS-Webhook-Signature", signCNS(testWebhookSecret, ts, body))
	if header != nil {
		header(req.Header)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

const updatedEvent = `{"id":"evt-1","type":"user.updated","created_at":"2026-10-31T10:00:00Z",` +
	`"data":{"user":{"id":42,"username":"alice2","display_name":null,"avatar":"https://cdn/a.png","created_at":"2026-01-01T00:00:00Z"},"changes":["username"]}}`

func TestCNSWebhookAppliesUserUpdated(t *testing.T) {
	store := &fakeWebhookStore{}
	w := postWebhook(t, NewCNSWebhookHandler([]string{testWebhookSecret}, store), updatedEvent, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if len(store.updated) != 1 || store.events[0] != "evt-1" {
		t.Fatalf("event not applied: %+v", store)
	}
	got := store.updated[0]
	want := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	if got.CNSUserID != 42 || got.Username != "alice2" || got.AvatarURL != "https://cdn/a.png" || !got.ObservedAt.Equal(want) {
		t.Fatalf("unexpected profile: %+v", got)
	}
}

func TestCNSWebhookAppliesUserDeleted(t *testing.T) {
	store := &fakeWebhookStore{}
	body := `{"id":"evt-2","type":"user.deleted","created_at":"2026-10-31T10:00:00Z","data":{"user_id":42}}`
	w := postWebhook(t, NewCNSWebhookHandler([]string{testWebhookSecret}, store), body, nil)
	if w.Code != http.StatusNoContent || len(store.deleted) != 1 || store.deleted[0] != 42 {
		t.Fatalf("status %d, store %+v", w.Code, store)
	}
}

func TestCNSWebhookAcceptsPingAndUnknownEvents(t *testing.T) {
	for _, body := range []string{
		`{"id":"evt-3","type":"ping","created_at":"2026-10-31T10:00:00Z","data":{"client_id":"sendly"}}`,
		`{"id":"evt-4","type":"user.something_new","created_at":"2026-10-31T10:00:00Z","data":{}}`,
	} {
		store := &fakeWebhookStore{}
		w := postWebhook(t, NewCNSWebhookHandler([]string{testWebhookSecret}, store), body, nil)
		if w.Code != http.StatusNoContent || len(store.events) != 0 {
			t.Fatalf("%s: status %d, store %+v", body, w.Code, store)
		}
	}
}

func TestCNSWebhookAcceptsAnyConfiguredSecret(t *testing.T) {
	store := &fakeWebhookStore{}
	h := NewCNSWebhookHandler([]string{"whsec_new", " " + testWebhookSecret + " "}, store)
	if w := postWebhook(t, h, updatedEvent, nil); w.Code != http.StatusNoContent {
		t.Fatalf("the previous secret should still verify during rotation: %d", w.Code)
	}
}

func TestCNSWebhookRejectsBadSignatures(t *testing.T) {
	now := time.Now().Unix()
	cases := map[string]func(http.Header){
		"wrong secret": func(h http.Header) { h.Set("CNS-Webhook-Signature", signCNS("whsec_other", now, updatedEvent)) },
		"no signature": func(h http.Header) { h.Del("CNS-Webhook-Signature") },
		"no v1 prefix": func(h http.Header) {
			h.Set("CNS-Webhook-Signature", strings.TrimPrefix(signCNS(testWebhookSecret, now, updatedEvent), "v1="))
		},
		"not hex":      func(h http.Header) { h.Set("CNS-Webhook-Signature", "v1=zz") },
		"no timestamp": func(h http.Header) { h.Del("CNS-Webhook-Timestamp") },
		"signed for another timestamp": func(h http.Header) {
			h.Set("CNS-Webhook-Signature", signCNS(testWebhookSecret, now-1, updatedEvent))
		},
		"replayed after 6 minutes": func(h http.Header) {
			old := now - 6*60
			h.Set("CNS-Webhook-Timestamp", strconv.FormatInt(old, 10))
			h.Set("CNS-Webhook-Signature", signCNS(testWebhookSecret, old, updatedEvent))
		},
		"timestamp 6 minutes ahead": func(h http.Header) {
			ahead := now + 6*60
			h.Set("CNS-Webhook-Timestamp", strconv.FormatInt(ahead, 10))
			h.Set("CNS-Webhook-Signature", signCNS(testWebhookSecret, ahead, updatedEvent))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			store := &fakeWebhookStore{}
			w := postWebhook(t, NewCNSWebhookHandler([]string{testWebhookSecret}, store), updatedEvent, mutate)
			if w.Code != http.StatusUnauthorized || len(store.events) != 0 {
				t.Fatalf("status %d, store %+v", w.Code, store)
			}
		})
	}
}

func TestCNSWebhookRejectsTamperedBody(t *testing.T) {
	store := &fakeWebhookStore{}
	h := NewCNSWebhookHandler([]string{testWebhookSecret}, store)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/webhooks/cns", h.Handle)
	ts := time.Now().Unix()
	tampered := strings.Replace(updatedEvent, "alice2", "mallory", 1)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/cns", strings.NewReader(tampered))
	req.Header.Set("CNS-Webhook-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("CNS-Webhook-Signature", signCNS(testWebhookSecret, ts, updatedEvent))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized || len(store.events) != 0 {
		t.Fatalf("status %d, store %+v", w.Code, store)
	}
}

func TestCNSWebhookRejectsMalformedEvents(t *testing.T) {
	for _, body := range []string{
		`not json`,
		`{"type":"user.deleted","created_at":"2026-10-31T10:00:00Z","data":{"user_id":42}}`,
		`{"id":"e","type":"user.deleted","created_at":"2026-10-31T10:00:00Z","data":{"user_id":0}}`,
		`{"id":"e","type":"user.deleted","created_at":"2026-10-31T10:00:00Z","data":{"user_id":"42"}}`,
		`{"id":"e","type":"user.updated","created_at":"2026-10-31T10:00:00Z","data":{"user":{"id":42,"username":""}}}`,
		`{"id":"e","type":"user.updated","data":{"user":{"id":42,"username":"alice"}}}`,
	} {
		store := &fakeWebhookStore{}
		w := postWebhook(t, NewCNSWebhookHandler([]string{testWebhookSecret}, store), body, nil)
		if w.Code != http.StatusBadRequest || len(store.events) != 0 {
			t.Fatalf("%s: status %d, store %+v", body, w.Code, store)
		}
	}
}

func TestCNSWebhookStoreFailureAsksForRetry(t *testing.T) {
	store := &fakeWebhookStore{err: errors.New("db down")}
	w := postWebhook(t, NewCNSWebhookHandler([]string{testWebhookSecret}, store), updatedEvent, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("a failed apply must not be acknowledged: %d", w.Code)
	}
}

func TestCNSWebhookDisabledWithoutSecret(t *testing.T) {
	store := &fakeWebhookStore{}
	w := postWebhook(t, NewCNSWebhookHandler([]string{" ", ""}, store), updatedEvent, nil)
	if w.Code != http.StatusNotFound || len(store.events) != 0 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestCNSWebhookRejectsOversizedBody(t *testing.T) {
	store := &fakeWebhookStore{}
	body := `{"id":"e","type":"ping","pad":"` + strings.Repeat("x", cnsWebhookMaxBody) + `"}`
	w := postWebhook(t, NewCNSWebhookHandler([]string{testWebhookSecret}, store), body, nil)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", w.Code)
	}
}

type fakeSignOut struct{ users []int64 }

func (f *fakeSignOut) SignedOut(_ context.Context, id int64) { f.users = append(f.users, id) }

const disconnectedEvent = `{"id":"evt-9","type":"app.disconnected","created_at":"2026-10-31T10:00:00Z","data":{"user_id":42,"client_id":"sendly"}}`

func TestCNSWebhookSignsOutOnAppDisconnected(t *testing.T) {
	store, out := &fakeWebhookStore{}, &fakeSignOut{}
	h := NewCNSWebhookHandler([]string{testWebhookSecret}, store)
	h.SetSignOutNotifier(out)
	w := postWebhook(t, h, disconnectedEvent, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if len(out.users) != 1 || out.users[0] != 42 {
		t.Fatalf("expected user 42 signed out once, got %v", out.users)
	}
	if len(store.deleted) != 0 || len(store.updated) != 0 {
		t.Fatal("a disconnect must not touch the cached profile")
	}
}

func TestCNSWebhookAppDisconnectedWithoutNotifierIsAcknowledged(t *testing.T) {
	w := postWebhook(t, NewCNSWebhookHandler([]string{testWebhookSecret}, &fakeWebhookStore{}), disconnectedEvent, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
}

func TestCNSWebhookRejectsAppDisconnectedWithoutUser(t *testing.T) {
	out := &fakeSignOut{}
	h := NewCNSWebhookHandler([]string{testWebhookSecret}, &fakeWebhookStore{})
	h.SetSignOutNotifier(out)
	body := `{"id":"evt-10","type":"app.disconnected","created_at":"2026-10-31T10:00:00Z","data":{"client_id":"sendly"}}`
	if w := postWebhook(t, h, body, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
	if len(out.users) != 0 {
		t.Fatal("nobody should be signed out")
	}
}
