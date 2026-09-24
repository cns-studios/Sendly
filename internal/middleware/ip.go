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
	trustedProxies   []*net.IPNet
}

func NewIPMiddleware(cfg *config.Config) *IPMiddleware {
	return &IPMiddleware{
		behindCloudflare: cfg.BehindCloudflare,
		trustedProxies:   parseProxyNets(cfg.TrustedProxies),
	}
}

func (m *IPMiddleware) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := m.getClientIP(c)
		c.Set(ClientIPKey, ip)
		c.Next()
	}
}

// getClientIP only believes forwarding headers when the direct peer is a
// configured trusted proxy; otherwise any client could claim any IP and
// sidestep IP-keyed rate limits and report de-duplication.
func (m *IPMiddleware) getClientIP(c *gin.Context) string {
	if m.behindCloudflare && m.isTrustedProxy(remotePeerIP(c)) {
		if ip := c.GetHeader("CF-Connecting-IP"); ip != "" {
			if normalized := normalizeIP(ip); normalized != "unknown" {
				return normalized
			}
		}
	}

	// gin only honors X-Forwarded-For / X-Real-IP from the trusted proxies
	// configured on the router (see SetTrustedProxies in main).
	ip := c.ClientIP()
	if ip == "" {
		ip = c.Request.RemoteAddr
	}

	return normalizeIP(ip)
}

func (m *IPMiddleware) isTrustedProxy(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, network := range m.trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func remotePeerIP(c *gin.Context) net.IP {
	host, _, err := net.SplitHostPort(strings.TrimSpace(c.Request.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(c.Request.RemoteAddr)
	}
	return net.ParseIP(host)
}

func parseProxyNets(entries []string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(entries))
	for _, entry := range entries {
		if _, network, err := net.ParseCIDR(entry); err == nil {
			nets = append(nets, network)
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			bits := 128
			if ip.To4() != nil {
				ip = ip.To4()
				bits = 32
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	return nets
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