package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

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

	MaxFileSize           int64
	AutoDeleteReportCount int

	DiscordWebhookURL string

	CNSAuthURL             string
	CNSAuthClientID        string
	CNSAuthDesktopClientID string
	CNSAuthServiceKey      string
	AuthMaxFileSize        int64
	MigrationsDir          string

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
		PostgresUser:                   getEnv("POSTGRES_USER", "shareit"),
		PostgresPassword:               getEnv("POSTGRES_PASSWORD", "shareit"),
		PostgresDB:                     getEnv("POSTGRES_DB", "shareit"),
		RedisHost:                      getEnv("REDIS_HOST", "localhost"),
		RedisPort:                      getEnv("REDIS_PORT", "6379"),
		DataDir:                        getEnv("DATA_DIR", "./data"),
		ChunkDir:                       getEnv("CHUNK_DIR", ""),
		BehindCloudflare:               getEnvBool("BEHIND_CLOUDFLARE", false),
		MaxFileSize:                    getEnvInt64("MAX_FILE_SIZE", 786432000),
		AutoDeleteReportCount:          getEnvInt("AUTO_DELETE_REPORT_COUNT", 3),
		DiscordWebhookURL:              getEnv("DISCORD_WEBHOOK_URL", ""),
		CNSAuthURL:                     getEnv("CNS_AUTH_URL", ""),
		CNSAuthClientID:                getEnv("CNS_AUTH_CLIENT_ID", ""),
		CNSAuthDesktopClientID:         getEnv("CNS_AUTH_DESKTOP_CLIENT_ID", ""),
		CNSAuthServiceKey:              getEnv("CNS_AUTH_SERVICE_KEY", ""),
		AuthMaxFileSize:                getEnvInt64("AUTH_MAX_FILE_SIZE", 1610612736), // 1.5 GB
		MigrationsDir:                  getEnv("MIGRATIONS_DIR", "db/migrations"),
		RateLimitMaxPerMinute:          getEnvInt64("RATE_LIMIT_MAX_PER_MINUTE", 2),
		RateLimitWindowSeconds:         getEnvInt64("RATE_LIMIT_WINDOW_SECONDS", 60),
		StrictRateLimitMaxPerMinute:    getEnvInt64("RATE_LIMIT_STRICT_MAX_PER_MINUTE", 1),
		StrictRateLimitWindowSeconds:   getEnvInt64("RATE_LIMIT_STRICT_WINDOW_SECONDS", 60),
		DownloadRateLimitMaxPerMinute:  getEnvInt64("RATE_LIMIT_DOWNLOAD_MAX_PER_MINUTE", 10),
		DownloadRateLimitWindowSeconds: getEnvInt64("RATE_LIMIT_DOWNLOAD_WINDOW_SECONDS", 60),
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
	return nil
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
