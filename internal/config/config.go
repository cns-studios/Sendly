package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port       string
	BaseURL    string
	TOSVersion string

	PostgresHost     string
	PostgresPort     string
	PostgresUser     string
	PostgresPassword string
	PostgresDB       string

	RedisHost string
	RedisPort string

	DataDir  string
	ChunkDir string

	BehindCloudflare bool
	// TrustedProxies lists the proxy IPs/CIDRs whose forwarding headers
	// (X-Forwarded-For, CF-Connecting-IP) are believed. Requests from any
	// other peer are attributed to their direct remote address.
	TrustedProxies []string

	MaxFileSize           int64
	AutoDeleteReportCount int

	DiscordWebhookURL string

	ReportBotURL         string
	SendlyBotAPIKey     string
	StatsReportInterval  time.Duration

	CNSAuthURL             string
	CNSAuthClientID        string
	CNSAuthDesktopClientID string
	CNSAuthServiceKey      string
	// CNSServiceAPIURL is CNS's service-to-service API gateway
	// (GET /api/service/me, /api/data/{service}/*) — a different host from
	// CNSAuthURL, which is the user-facing accounts surface (/api/me,
	// /api/account/me, /api/auth/token/refresh) used for cookie-based
	// browser auth.
	CNSServiceAPIURL string
	CNSServiceSlug   string
	AuthMaxFileSize  int64
	MigrationsDir    string

	UserCacheTTL               time.Duration
	UserCacheReconcileInterval time.Duration
	UserCacheStaleAfter        time.Duration

	RateLimitMaxPerMinute          int64
	RateLimitWindowSeconds         int64
	StrictRateLimitMaxPerMinute    int64
	StrictRateLimitWindowSeconds   int64
	DownloadRateLimitMaxPerMinute  int64
	DownloadRateLimitWindowSeconds int64
}

func Load() (*Config, error) {

	_ = godotenv.Load()

	cfg := &Config{
		Port:                           getEnv("PORT", "8085"),
		BaseURL:                        getEnv("BASE_URL", "http://localhost:8085"),
		TOSVersion:                     getEnv("TOS_VERSION", "2026-04-05"),
		PostgresHost:                   getEnv("POSTGRES_HOST", "localhost"),
		PostgresPort:                   getEnv("POSTGRES_PORT", "5432"),
		PostgresUser:                   getEnv("POSTGRES_USER", "sendly"),
		PostgresPassword:               getEnv("POSTGRES_PASSWORD", "sendly"),
		PostgresDB:                     getEnv("POSTGRES_DB", "sendly"),
		RedisHost:                      getEnv("REDIS_HOST", "localhost"),
		RedisPort:                      getEnv("REDIS_PORT", "6379"),
		DataDir:                        getEnv("DATA_DIR", "./data"),
		ChunkDir:                       getEnv("CHUNK_DIR", ""),
		BehindCloudflare:               getEnvBool("BEHIND_CLOUDFLARE", false),
		TrustedProxies:                 getEnvList("TRUSTED_PROXIES"),
		MaxFileSize:                    getEnvInt64("MAX_FILE_SIZE", 786432000),
		AutoDeleteReportCount:          getEnvInt("AUTO_DELETE_REPORT_COUNT", 3),
		DiscordWebhookURL:              getEnv("DISCORD_WEBHOOK_URL", ""),
		ReportBotURL:                   getEnv("REPORT_BOT_URL", ""),
		SendlyBotAPIKey:               getEnv("SENDLY_BOT_API_KEY", ""),
		StatsReportInterval:            time.Duration(getEnvInt("STATS_REPORT_INTERVAL_MINUTES", 5)) * time.Minute,
		CNSAuthURL:                     getEnv("CNS_AUTH_URL", ""),
		CNSAuthClientID:                getEnv("CNS_AUTH_CLIENT_ID", ""),
		CNSAuthDesktopClientID:         getEnv("CNS_AUTH_DESKTOP_CLIENT_ID", ""),
		CNSAuthServiceKey:              getEnv("CNS_AUTH_SERVICE_KEY", ""),
		CNSServiceAPIURL:               getEnv("CNS_SERVICE_API_URL", ""),
		CNSServiceSlug:                 getEnv("CNS_SERVICE_SLUG", "sendly"),
		AuthMaxFileSize:                getEnvInt64("AUTH_MAX_FILE_SIZE", 1610612736), // 1.5 GB
		MigrationsDir:                  getEnv("MIGRATIONS_DIR", "db/migrations"),
		UserCacheTTL:                   time.Duration(getEnvInt("USER_CACHE_TTL_HOURS", 24)) * time.Hour,
		UserCacheReconcileInterval:     time.Duration(getEnvInt("USER_CACHE_RECONCILE_INTERVAL_MINUTES", 60)) * time.Minute,
		UserCacheStaleAfter:            time.Duration(getEnvInt("USER_CACHE_STALE_AFTER_DAYS", 30)) * 24 * time.Hour,
		RateLimitMaxPerMinute:          getEnvInt64("RATE_LIMIT_MAX_PER_MINUTE", 30),
		RateLimitWindowSeconds:         getEnvInt64("RATE_LIMIT_WINDOW_SECONDS", 60),
		StrictRateLimitMaxPerMinute:    getEnvInt64("RATE_LIMIT_STRICT_MAX_PER_MINUTE", 15),
		StrictRateLimitWindowSeconds:   getEnvInt64("RATE_LIMIT_STRICT_WINDOW_SECONDS", 60),
		DownloadRateLimitMaxPerMinute:  getEnvInt64("RATE_LIMIT_DOWNLOAD_MAX_PER_MINUTE", 60),
		DownloadRateLimitWindowSeconds: getEnvInt64("RATE_LIMIT_DOWNLOAD_WINDOW_SECONDS", 60),
	}

	if cfg.BehindCloudflare && len(cfg.TrustedProxies) == 0 {
		cfg.TrustedProxies = append([]string(nil), cloudflareIPRanges...)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) validate() error {
	if c.Port == "" {
		return fmt.Errorf("PORT is required")
	}
	if c.PostgresHost == "" {
		return fmt.Errorf("POSTGRES_HOST is required")
	}
	if c.PostgresPassword == "" {
		return fmt.Errorf("POSTGRES_PASSWORD is required")
	}
	if c.DataDir == "" {
		return fmt.Errorf("DATA_DIR is required")
	}
	for _, proxy := range c.TrustedProxies {
		if _, _, err := net.ParseCIDR(proxy); err == nil {
			continue
		}
		if net.ParseIP(proxy) == nil {
			return fmt.Errorf("TRUSTED_PROXIES entry %q is not an IP or CIDR", proxy)
		}
	}
	return nil
}

// cloudflareIPRanges are Cloudflare's published edge ranges
// (https://www.cloudflare.com/ips/). They are the default trusted proxies
// when BEHIND_CLOUDFLARE is set and TRUSTED_PROXIES is empty, i.e. when
// Cloudflare connects to this server directly.
var cloudflareIPRanges = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
	"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
	"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
	"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
	"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
}

func (c *Config) PostgresDSN() string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		c.PostgresHost,
		c.PostgresPort,
		c.PostgresUser,
		c.PostgresPassword,
		c.PostgresDB,
	)
}

func (c *Config) RedisAddr() string {
	return fmt.Sprintf("%s:%s", c.RedisHost, c.RedisPort)
}

func (c *Config) Hostname() string {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func (c *Config) IsProd() bool {
	env := strings.ToLower(getEnv("GIN_MODE", "debug"))
	return env == "release"
}

func (c *Config) DesktopOAuthClientID() string {
	if c.CNSAuthDesktopClientID != "" {
		return c.CNSAuthDesktopClientID
	}
	return c.CNSAuthClientID
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvList(key string) []string {
	var items []string
	for _, item := range strings.Split(os.Getenv(key), ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return defaultValue
		}
		return parsed
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return defaultValue
		}
		return parsed
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return defaultValue
		}
		return parsed
	}
	return defaultValue
}
