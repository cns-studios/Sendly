package handlers

import (
	"encoding/xml"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type sitemapURL struct {
	Loc string `xml:"loc"`
}

type sitemapURLSet struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

func (h *PageHandler) RobotsTXT(c *gin.Context) {
	baseURL := strings.TrimSuffix(h.cfg.BaseURL, "/")
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.String(http.StatusOK, "User-agent: *\nAllow: /\nDisallow: /api\n\nSitemap: %s/sitemap.xml\n", baseURL)
}

func (h *PageHandler) Sitemap(c *gin.Context) {
	baseURL := strings.TrimSuffix(h.cfg.BaseURL, "/")
	paths := []string{
		"/",
		"/link",
		"/quickshare",
		"/limits",
		"/data-encryption",
		"/help",
		"/tos",
		"/privacy",
	}
	urls := make([]sitemapURL, 0, len(paths))
	for _, p := range paths {
		urls = append(urls, sitemapURL{Loc: baseURL + p})
	}
	set := sitemapURLSet{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: urls}
	output, err := xml.MarshalIndent(set, "", "  ")
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Header("Content-Type", "application/xml; charset=utf-8")
	c.String(http.StatusOK, xml.Header+string(output)+"\n")
}
