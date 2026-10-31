package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sendly/internal/cnshttp"
	"sendly/internal/config"
)

// CNSClient talks to CNS's service-to-service API (serviceBase: CNS_API_URL,
// else CNS_SERVICE_API_URL):
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

// serviceBase is where /api/service/me and /api/data live: CNS_API_URL when
// set, else CNS_SERVICE_API_URL. Empty disables those calls.
func (c *CNSClient) serviceBase() string {
	for _, u := range []string{c.cfg.CNSAPIURL, c.cfg.CNSServiceAPIURL} {
		if u = strings.TrimSpace(u); u != "" {
			return strings.TrimSuffix(u, "/")
		}
	}
	return ""
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
	base := c.serviceBase()
	if base == "" || c.cfg.CNSAuthServiceKey == "" {
		return nil, fmt.Errorf("cns service api is not configured")
	}

	resp, err := cnshttp.Do(ctx, c.httpClient, true, func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/service/me", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("X-Service-Key", c.cfg.CNSAuthServiceKey)
		req.Header.Set("User-Agent", "Sendly-Auth-Bridge/1.0")
		return req, nil
	})
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
	if c.serviceBase() == "" || c.cfg.CNSAuthServiceKey == "" {
		return fmt.Errorf("cns service api is not configured")
	}

	body, err := json.Marshal(data)
	if err != nil {
		return err
	}

	url := c.serviceBase() + "/api/data/" + c.cfg.CNSServiceSlug + "/"
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

// CNSPublicProfile is a user as CNS's users:read directory returns them.
type CNSPublicProfile struct {
	ID       int64   `json:"id"`
	Username string  `json:"username"`
	Avatar   *string `json:"avatar"`
}

var (
	// ErrCNSUserNotFound means CNS answered that the user does not exist:
	// the account was deleted (CNS user ids are never reused).
	ErrCNSUserNotFound = errors.New("cns user not found")
	// ErrCNSDirectoryDenied means the service key is missing, invalid or
	// lacks the users:read scope.
	ErrCNSDirectoryDenied = errors.New("cns directory access denied (service key needs the users:read scope)")
	ErrCNSRateLimited     = errors.New("cns directory rate limited")
)

// cnsDirectoryBatchMax is the most ids CNS accepts per GET /api/users.
const cnsDirectoryBatchMax = 100

// DirectoryConfigured reports whether the users:read directory can be called.
// It lives on the CNS API (config.CNSAPIBaseURL), like /api/service/push;
// the accounts site at CNS_AUTH_URL does not serve it.
func (c *CNSClient) DirectoryConfigured() bool {
	return c.cfg.CNSAPIBaseURL() != "" && strings.TrimSpace(c.cfg.CNSAuthServiceKey) != ""
}

// GetPublicUsers looks up to cnsDirectoryBatchMax users by id. Users CNS
// doesn't return are deleted or never existed; confirm with GetPublicUser
// before acting on that.
func (c *CNSClient) GetPublicUsers(ctx context.Context, ids []int64) (map[int64]CNSPublicProfile, error) {
	if len(ids) == 0 {
		return map[int64]CNSPublicProfile{}, nil
	}
	if len(ids) > cnsDirectoryBatchMax {
		return nil, fmt.Errorf("at most %d ids per directory lookup", cnsDirectoryBatchMax)
	}
	parts := make([]string, len(ids))
	requested := make(map[int64]bool, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
		requested[id] = true
	}
	var body struct {
		Users []CNSPublicProfile `json:"users"`
	}
	if err := c.directoryGet(ctx, "/api/users?ids="+url.QueryEscape(strings.Join(parts, ",")), &body); err != nil {
		return nil, err
	}
	out := make(map[int64]CNSPublicProfile, len(body.Users))
	for _, u := range body.Users {
		if requested[u.ID] && strings.TrimSpace(u.Username) != "" {
			out[u.ID] = u
		}
	}
	return out, nil
}

// GetPublicUser looks up one user by id, returning ErrCNSUserNotFound only
// when CNS itself says so (404 with code not_found).
func (c *CNSClient) GetPublicUser(ctx context.Context, id int64) (*CNSPublicProfile, error) {
	var profile CNSPublicProfile
	if err := c.directoryGet(ctx, "/api/users/"+strconv.FormatInt(id, 10), &profile); err != nil {
		return nil, err
	}
	if profile.ID != id || strings.TrimSpace(profile.Username) == "" {
		return nil, fmt.Errorf("cns directory returned an unexpected profile for user %d", id)
	}
	return &profile, nil
}

func (c *CNSClient) directoryGet(ctx context.Context, path string, out any) error {
	if !c.DirectoryConfigured() {
		return fmt.Errorf("cns directory is not configured")
	}
	resp, err := cnshttp.Do(ctx, c.httpClient, true, func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.CNSAPIBaseURL()+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Service-Key", c.cfg.CNSAuthServiceKey)
		req.Header.Set("User-Agent", "Sendly-Auth-Bridge/1.0")
		return req, nil
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return json.Unmarshal(raw, out)
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrCNSDirectoryDenied
	case http.StatusTooManyRequests:
		return ErrCNSRateLimited
	case http.StatusNotFound:
		// Anything between here and CNS (a proxy, a wrong base URL) can
		// answer 404 too; only CNS's own error envelope means "no such user".
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &envelope) == nil && envelope.Error.Code == "not_found" {
			return ErrCNSUserNotFound
		}
	}
	return fmt.Errorf("cns directory %s failed with status %d: %s", strings.SplitN(path, "?", 2)[0], resp.StatusCode, bytes.TrimSpace(raw[:min(len(raw), 256)]))
}
