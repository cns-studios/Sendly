package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sendly/internal/accountstatus"
	"sendly/internal/config"

	"github.com/gin-gonic/gin"
)

func userWithStatus(status string, expiresAt *time.Time) *CNSUser {
	return &CNSUser{ID: 5, Username: "bob", AccountStatus: &accountstatus.Info{Status: status, ExpiresAt: expiresAt}}
}

func TestValidateParsesAccountStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":7,"username":"alice","account_status":{"status":"limited","restrictions":["avatar_change"],"reason_code":"spam","reason_text":"Too many messages","expires_at":"2099-01-01T00:00:00Z"}}`))
	}))
	t.Cleanup(srv.Close)

	user, err := ValidateCNSAccessToken(context.Background(), &config.Config{CNSAuthURL: srv.URL}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if user.AccountStatus == nil || user.AccountStatus.Status != "limited" || user.AccountStatus.ReasonCode != "spam" ||
		user.AccountStatus.ReasonText != "Too many messages" || user.AccountStatus.ExpiresAt == nil {
		t.Fatalf("account status not parsed: %+v", user.AccountStatus)
	}
	if UserStatus(user) != accountstatus.Limited {
		t.Fatalf("UserStatus = %q", UserStatus(user))
	}
}

func TestUserStatusWithoutClaimIsActive(t *testing.T) {
	if UserStatus(&CNSUser{ID: 1}) != accountstatus.Active || UserStatus(nil) != accountstatus.Active {
		t.Fatal("missing account status must be active")
	}
	past := time.Now().Add(-time.Minute)
	if UserStatus(userWithStatus("suspended", &past)) != accountstatus.Active {
		t.Fatal("expired restriction must be active")
	}
}

func gatedRouter(user *CNSUser) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if user != nil {
			c.Set(CNSUserKey, user)
		}
		c.Next()
	})
	r.Use(AccountStatusGate(func(c *gin.Context) { c.String(http.StatusForbidden, "suspended page") }))
	ok := func(c *gin.Context) { c.String(http.StatusOK, "ok") }
	for _, path := range []string{"/", "/transfers", "/tos", "/auth/logout", "/static/css/style.css", "/api/me/transfers", "/desktop/files"} {
		r.GET(path, ok)
	}
	r.POST("/api/upload/init", ok)
	return r
}

func TestAccountStatusGate(t *testing.T) {
	cases := []struct {
		name   string
		user   *CNSUser
		method string
		path   string
		status int
		body   string
	}{
		{"guest", nil, http.MethodGet, "/transfers", http.StatusOK, "ok"},
		{"active", userWithStatus("active", nil), http.MethodGet, "/transfers", http.StatusOK, "ok"},
		{"very limited", userWithStatus("very_limited", nil), http.MethodGet, "/", http.StatusOK, "ok"},
		{"suspended page", userWithStatus("suspended", nil), http.MethodGet, "/", http.StatusForbidden, "suspended page"},
		{"suspended api", userWithStatus("suspended", nil), http.MethodGet, "/api/me/transfers", http.StatusForbidden, ""},
		{"suspended post", userWithStatus("suspended", nil), http.MethodPost, "/api/upload/init", http.StatusForbidden, ""},
		{"suspended desktop", userWithStatus("suspended", nil), http.MethodGet, "/desktop/files", http.StatusForbidden, ""},
		{"suspended tos", userWithStatus("suspended", nil), http.MethodGet, "/tos", http.StatusOK, "ok"},
		{"suspended logout", userWithStatus("suspended", nil), http.MethodGet, "/auth/logout", http.StatusOK, "ok"},
		{"suspended static", userWithStatus("suspended", nil), http.MethodGet, "/static/css/style.css", http.StatusOK, "ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			gatedRouter(tc.user).ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d", w.Code, tc.status)
			}
			if tc.body != "" && w.Body.String() != tc.body {
				t.Fatalf("body %q, want %q", w.Body.String(), tc.body)
			}
			if tc.body == "" && w.Body.String() == "ok" {
				t.Fatal("request went through")
			}
		})
	}
}

func TestRequireCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for status, want := range map[string]int{"active": http.StatusOK, "limited": http.StatusOK, "very_limited": http.StatusForbidden} {
		r := gin.New()
		user := userWithStatus(status, nil)
		r.Use(func(c *gin.Context) { c.Set(CNSUserKey, user); c.Next() })
		r.GET("/q", RequireCapability(accountstatus.QuickShare), func(c *gin.Context) { c.Status(http.StatusOK) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/q", nil))
		if w.Code != want {
			t.Errorf("%s: status %d, want %d", status, w.Code, want)
		}
	}
}

type recordingLimitStore struct {
	max int64
}

func (s *recordingLimitStore) CheckRateLimit(_ context.Context, _ string, max int64, _ time.Duration) (bool, error) {
	s.max = max
	return true, nil
}

func TestRateLimitIsStricterForRestrictedAccounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := map[string]int64{"active": 30, "limited": 10, "very_limited": 10}
	for status, want := range cases {
		store := &recordingLimitStore{}
		standard, _, _ := NewRateLimiterSet(&config.Config{RateLimitMaxPerMinute: 30, RateLimitWindowSeconds: 60, RestrictedRateLimitDivisor: 3}, store)
		r := gin.New()
		user := userWithStatus(status, nil)
		r.Use(func(c *gin.Context) { c.Set(CNSUserKey, user); c.Next() })
		r.GET("/x", standard.Handler(), func(c *gin.Context) { c.Status(http.StatusOK) })
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
		if store.max != want {
			t.Errorf("%s: limit %d, want %d", status, store.max, want)
		}
	}
}

func TestGetTierFallsBackToGuestForVeryLimited(t *testing.T) {
	cfg := &config.Config{MaxFileSize: 100, AuthMaxFileSize: 200}
	if GetTier(cfg, userWithStatus("limited", nil)).MaxFileSize != 200 {
		t.Error("limited users keep the member tier")
	}
	tier := GetTier(cfg, userWithStatus("very_limited", nil))
	if tier.MaxFileSize != 100 || !tier.IsDurationAllowed("7d") || tier.IsDurationAllowed("90d") {
		t.Errorf("very limited users get the guest tier: %+v", tier)
	}
}
