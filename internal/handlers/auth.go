package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"shareit/internal/config"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	cfg *config.Config
}

type tokenExchangeResult struct {
	AccessToken string `json:"access_token"`
}

func NewAuthHandler(cfg *config.Config) *AuthHandler {
	return &AuthHandler{cfg: cfg}
}

func (h *AuthHandler) Login(c *gin.Context) {
	if h.cfg.CNSAuthURL == "" || h.cfg.CNSAuthClientID == "" {
		c.String(http.StatusInternalServerError, "CNS Auth is not configured")
		return
	}

	state := generateRandomHex(16)
	verifier := generateRandomHex(32)
	challenge := generateChallenge(verifier)

	isSecure := strings.HasPrefix(h.cfg.BaseURL, "https")

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("pkce_verifier", verifier, 600, "/", "", isSecure, true)
	c.SetCookie("pkce_state", state, 600, "/", "", isSecure, true)

	authURL := h.cfg.CNSAuthURL + "/login"
	redirectURI := h.cfg.BaseURL + "/auth/callback"

	val := url.Values{}
	val.Add("client_id", h.cfg.CNSAuthClientID)
	val.Add("redirect_uri", redirectURI)
	val.Add("response_type", "code")
	val.Add("code_challenge", challenge)
	val.Add("code_challenge_method", "S256")
	val.Add("state", state)
	val.Add("scope", "openid profile")

	c.Redirect(http.StatusFound, authURL+"?"+val.Encode())
}

func (h *AuthHandler) Callback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	authErr := c.Query("error")

	isSecure := strings.HasPrefix(h.cfg.BaseURL, "https")

	if authErr != "" {
		c.SetCookie("pkce_verifier", "", -1, "/", "", isSecure, true)
		c.SetCookie("pkce_state", "", -1, "/", "", isSecure, true)
		c.String(http.StatusBadRequest, "Authentication failed: %s", authErr)
		return
	}

	if code == "" || state == "" {
		c.SetCookie("pkce_verifier", "", -1, "/", "", isSecure, true)
		c.SetCookie("pkce_state", "", -1, "/", "", isSecure, true)
		c.String(http.StatusBadRequest, "Authentication callback missing required parameters")
		return
	}

	savedState, err := c.Cookie("pkce_state")
	if err != nil {
		c.SetCookie("pkce_verifier", "", -1, "/", "", isSecure, true)
		c.SetCookie("pkce_state", "", -1, "/", "", isSecure, true)
		c.String(http.StatusBadRequest, "Invalid state: missing pkce_state cookie. (Error: %v)", err)
		return
	}
	if savedState != state {
		fmt.Printf("State Mismatch: saved_cookie=%s, got_url=%s\n", savedState, state)
		c.SetCookie("pkce_verifier", "", -1, "/", "", isSecure, true)
		c.SetCookie("pkce_state", "", -1, "/", "", isSecure, true)
		c.String(http.StatusBadRequest, "Invalid state: mismatch. Cookie had '%s' but URL had '%s'.", savedState, state)
		return
	}

	verifier, err := c.Cookie("pkce_verifier")
	if err != nil {
		c.SetCookie("pkce_verifier", "", -1, "/", "", isSecure, true)
		c.SetCookie("pkce_state", "", -1, "/", "", isSecure, true)
		c.String(http.StatusBadRequest, "Missing verifier cookie. Your session may have expired.")
		return
	}

	redirectURI := h.cfg.BaseURL + "/auth/callback"
	result, exchangeErr := h.exchangeToken(code, verifier, h.cfg.CNSAuthClientID, redirectURI, state)
	if exchangeErr != nil {
		c.String(http.StatusBadGateway, "Token exchange failed: %v", exchangeErr)
		return
	}

	c.SetCookie("pkce_verifier", "", -1, "/", "", isSecure, true)
	c.SetCookie("pkce_state", "", -1, "/", "", isSecure, true)

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("auth_token", result.AccessToken, 3600*24*7, "/", "", isSecure, true)

	c.Redirect(http.StatusFound, "/")
}

func (h *AuthHandler) Logout(c *gin.Context) {
	isSecure := strings.HasPrefix(h.cfg.BaseURL, "https")
	c.SetCookie("auth_token", "", -1, "/", "", isSecure, true)
	c.Redirect(http.StatusFound, "/")
}

func generateRandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func generateChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func (h *AuthHandler) exchangeToken(code, verifier, clientID, redirectURI, state string) (*tokenExchangeResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tokenURL := h.cfg.CNSAuthURL + "/v2/token"

	jsonPayload := map[string]string{
		"code":          code,
		"code_verifier": verifier,
		"client_id":     clientID,
		"redirect_uri":  redirectURI,
		"state":         state,
	}
	body, _ := json.Marshal(jsonPayload)

	result, statusCode, rawBody, reqErr := doTokenRequest(ctx, tokenURL, "application/json", strings.NewReader(string(body)))
	if reqErr == nil && statusCode == http.StatusOK {
		return result, nil
	}

	formPayload := url.Values{}
	formPayload.Set("grant_type", "authorization_code")
	formPayload.Set("code", code)
	formPayload.Set("code_verifier", verifier)
	formPayload.Set("client_id", clientID)
	formPayload.Set("redirect_uri", redirectURI)
	formPayload.Set("state", state)

	fallbackResult, fallbackStatus, fallbackRawBody, fallbackErr := doTokenRequest(ctx, tokenURL, "application/x-www-form-urlencoded", strings.NewReader(formPayload.Encode()))
	if fallbackErr == nil && fallbackStatus == http.StatusOK {
		return fallbackResult, nil
	}

	if fallbackErr != nil {
		return nil, fmt.Errorf("primary request failed (status=%d body=%q): %v; fallback request failed: %v", statusCode, limitText(rawBody, 500), reqErr, fallbackErr)
	}

	return nil, fmt.Errorf("primary request failed (status=%d body=%q): %v; fallback status=%d body=%q", statusCode, limitText(rawBody, 500), reqErr, fallbackStatus, limitText(fallbackRawBody, 500))
}

func doTokenRequest(ctx context.Context, tokenURL, contentType string, body io.Reader) (*tokenExchangeResult, int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, body)
	if err != nil {
		return nil, 0, "", err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, "", err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	rawBody := strings.TrimSpace(string(raw))

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, rawBody, fmt.Errorf("upstream status %s", resp.Status)
	}

	var result tokenExchangeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, resp.StatusCode, rawBody, fmt.Errorf("invalid token response: %w", err)
	}
	if result.AccessToken == "" {
		return nil, resp.StatusCode, rawBody, fmt.Errorf("token response missing access_token")
	}

	return &result, resp.StatusCode, rawBody, nil
}

func limitText(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
