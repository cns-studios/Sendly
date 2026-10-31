package middleware

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func idToken(payload string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc([]byte(payload)) + ".sig"
}

func TestMatchLocale(t *testing.T) {
	for in, want := range map[string]string{"de": "de", "DE": "de", "de-AT": "de", "en_GB": "en", " en-US ": "en"} {
		if got, ok := MatchLocale(in); !ok || got != want {
			t.Errorf("MatchLocale(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "fr", "fr-DE", "x"} {
		if got, ok := MatchLocale(in); ok {
			t.Errorf("MatchLocale(%q) = %q; want no match", in, got)
		}
	}
}

func TestNegotiateLocaleHonoursQValues(t *testing.T) {
	cases := map[string]string{
		"de":                      "de",
		"fr,de;q=0.8":             "de",
		"fr,de;q=0.8,en;q=0.9":    "en",
		"en;q=0.3,de;q=0.9":       "de",
		"de;q=0,en":               "en",
		"de-AT,de;q=0.9,en;q=0.5": "de",
		"fr,es":                   "",
		"":                        "",
	}
	for in, want := range cases {
		if got := NegotiateLocale(in); got != want {
			t.Errorf("NegotiateLocale(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestLocaleFromIDToken(t *testing.T) {
	if got, ok := LocaleFromIDToken(idToken(`{"sub":"1","locale":"de-AT"}`)); !ok || got != "de" {
		t.Errorf("got %q, %v; want de", got, ok)
	}
	for name, tok := range map[string]string{
		"no claim":       idToken(`{"sub":"1"}`),
		"unsupported":    idToken(`{"locale":"fr"}`),
		"not a jwt":      "abc",
		"bad base64":     "a.!!!.c",
		"bad json":       "a." + base64.RawURLEncoding.EncodeToString([]byte("nope")) + ".c",
		"empty":          "",
		"claim not text": idToken(`{"locale":5}`),
	} {
		if got, ok := LocaleFromIDToken(tok); ok {
			t.Errorf("%s: got %q; want no locale", name, got)
		}
	}
}

func newCtx(url string, headers map[string]string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, url, nil)
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	return c, rec
}

func TestDetectLocalePrecedence(t *testing.T) {
	// ?lang= beats the cookie and Accept-Language, and is remembered.
	c, rec := newCtx("/?lang=de", map[string]string{"Cookie": "lang=en", "Accept-Language": "en"})
	if got := detectLocale(c); got != "de" {
		t.Errorf("query: got %q", got)
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "lang=de") {
		t.Errorf("query choice not remembered: %q", rec.Header().Get("Set-Cookie"))
	}
	// The cookie beats Accept-Language.
	c, _ = newCtx("/", map[string]string{"Cookie": "lang=en", "Accept-Language": "de"})
	if got := detectLocale(c); got != "en" {
		t.Errorf("cookie: got %q", got)
	}
	// Accept-Language with q-values, not just a "de" prefix.
	c, _ = newCtx("/", map[string]string{"Accept-Language": "fr,de;q=0.8"})
	if got := detectLocale(c); got != "de" {
		t.Errorf("accept-language: got %q", got)
	}
	c, _ = newCtx("/", map[string]string{"Accept-Language": "fr"})
	if got := detectLocale(c); got != "en" {
		t.Errorf("default: got %q", got)
	}
}

func TestApplyAccountLocale(t *testing.T) {
	c, rec := newCtx("/", map[string]string{"Cookie": "lang=en"})
	ApplyAccountLocale(c, idToken(`{"locale":"de"}`))
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "lang=de") {
		t.Errorf("account language not applied: %q", rec.Header().Get("Set-Cookie"))
	}
	if GetLocale(c) != "de" {
		t.Errorf("request locale = %q, want de", GetLocale(c))
	}

	// No usable claim: whatever the visitor chose stays.
	c, rec = newCtx("/", nil)
	ApplyAccountLocale(c, idToken(`{"sub":"1"}`))
	ApplyAccountLocale(c, "")
	if rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("cookie changed without a claim: %q", rec.Header().Get("Set-Cookie"))
	}
}

func TestChoosingALanguageWhileSignedInIsReported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	type call struct{ token, locale string }
	got := make(chan call, 4)
	OnLocaleChosen = func(token, locale string) { got <- call{token, locale} }
	t.Cleanup(func() { OnLocaleChosen = nil })

	router := gin.New()
	router.Use(LocaleMiddleware())
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	do := func(url, cookie string) {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		router.ServeHTTP(httptest.NewRecorder(), req)
	}

	do("/?lang=de", "auth_token=tok")
	if c := <-got; c.token != "tok" || c.locale != "de" {
		t.Errorf("reported %+v", c)
	}
	// Signed out, or no explicit choice: nothing to save.
	do("/?lang=de", "")
	do("/", "auth_token=tok; lang=de")
	do("/?lang=fr", "auth_token=tok")
	select {
	case c := <-got:
		t.Errorf("unexpected report %+v", c)
	default:
	}
}
