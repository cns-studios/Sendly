package handlers

import (
	"encoding/xml"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type sitemapURL struct {
	Loc        string `xml:"loc"`
	LastMod    string `xml:"lastmod,omitempty"`
	ChangeFreq string `xml:"changefreq,omitempty"`
	Priority   string `xml:"priority,omitempty"`
}

type sitemapURLSet struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

func (h *PageHandler) RobotsTXT(c *gin.Context) {
	baseURL := strings.TrimSuffix(h.cfg.BaseURL, "/")
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.String(http.StatusOK, "User-agent: *\nAllow: /\nDisallow: /api\nDisallow: /android\nDisallow: /desktop\nDisallow: /auth\nDisallow: /shared/\n\nSitemap: %s/sitemap.xml\n", baseURL)
}

func (h *PageHandler) Sitemap(c *gin.Context) {
	baseURL := strings.TrimSuffix(h.cfg.BaseURL, "/")
	now := time.Now().Format("2006-01-02")

	urls := []sitemapURL{
		{Loc: baseURL + "/", LastMod: now, ChangeFreq: "weekly", Priority: "1.0"},
		{Loc: baseURL + "/link", LastMod: now, ChangeFreq: "weekly", Priority: "0.9"},
		{Loc: baseURL + "/quickshare", LastMod: now, ChangeFreq: "weekly", Priority: "0.9"},
		{Loc: baseURL + "/limits", LastMod: now, ChangeFreq: "monthly", Priority: "0.7"},
		{Loc: baseURL + "/data-encryption", LastMod: now, ChangeFreq: "monthly", Priority: "0.7"},
		{Loc: baseURL + "/help", LastMod: now, ChangeFreq: "monthly", Priority: "0.7"},
		{Loc: baseURL + "/tos", LastMod: "2026-04-05", ChangeFreq: "yearly", Priority: "0.5"},
		{Loc: baseURL + "/privacy", LastMod: "2026-04-04", ChangeFreq: "yearly", Priority: "0.5"},
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

func (h *PageHandler) SecurityTXT(c *gin.Context) {
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.File("./web/static/.well-known/security.txt")
}
