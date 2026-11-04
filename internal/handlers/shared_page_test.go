package handlers

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sendly/internal/config"
	"sendly/internal/i18n"
	"sendly/internal/middleware"

	"github.com/gin-gonic/gin"
)

func sharedPageRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.SetHTMLTemplate(template.Must(template.ParseGlob("../../web/templates/*.html")))
	r.Use(func(c *gin.Context) {
		c.Set(middleware.LocaleKey, "en")
		c.Next()
	})
	h := NewPageHandler(&config.Config{BaseURL: "https://sendly.test"}, i18n.NewTranslator(), nil)
	r.GET("/s", h.SharedLink)
	r.GET("/shared/:id", h.SharedFile)
	r.GET("/robots.txt", h.RobotsTXT)
	return r
}

func getPage(r http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestShortSharePageIsGeneric(t *testing.T) {
	w := getPage(sharedPageRouter(), "/s")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`<title>Someone sent you a file ➤ Sendly</title>`,
		`<meta property="og:title" content="Someone sent you a file ➤ Sendly">`,
		`<link rel="canonical" href="https://sendly.test/s">`,
		`hreflang="de" href="https://sendly.test/s?lang=de"`,
		`"fileID":""`,
		`/static/js/link-download.js`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/s is missing %s", want)
		}
	}
	if !strings.Contains(w.Header().Get("Set-Cookie"), "csrf_token=") {
		t.Error("/s must set the csrf cookie")
	}
}

func TestLegacySharePageKeepsWorkingWithoutLeakingMetadata(t *testing.T) {
	w := getPage(sharedPageRouter(), "/shared/abcdefghijklm0123")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`<title>Someone sent you a file ➤ Sendly</title>`,
		`"fileID":"abcdefghijklm0123"`,
		`<link rel="canonical" href="https://sendly.test/shared/abcdefghijklm0123">`,
		`hreflang="en" href="https://sendly.test/shared/abcdefghijklm0123"`,
		`id="missing-key-section"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/shared/:id is missing %s", want)
		}
	}
}

func TestRobotsKeepsSharePagesOutOfIndexes(t *testing.T) {
	body := getPage(sharedPageRouter(), "/robots.txt").Body.String()
	for _, want := range []string{"Disallow: /shared/\n", "Disallow: /s$\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("robots.txt is missing %q", want)
		}
	}
	if strings.Contains(body, "Disallow: /s\n") {
		t.Error("robots.txt must not block every path starting with /s")
	}
}
