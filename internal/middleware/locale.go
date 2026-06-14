package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

const LocaleKey = "locale"

func LocaleMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		locale := detectLocale(c)
		c.Set(LocaleKey, locale)
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

func detectLocale(c *gin.Context) string {
	if lang := c.Query("lang"); lang == "de" || lang == "en" {
		c.SetCookie("lang", lang, 31536000, "/", "", false, true)
		return lang
	}

	if lang, err := c.Cookie("lang"); err == nil {
		if lang == "de" || lang == "en" {
			return lang
		}
	}

	accept := c.GetHeader("Accept-Language")
	if strings.HasPrefix(accept, "de") {
		return "de"
	}

	return "en"
}
