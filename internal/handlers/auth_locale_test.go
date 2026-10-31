package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"sendly/internal/config"
	"sendly/internal/middleware"

	"github.com/gin-gonic/gin"
)

func TestLoginRedirectCarriesTheCurrentLanguageAsUILocales(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{BaseURL: "http://localhost:8085", CNSAuthURL: "https://accounts.example", CNSAuthClientID: "sendly"}
	router := gin.New()
	router.Use(middleware.LocaleMiddleware())
	router.GET("/auth/login", NewAuthHandler(cfg).Login)

	for name, tc := range map[string]struct{ url, accept, want string }{
		"accept-language": {"/auth/login", "de-DE,de;q=0.9", "de"},
		"explicit choice": {"/auth/login?lang=de", "en", "de"},
		"default":         {"/auth/login", "", "en"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.url, nil)
		if tc.accept != "" {
			req.Header.Set("Accept-Language", tc.accept)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("%s: status %d", name, rec.Code)
		}
		loc, err := url.Parse(rec.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		q := loc.Query()
		if q.Get("ui_locales") != tc.want || q.Get("scope") != "openid profile" || q.Get("client_id") != "sendly" {
			t.Errorf("%s: unexpected authorize URL %s", name, loc)
		}
	}
}

func TestTokenResponseKeepsTheIDToken(t *testing.T) {
	var r tokenExchangeResult
	if err := r.UnmarshalJSON([]byte(`{"access_token":"a","refresh_token":"r","expires_in":60,"id_token":"x.y.z"}`)); err != nil {
		t.Fatal(err)
	}
	if r.IDToken != "x.y.z" || r.AccessToken != "a" {
		t.Fatalf("got %+v", r)
	}
}
