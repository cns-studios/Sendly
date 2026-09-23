package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"sendly/internal/config"
)

// CNSClient talks to CNS's service-to-service API gateway (cfg.CNSServiceAPIURL):
// GET /api/service/me (a scoped profile read) and /api/data/{service}/ (a
// scoped per-service KV store), both authorized with this service's
// X-Service-Key plus the caller's own bearer token. This is a different host
// from cfg.CNSAuthURL, which is the user-self-service surface
// (/api/me, /api/account/me, /api/auth/token/refresh) that CNSAuthMiddleware
// uses to validate that bearer token in the first place; that flow, and its
// host, are untouched by this client.
type CNSClient struct {
	cfg        *config.Config
	httpClient *http.Client
}

func NewCNSClient(cfg *config.Config) *CNSClient {
	return &CNSClient{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// CNSServiceProfile is the shape returned by GET /api/service/me.
type CNSServiceProfile struct {
	ID      int64 `json:"id"`
	Profile struct {
		Avatar string `json:"avatar"`
	} `json:"profile"`
	Username string `json:"username"`
	IsActive bool   `json:"is_active"`
	Locked   bool   `json:"locked"`
}

func (c *CNSClient) GetServiceProfile(ctx context.Context, accessToken string) (*CNSServiceProfile, error) {
	if c.cfg.CNSServiceAPIURL == "" || c.cfg.CNSAuthServiceKey == "" {
		return nil, fmt.Errorf("cns service api is not configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(c.cfg.CNSServiceAPIURL, "/")+"/api/service/me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Service-Key", c.cfg.CNSAuthServiceKey)
	req.Header.Set("User-Agent", "Sendly-Auth-Bridge/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("cns service profile lookup failed with status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}

	var profile CNSServiceProfile
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, err
	}
	if profile.ID == 0 {
		return nil, fmt.Errorf("cns service profile resolved to empty user")
	}
	return &profile, nil
}

// PatchServiceData merges data into this service's per-user KV blob on CNS
// (PATCH /api/data/{service}/), so CNS stays aware of this service's own
// view of the user (status, tier, etc). Errors here never fail the caller's
// request; see UserCache.SyncIfStale.
func (c *CNSClient) PatchServiceData(ctx context.Context, accessToken string, data map[string]any) error {
	if c.cfg.CNSServiceAPIURL == "" || c.cfg.CNSAuthServiceKey == "" {
		return fmt.Errorf("cns service api is not configured")
	}

	body, err := json.Marshal(data)
	if err != nil {
		return err
	}

	url := strings.TrimSuffix(c.cfg.CNSServiceAPIURL, "/") + "/api/data/" + c.cfg.CNSServiceSlug + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-Service-Key", c.cfg.CNSAuthServiceKey)
	req.Header.Set("User-Agent", "Sendly-Auth-Bridge/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("cns service data patch failed with status %d: %s", resp.StatusCode, bytes.TrimSpace(respBody))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
