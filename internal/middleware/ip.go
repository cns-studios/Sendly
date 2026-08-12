package middleware

import (
	"net"
	"strings"

	"sendly/internal/config"

	"github.com/gin-gonic/gin"
)

const (
	 
	ClientIPKey = "client_ip"
)

type IPMiddleware struct {
	behindCloudflare bool
}

func NewIPMiddleware(cfg *config.Config) *IPMiddleware {
	return &IPMiddleware{
		behindCloudflare: cfg.BehindCloudflare,
	}
}

func (m *IPMiddleware) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := m.getClientIP(c)
		c.Set(ClientIPKey, ip)
		c.Next()
	}
}

func (m *IPMiddleware) getClientIP(c *gin.Context) string {
	var ip string

	if m.behindCloudflare {
		 
		ip = c.GetHeader("CF-Connecting-IP")
		if ip != "" {
			return normalizeIP(ip)
		}

		 
		ip = c.GetHeader("X-Forwarded-For")
		if ip != "" {
			 
			ips := strings.Split(ip, ",")
			if len(ips) > 0 {
				return normalizeIP(strings.TrimSpace(ips[0]))
			}
		}

		 
		ip = c.GetHeader("X-Real-IP")
		if ip != "" {
			return normalizeIP(ip)
		}
	}

	 
	ip = c.ClientIP()
	if ip == "" {
		ip = c.Request.RemoteAddr
	}

	return normalizeIP(ip)
}

 
func normalizeIP(ip string) string {
	 
	if strings.Contains(ip, ":") {
		host, _, err := net.SplitHostPort(ip)
		if err == nil {
			ip = host
		}
	}

	 
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "unknown"
	}

	return parsed.String()
}

 
func GetClientIP(c *gin.Context) string {
	ip, exists := c.Get(ClientIPKey)
	if !exists {
		return "unknown"
	}
	
	ipStr, ok := ip.(string)
	if !ok {
		return "unknown"
	}
	
	return ipStr
}

 
func IsPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}

	 
	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"::1/128",
		"fc00::/7",
	}

	for _, cidr := range privateRanges {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(parsed) {
			return true
		}
	}

	return false
}