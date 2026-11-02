package handlers

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sendly/internal/accountstatus"
	"sendly/internal/config"
	"sendly/internal/i18n"
	"sendly/internal/middleware"

	"github.com/gin-gonic/gin"
)

func restrictedUser(status, reason string, expiresAt *time.Time) *middleware.CNSUser {
	return &middleware.CNSUser{ID: 9, Username: "carol", AccountStatus: &accountstatus.Info{Status: status, ReasonCode: reason, ReasonText: "Repeated spam reports", ExpiresAt: expiresAt}}
}

func TestBuildAccountStatusView(t *testing.T) {
	tr := i18n.NewTranslator()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	until := time.Date(2026, 12, 24, 18, 30, 0, 0, time.UTC)

	active := buildAccountStatusView(&middleware.CNSUser{ID: 1}, tr.Get("en"), "en", now)
	if active.Restricted || !active.Transfers || !active.QuickShare || !active.MemberLinks {
		t.Fatalf("active view: %+v", active)
	}
	guest := buildAccountStatusView(nil, tr.Get("en"), "en", now)
	if guest.Restricted || guest.Suspended {
		t.Fatalf("guest view: %+v", guest)
	}

	limited := buildAccountStatusView(restrictedUser("limited", "spam", &until), tr.Get("en"), "en", now)
	if !limited.Restricted || limited.Transfers || !limited.QuickShare || !limited.MemberLinks {
		t.Fatalf("limited view: %+v", limited)
	}
	if limited.Reason != "Reason: Spam" || limited.ReasonText != "Repeated spam reports" {
		t.Fatalf("limited reason: %q / %q", limited.Reason, limited.ReasonText)
	}
	if limited.Until != "This restriction ends on 24 December 2026, 18:30 UTC." {
		t.Fatalf("limited until: %q", limited.Until)
	}
	if len(limited.Items) != 2 {
		t.Fatalf("limited items: %v", limited.Items)
	}

	very := buildAccountStatusView(restrictedUser("very_limited", "made_up_code", nil), tr.Get("de"), "de", now)
	if very.QuickShare || very.MemberLinks || very.Reason != "Grund: Sonstiges" || very.Until != "" || len(very.Items) != 4 {
		t.Fatalf("very limited view: %+v", very)
	}
	if got := formatAccountStatusDate(until, "de"); got != "24. Dezember 2026, 18:30 UTC" {
		t.Fatalf("german date: %q", got)
	}

	suspended := buildAccountStatusView(restrictedUser("suspended", "", nil), tr.Get("en"), "en", now)
	if !suspended.Suspended || suspended.Reason != "" {
		t.Fatalf("suspended view: %+v", suspended)
	}
}

func renderPage(t *testing.T, user *middleware.CNSUser, path string, handle func(*PageHandler) gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.SetHTMLTemplate(template.Must(template.ParseGlob("../../web/templates/*.html")))
	r.Use(func(c *gin.Context) {
		c.Set(middleware.LocaleKey, "en")
		if user != nil {
			c.Set(middleware.CNSUserKey, user)
		}
		c.Next()
	})
	h := NewPageHandler(&config.Config{BaseURL: "https://sendly.test", CNSAuthURL: "https://accounts.test"}, i18n.NewTranslator(), nil)
	r.GET(path, handle(h))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestPagesRenderAccountStatus(t *testing.T) {
	pages := map[string]func(*PageHandler) gin.HandlerFunc{
		"/":           func(h *PageHandler) gin.HandlerFunc { return h.Index },
		"/quickshare": func(h *PageHandler) gin.HandlerFunc { return h.QuickShare },
		"/link":       func(h *PageHandler) gin.HandlerFunc { return h.Link },
		"/transfers":  func(h *PageHandler) gin.HandlerFunc { return h.Transfers },
		"/help":       func(h *PageHandler) gin.HandlerFunc { return h.HelpPage },
	}
	users := map[string]*middleware.CNSUser{
		"guest":        nil,
		"active":       {ID: 3, Username: "dave"},
		"limited":      restrictedUser("limited", "spam", nil),
		"very_limited": restrictedUser("very_limited", "abuse", nil),
	}
	for name, user := range users {
		for path, handle := range pages {
			w := renderPage(t, user, path, handle)
			if w.Code != http.StatusOK {
				t.Fatalf("%s %s: status %d", name, path, w.Code)
			}
			body := w.Body.String()
			restricted := user != nil && user.AccountStatus != nil
			if got := strings.Contains(body, `class="account-status-notice"`); got != restricted {
				t.Errorf("%s %s: notice shown = %v", name, path, got)
			}
			if !strings.Contains(body, `id="account-status-config"`) {
				t.Errorf("%s %s: missing account status config", name, path)
			}
		}
	}

	link := renderPage(t, users["limited"], "/link", pages["/link"]).Body.String()
	if !strings.Contains(link, "Sending to people is turned off") || strings.Contains(link, `id="share-recipient-input"`) {
		t.Error("limited users must see the restricted share section instead of the recipient picker")
	}
	transfers := renderPage(t, users["limited"], "/transfers", pages["/transfers"]).Body.String()
	if !strings.Contains(transfers, "Transfers are turned off for your account") {
		t.Error("limited users must be told transfers are off")
	}
	quick := renderPage(t, users["very_limited"], "/quickshare", pages["/quickshare"]).Body.String()
	if !strings.Contains(quick, "Quick Share is turned off for your account") || strings.Contains(quick, "quick-share.js") {
		t.Error("very limited users must not get the quick share app")
	}
	index := renderPage(t, users["very_limited"], "/", pages["/"]).Body.String()
	if strings.Contains(index, `href="/quickshare?action=create"`) {
		t.Error("very limited users must not get quick share links on the homepage")
	}
}

func TestSuspendedPage(t *testing.T) {
	until := time.Now().Add(48 * time.Hour)
	w := renderPage(t, restrictedUser("suspended", "tos_violation", &until), "/", func(h *PageHandler) gin.HandlerFunc { return h.Suspended })
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"Your account is currently suspended, and cannot use Sendly. If you think this is a mistake, you may",
		`href="https://accounts.cns.ad/help"`,
		"Reason: Terms of Service violation",
		"Repeated spam reports",
		"This restriction ends on",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("suspended page misses %q", want)
		}
	}
	if strings.Contains(body, "/auth/logout") {
		t.Error("suspended page must not offer logging out")
	}
}
