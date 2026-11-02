package middleware

import (
	"encoding/base64"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

const LocaleKey = "locale"

const langCookie = "lang"

var supportedLocales = []string{"en", "de"}

// OnLocaleChosen, when set, is told about an explicit language choice made by
// a signed-in user (the ?lang= switch), so it can be saved to their CNS
// account. it runs in its own goroutine and must not block or panic the request.
var OnLocaleChosen func(accessToken, locale string)

func LocaleMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		locale := detectLocale(c)
		c.Set(LocaleKey, locale)
		if OnLocaleChosen != nil && c.Query("lang") == locale {
			if token, err := c.Cookie("auth_token"); err == nil && token != "" {
				go OnLocaleChosen(token, locale)
			}
		}
		c.Next()
	}
}

func GetLocale(c *gin.Context) string {
	locale, _ := c.Get(LocaleKey)
	if locale == "" {
		return "en"
	}
	return locale.(string)
}

// MatchLocale maps any BCP 47 tag ("de-AT", "en_GB", "DE") to a language
// sendly has a catalog for.
func MatchLocale(tag string) (string, bool) {
	lang := strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(lang, "-_"); i >= 0 {
		lang = lang[:i]
	}
	for _, s := range supportedLocales {
		if lang == s {
			return s, true
		}
	}
	return "", false
}

// NegotiateLocale picks the best supported language from an accept-language
// header, honouring q-values. it returns "" when none is supported.
func NegotiateLocale(header string) string {
	type candidate struct {
		tag   string
		q     float64
		order int
	}
	var list []candidate
	for i, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		if fields[0] == "" {
			continue
		}
		q := 1.0
		for _, p := range fields[1:] {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "q=") {
				if v, err := strconv.ParseFloat(p[2:], 64); err == nil {
					q = v
				} else {
					q = 0
				}
			}
		}
		if q > 0 {
			list = append(list, candidate{fields[0], q, i})
		}
	}
	sort.SliceStable(list, func(a, b int) bool {
		if list[a].q != list[b].q {
			return list[a].q > list[b].q
		}
		return list[a].order < list[b].order
	})
	for _, cand := range list {
		if l, ok := MatchLocale(cand.tag); ok {
			return l
		}
	}
	return ""
}

func detectLocale(c *gin.Context) string {
	if lang := c.Query("lang"); lang == "de" || lang == "en" {
		setLangCookie(c, lang)
		return lang
	}

	if lang, err := c.Cookie(langCookie); err == nil {
		if l, ok := MatchLocale(lang); ok {
			return l
		}
	}

	if l := NegotiateLocale(c.GetHeader("Accept-Language")); l != "" {
		return l
	}

	return "en"
}

func setLangCookie(c *gin.Context, lang string) {
	c.SetCookie(langCookie, lang, 31536000, "/", "", false, true)
}

// LocaleFromIDToken reads the OIDC "locale" claim from an ID token and maps
// it to a language sendly has. the token came straight from the CNS token
// endpoint over TLS, which OpenID connect core 3.1.3.7 allows to be trusted
// without checking the signature; the claim is only a display hint anyway.
func LocaleFromIDToken(idToken string) (string, bool) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", false
	}
	var claims struct {
		Locale string `json:"locale"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Locale == "" {
		return "", false
	}
	return MatchLocale(claims.Locale)
}

// ApplyAccountLocale makes the account's language (the locale claim) the
// site language. with no usable claim, the language the visitor already
// chose here stays.
func ApplyAccountLocale(c *gin.Context, idToken string) {
	if l, ok := LocaleFromIDToken(idToken); ok {
		setLangCookie(c, l)
		c.Set(LocaleKey, l)
	}
}
