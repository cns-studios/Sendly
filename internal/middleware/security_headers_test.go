package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSecurityHeadersSetsCSP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeaders("https://sendly.example"))
	r.GET("/", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	csp := w.Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"script-src 'self';",
		"default-src 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"connect-src 'self' blob: wss://sendly.example",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q missing %q", csp, want)
		}
	}
	if strings.Contains(csp, "script-src 'self' 'unsafe") {
		t.Errorf("script-src must not allow unsafe sources: %q", csp)
	}
}

func TestCSPUsesWSForHTTPBase(t *testing.T) {
	if got := ContentSecurityPolicy("http://localhost:8085"); !strings.Contains(got, "ws://localhost:8085") {
		t.Errorf("expected ws origin for http base URL, got %q", got)
	}
}

// TestFrontendHasNoThirdPartyOrInlineCode guards the CSP: templates must not
// load external resources, run inline scripts or use inline event handlers.
func TestFrontendHasNoThirdPartyOrInlineCode(t *testing.T) {
	webDir := filepath.Join("..", "..", "web")
	external := regexp.MustCompile(`(?i)(?:src|href)\s*=\s*"https?://[^"]*"`)
	scriptTag := regexp.MustCompile(`(?is)<script([^>]*)>(.*?)</script>`)
	handler := regexp.MustCompile(`(?i)\son[a-z]+\s*="`)
	cssExternal := regexp.MustCompile(`(?i)(?:@import|url\()\s*['"]?\s*(?:https?:)?//`)

	walk := func(dir string, fn func(path, content string)) {
		err := filepath.Walk(filepath.Join(webDir, dir), func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			fn(p, string(b))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	walk("templates", func(p, s string) {
		for _, m := range external.FindAllString(s, -1) {
			if strings.Contains(m, "rel=") {
				continue
			}
			if !strings.Contains(strings.ToLower(m), "href") {
				t.Errorf("%s: external resource %s", p, m)
			}
		}
		if strings.Contains(s, "unpkg.com") || strings.Contains(s, "cdnjs.") {
			t.Errorf("%s: references a CDN", p)
		}
		for _, m := range scriptTag.FindAllStringSubmatch(s, -1) {
			attrs := strings.ToLower(m[1])
			if strings.Contains(attrs, "src=") {
				if strings.Contains(attrs, "http") {
					t.Errorf("%s: external script %s", p, m[1])
				}
				continue
			}
			if !strings.Contains(attrs, "application/json") && !strings.Contains(attrs, "application/ld+json") {
				t.Errorf("%s: inline executable script", p)
			}
		}
		if handler.MatchString(s) {
			t.Errorf("%s: inline event handler attribute", p)
		}
		if regexp.MustCompile(`(?i)<link[^>]+href="https?://`).MatchString(s) {
			t.Errorf("%s: external <link>", p)
		}
	})

	walk(filepath.Join("static", "css"), func(p, s string) {
		if cssExternal.MatchString(s) {
			t.Errorf("%s: external CSS resource", p)
		}
	})
}
