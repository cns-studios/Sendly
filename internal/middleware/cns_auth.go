package middleware

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"sendly/internal/cnshttp"
	"sendly/internal/config"

	"github.com/gin-gonic/gin"
)

const CNSUserKey = "cns_user"
const CNSAccessTokenKey = "cns_access_token"

type CNSUser struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Avatar   string `json:"avatar,omitempty"`
}

func (u *CNSUser) UnmarshalJSON(data []byte) error {
	type cnsUserAlias CNSUser
	var payload struct {
		cnsUserAlias
		AvatarURL      string `json:"avatar_url"`
		AvatarURLCamel string `json:"avatarUrl"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	*u = CNSUser(payload.cnsUserAlias)
	if u.Avatar == "" {
		u.Avatar = payload.AvatarURL
	}
	if u.Avatar == "" {
		u.Avatar = payload.AvatarURLCamel
	}
	if u.Avatar == "" {
		var raw map[string]interface{}
		if err := json.Unmarshal(data, &raw); err == nil {
			u.Avatar = findAvatarURL(raw)
		}
	}
	return nil
}

func findAvatarURL(value interface{}) string {
	switch value := value.(type) {
	case map[string]interface{}:
		for key, nested := range value {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
			if normalized == "avatar" || normalized == "avatarurl" || normalized == "profileimage" || normalized == "profilepicture" || normalized == "picture" {
				if candidate, ok := nested.(string); ok && strings.HasPrefix(candidate, "http") {
					return candidate
				}
			}
			if candidate := findAvatarURL(nested); candidate != "" {
				return candidate
			}
		}
	case []interface{}:
		for _, nested := range value {
			if candidate := findAvatarURL(nested); candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

// CNSStatusError is a CNS answer other than success. Callers tell a rejected
// token (IsTokenRejected) apart from CNS being briefly unreachable, which
// must never sign the user out.
type CNSStatusError struct {
	Status int
}

func (e *CNSStatusError) Error() string {
	return fmt.Sprintf("token validation failed with status %d", e.Status)
}

// IsTokenRejected reports whether CNS itself refused the access token
// (401/403), as opposed to a network error or a 5xx.
func IsTokenRejected(err error) bool {
	var se *CNSStatusError
	return errors.As(err, &se) && (se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden)
}

// cnsValidateTimeout bounds one token check, including its one retry.
const cnsValidateTimeout = 4 * time.Second

func ValidateCNSAccessToken(ctx context.Context, cfg *config.Config, token string) (*CNSUser, error) {
	base := cfg.CNSAPIBaseURL()
	if base == "" {
		return nil, fmt.Errorf("cns auth is not configured")
	}
	if token == "" {
		return nil, fmt.Errorf("missing access token")
	}

	requestCtx, cancel := context.WithTimeout(ctx, cnsValidateTimeout)
	defer cancel()

	// /api/account/me is served by the CNS API and, with the bearer token,
	// also by the accounts site, so it works whichever of the two base
	// URLs is configured.
	resp, err := cnshttp.Do(requestCtx, http.DefaultClient, true, func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/account/me", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "Sendly-Auth-Bridge/1.0")
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &CNSStatusError{Status: resp.StatusCode}
	}

	var user CNSUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, err
	}
	if user.ID == 0 {
		return nil, fmt.Errorf("token resolved to empty user")
	}
	return &user, nil
}

type refreshTokenResult struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// tokenRefreshError carries the upstream status so callers can distinguish a
// dead token (401/403) from a transient upstream failure (5xx, network).
type tokenRefreshError struct {
	status int
	msg    string
}

const refreshResultCacheTTL = 10 * time.Second

var refreshCoordinator = struct {
	sync.Mutex
	results map[[32]byte]cachedRefreshResult
}{results: make(map[[32]byte]cachedRefreshResult)}

type cachedRefreshResult struct {
	result    *refreshTokenResult
	expiresAt time.Time
}

func (e *tokenRefreshError) Error() string { return e.msg }

func (e *tokenRefreshError) deadToken() bool {
	return e.status == http.StatusUnauthorized || e.status == http.StatusForbidden
}

// IsDeadTokenError reports whether the given refresh error means the token
// itself was rejected by the auth server (401/403) rather than an upstream
// outage or transient network failure.
func IsDeadTokenError(err error) bool {
	var tre *tokenRefreshError
	return errors.As(err, &tre) && tre.deadToken()
}

// RefreshStore lets replicas share the outcome of a token refresh;
// implemented by *storage.Postgres.
type RefreshStore interface {
	SharedRefresh(ctx context.Context, tokenID []byte, ttl time.Duration, refresh func() ([]byte, error)) ([]byte, bool, error)
}

var refreshStore RefreshStore

// SetRefreshStore makes token refreshes coordinate through store, so that
// concurrent requests presenting the same refresh token on different
// replicas redeem it with CNS only once. Without a store, refreshes are only
// coordinated within this process.
func SetRefreshStore(store RefreshStore) {
	refreshStore = store
}

// RefreshAccessToken redeems refreshToken with CNS. CNS rotates refresh
// tokens and treats a second redemption as theft, revoking the session, so
// callers presenting the same token within refreshResultCacheTTL get the
// first caller's result instead of a second refresh.
func RefreshAccessToken(ctx context.Context, cfg *config.Config, refreshToken string) (*refreshTokenResult, error) {
	if refreshStore == nil {
		return refreshAccessTokenLocally(ctx, cfg, refreshToken)
	}

	tokenID := sha256.Sum256([]byte("sendly/refresh-id\x00" + refreshToken))
	sealKey := sha256.Sum256([]byte("sendly/refresh-key\x00" + refreshToken))

	var fresh *refreshTokenResult
	sealed, cached, err := refreshStore.SharedRefresh(ctx, tokenID[:], refreshResultCacheTTL, func() ([]byte, error) {
		result, err := requestTokenRefresh(ctx, cfg, refreshToken)
		if err != nil {
			return nil, err
		}
		fresh = result
		plain, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return sealRefreshResult(sealKey, plain)
	})
	switch {
	case fresh != nil:
		// The token is redeemed: use the result even if storing it failed.
		return fresh, nil
	case err != nil:
		var tre *tokenRefreshError
		if errors.As(err, &tre) {
			return nil, err
		}
		// The store is unreachable and nothing was redeemed yet.
		log.Printf("Token refresh: shared store unavailable, refreshing locally: %v", err)
		return refreshAccessTokenLocally(ctx, cfg, refreshToken)
	}

	plain, err := openRefreshResult(sealKey, sealed)
	if err != nil {
		return nil, fmt.Errorf("stored refresh result unreadable (cached=%v): %w", cached, err)
	}
	var result refreshTokenResult
	if err := json.Unmarshal(plain, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// sealRefreshResult encrypts a refresh result under a key derived from the
// refresh token that produced it: the stored row is useless to anyone who
// does not already hold that token.
func sealRefreshResult(key [32]byte, plain []byte) ([]byte, error) {
	aead, err := refreshResultAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plain, nil), nil
}

func openRefreshResult(key [32]byte, sealed []byte) ([]byte, error) {
	aead, err := refreshResultAEAD(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < aead.NonceSize() {
		return nil, errors.New("sealed refresh result too short")
	}
	return aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], nil)
}

func refreshResultAEAD(key [32]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func refreshAccessTokenLocally(ctx context.Context, cfg *config.Config, refreshToken string) (*refreshTokenResult, error) {
	tokenKey := sha256.Sum256([]byte(refreshToken))
	refreshCoordinator.Lock()
	defer refreshCoordinator.Unlock()

	now := time.Now()
	for key, cached := range refreshCoordinator.results {
		if now.After(cached.expiresAt) {
			delete(refreshCoordinator.results, key)
		}
	}
	if cached, ok := refreshCoordinator.results[tokenKey]; ok {
		return cached.result, nil
	}

	result, err := requestTokenRefresh(ctx, cfg, refreshToken)
	if err != nil {
		return nil, err
	}
	refreshCoordinator.results[tokenKey] = cachedRefreshResult{
		result:    result,
		expiresAt: time.Now().Add(refreshResultCacheTTL),
	}
	return result, nil
}

func requestTokenRefresh(ctx context.Context, cfg *config.Config, refreshToken string) (*refreshTokenResult, error) {
	tokenURL := cfg.CNSAPIBaseURL() + "/api/auth/token/refresh"

	jsonPayload := map[string]string{
		"refresh_token": refreshToken,
		"client_id":     cfg.CNSAuthClientID,
	}
	body, err := json.Marshal(jsonPayload)
	if err != nil {
		return nil, err
	}
	// Repeating a refresh whose answer was lost is safe: CNS accepts the
	// just-rotated token again for 10 seconds.
	resp, err := cnshttp.Do(ctx, http.DefaultClient, true, func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "Sendly-Auth-Bridge/1.0")
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	rawBody := strings.TrimSpace(string(raw))

	if resp.StatusCode != http.StatusOK {
		return nil, &tokenRefreshError{status: resp.StatusCode, msg: fmt.Sprintf("token refresh failed with status %d: %s", resp.StatusCode, rawBody)}
	}

	var result refreshTokenResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("invalid refresh response: %w", err)
	}
	if result.AccessToken == "" {
		return nil, fmt.Errorf("refresh response missing access_token")
	}

	return &result, nil
}

func authCookieDomain(cfg *config.Config) string {
	if strings.Contains(cfg.BaseURL, "localhost") {
		return ""
	}
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	return "." + parsed.Hostname()
}

func setCookieForAuthScope(c *gin.Context, name, value string, maxAge int, cfg *config.Config) {
	isSecure := strings.HasPrefix(cfg.BaseURL, "https")
	domain := authCookieDomain(cfg)
	c.SetCookie(name, value, maxAge, "/", domain, isSecure, true)
	if domain != "" {
		// Remove older host-only cookies created before domain scoping was consistent.
		c.SetCookie(name, "", -1, "/", "", isSecure, true)
	}
}

func setAuthCookies(c *gin.Context, cfg *config.Config, token, refreshToken string, expiresIn int64) {
	maxAge := int(expiresIn)
	if maxAge <= 0 {
		maxAge = 86400
	}
	expiresAt := time.Now().Unix() + int64(maxAge)

	c.SetSameSite(http.SameSiteLaxMode)
	setCookieForAuthScope(c, "auth_token", token, 3600*24*30, cfg)
	setCookieForAuthScope(c, "auth_expires_at", fmt.Sprintf("%d", expiresAt), 3600*24*30, cfg)
	if refreshToken != "" {
		setCookieForAuthScope(c, "refresh_token", refreshToken, 3600*24*30, cfg)
	}
}

func clearAuthTokenCookie(c *gin.Context, cfg *config.Config) {
	isSecure := strings.HasPrefix(cfg.BaseURL, "https")
	for _, domain := range []string{"", authCookieDomain(cfg)} {
		c.SetCookie("auth_token", "", -1, "/", domain, isSecure, true)
		c.SetCookie("refresh_token", "", -1, "/", domain, isSecure, true)
		c.SetCookie("auth_expires_at", "", -1, "/", domain, isSecure, true)
	}
}

func clearRefreshTokenCookie(c *gin.Context, cfg *config.Config) {
	isSecure := strings.HasPrefix(cfg.BaseURL, "https")
	for _, domain := range []string{"", authCookieDomain(cfg)} {
		c.SetCookie("refresh_token", "", -1, "/", domain, isSecure, true)
	}
}

// ClearRefreshTokenCookie expires the refresh_token cookie on the response.
func ClearRefreshTokenCookie(c *gin.Context, cfg *config.Config) {
	clearRefreshTokenCookie(c, cfg)
}

// refreshAccessToken attempts to refresh the access token using the refresh token cookie.
// Returns the new access token on success, empty string on failure.
func refreshAccessToken(c *gin.Context, cfg *config.Config) (string, bool) {
	refreshToken, err := c.Cookie("refresh_token")
	if err != nil || refreshToken == "" {
		return "", false
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	result, refreshErr := RefreshAccessToken(ctx, cfg, refreshToken)
	if refreshErr != nil {
		// Only treat the token as dead when the auth server says so (401/403).
		// Transient failures (5xx, network) must not log the user out; the
		// next request simply retries. A dead token is cleared immediately so
		// subsequent requests don't keep hammering the refresh endpoint.
		if IsDeadTokenError(refreshErr) {
			clearRefreshTokenCookie(c, cfg)
		}
		return "", false
	}

	setAuthCookies(c, cfg, result.AccessToken, result.RefreshToken, result.ExpiresIn)
	return result.AccessToken, true
}

func CNSAuthMiddleware(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cfg.CNSAuthURL == "" || cfg.CNSAuthServiceKey == "" {
			c.Next()
			return
		}

		authToken, err := c.Cookie("auth_token")
		if err != nil || authToken == "" {
			c.Next()
			return
		}

		if expiresAtStr, cookieErr := c.Cookie("auth_expires_at"); cookieErr == nil {
			if expiresAt, parseErr := strconv.ParseInt(expiresAtStr, 10, 64); parseErr == nil {
				if time.Now().Unix() >= expiresAt-60 {
					if newToken, ok := refreshAccessToken(c, cfg); ok {
						authToken = newToken
					}
				}
			}
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		user, err := ValidateCNSAccessToken(ctx, cfg, authToken)
		if err != nil {
			// Only a token CNS refused is refreshed or cleared. A network
			// error or 5xx (CNS replaced mid-request, after one retry) leaves
			// the cookies alone; this request is served signed out and the
			// next one tries again.
			if IsTokenRejected(err) {
				if newToken, ok := refreshAccessToken(c, cfg); ok {
					newCtx, newCancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
					defer newCancel()
					user, err = ValidateCNSAccessToken(newCtx, cfg, newToken)
					newCancel()
					if err == nil {
						c.Set(CNSUserKey, user)
						c.Set(CNSAccessTokenKey, newToken)
						c.Next()
						return
					}
				}
				clearAuthTokenCookie(c, cfg)
			}
			c.Next()
			return
		}

		if user.Avatar == "" {
			if avatar, cookieErr := c.Cookie("auth_avatar"); cookieErr == nil {
				user.Avatar = avatar
			}
		}
		c.Set(CNSUserKey, user)
		c.Set(CNSAccessTokenKey, authToken)
		c.Next()
	}
}

func GetCNSUser(c *gin.Context) *CNSUser {
	val, exists := c.Get(CNSUserKey)
	if !exists {
		return nil
	}
	user, _ := val.(*CNSUser)
	return user
}

// GetCNSAccessToken returns the bearer token CNSAuthMiddleware just
// validated the current user against. This may differ from the raw
// "auth_token" request cookie if the middleware refreshed an expiring
// token during this request; callers that need to make their own
// authenticated CNS calls (e.g. UserCacheSyncMiddleware) must use this
// instead of re-reading the cookie, or they risk using an already-stale
// token that was valid when the request arrived but got rotated out from
// under them before this point in the middleware chain.
func GetCNSAccessToken(c *gin.Context) string {
	val, exists := c.Get(CNSAccessTokenKey)
	if !exists {
		return ""
	}
	token, _ := val.(string)
	return token
}
