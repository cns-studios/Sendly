package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"sendly/internal/config"

	"github.com/gin-gonic/gin"
)

func TestValidateRetriesWhileCNSReplicaIsReplaced(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path != "/api/account/me" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("unexpected request %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"id":7,"username":"alice"}`))
	}))
	t.Cleanup(srv.Close)

	user, err := ValidateCNSAccessToken(context.Background(), &config.Config{CNSAuthURL: srv.URL}, "tok")
	if err != nil || user.ID != 7 || calls.Load() != 2 {
		t.Fatalf("got %+v, %v after %d calls", user, err, calls.Load())
	}
}

func TestValidateReportsRejectionSeparatelyFromOutage(t *testing.T) {
	for status, rejected := range map[int]bool{http.StatusUnauthorized: true, http.StatusForbidden: true, http.StatusInternalServerError: false} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		_, err := ValidateCNSAccessToken(context.Background(), &config.Config{CNSAuthURL: srv.URL}, "tok")
		srv.Close()
		if err == nil || IsTokenRejected(err) != rejected {
			t.Errorf("status %d: err %v, IsTokenRejected = %v", status, err, IsTokenRejected(err))
		}
	}
}

func TestValidatePrefersCNSAPIURL(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":1,"username":"a"}`))
	}))
	t.Cleanup(api.Close)
	cfg := &config.Config{CNSAuthURL: "http://127.0.0.1:1", CNSServiceAPIURL: "http://127.0.0.1:1", CNSAPIURL: api.URL + "/"}
	if _, err := ValidateCNSAccessToken(context.Background(), cfg, "tok"); err != nil {
		t.Fatal(err)
	}
}

// A token CNS rejects before its cookie says it expires (revoked, or a
// clock skew) is refreshed instead of leaving the user signed out.
func TestMiddlewareRefreshesRejectedToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var refreshes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/token/refresh":
			refreshes.Add(1)
			w.Write([]byte(`{"access_token":"fresh","refresh_token":"r2","expires_in":900}`))
		case "/api/account/me":
			if r.Header.Get("Authorization") != "Bearer fresh" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Write([]byte(`{"id":9,"username":"bob"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	cfg := &config.Config{CNSAuthURL: srv.URL, CNSAuthServiceKey: "sc_x", CNSAuthClientID: "sendly", BaseURL: "http://localhost"}

	var seen *CNSUser
	r := gin.New()
	r.Use(CNSAuthMiddleware(cfg))
	r.GET("/", func(c *gin.Context) { seen = GetCNSUser(c) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: "revoked"})
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "r1-" + t.Name()})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if seen == nil || seen.ID != 9 || refreshes.Load() != 1 {
		t.Fatalf("user %+v after %d refreshes", seen, refreshes.Load())
	}
	if !strings.Contains(strings.Join(rec.Header().Values("Set-Cookie"), ";"), "auth_token=fresh") {
		t.Fatal("refreshed access token not stored")
	}
}

// An outage must not sign the user out: the cookies stay for the next request.
func TestMiddlewareKeepsCookiesWhenCNSIsDown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	cfg := &config.Config{CNSAuthURL: srv.URL, CNSAuthServiceKey: "sc_x", BaseURL: "http://localhost"}

	r := gin.New()
	r.Use(CNSAuthMiddleware(cfg))
	r.GET("/", func(c *gin.Context) {})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: "tok"})
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "r1"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if cookies := rec.Header().Values("Set-Cookie"); len(cookies) != 0 {
		t.Fatalf("cookies changed during an outage: %v", cookies)
	}
}
