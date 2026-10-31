package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"sendly/internal/config"
	"sendly/internal/handlers"
	"sendly/internal/middleware"
	"sendly/internal/models"
	"sendly/internal/realtime"
	"sendly/internal/services"
	"sendly/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
)

// TestLiveCNSUserSync runs Sendly's user cache against a real CNS accounts
// service: Sendly's auth middleware and cache sync, CNS's webhook deliveries
// to Sendly's webhook route, and the users:read directory sweep.
//
// It needs a local accounts-api whose "sendly" OAuth client has its webhook
// pointed at http://<CNSLOCAL_WEBHOOK_ADDR>/webhooks/cns, plus:
//
//	CNSLOCAL_URL             accounts-api base URL
//	CNSLOCAL_DB_DSN          its Postgres (read only, for signup codes)
//	CNSLOCAL_SERVICE_KEY     service key named sendly with users:read
//	CNSLOCAL_NOSCOPE_KEY     service key without users:read
//	CNSLOCAL_WEBHOOK_SECRET  the sendly client's webhook secret
//	CNSLOCAL_WEBHOOK_ADDR    host:port to listen on for webhooks
//
// and SENDLY_LIVE_INTEGRATION=1 with Sendly's POSTGRES_* settings.
func TestLiveCNSUserSync(t *testing.T) {
	if os.Getenv("SENDLY_LIVE_INTEGRATION") != "1" || os.Getenv("CNSLOCAL_URL") == "" {
		t.Skip("set SENDLY_LIVE_INTEGRATION=1 and CNSLOCAL_URL to run against a local CNS")
	}
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	t.Setenv("DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("CHUNK_DIR", filepath.Join(root, "chunks"))
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.CNSAuthURL = os.Getenv("CNSLOCAL_URL")
	cfg.CNSServiceAPIURL = cfg.CNSAuthURL
	cfg.CNSAPIURL = cfg.CNSAuthURL
	cfg.CNSAuthClientID = "sendly"
	cfg.CNSAuthServiceKey = os.Getenv("CNSLOCAL_SERVICE_KEY")
	cfg.CNSServiceSlug = "sendly"
	cfg.CNSWebhookSecrets = []string{os.Getenv("CNSLOCAL_WEBHOOK_SECRET")}
	cfg.BaseURL = "https://send.cns.ad"

	db, err := storage.NewPostgres(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.RunMigrations(context.Background(), cfg.MigrationsDir); err != nil {
		t.Fatal(err)
	}
	cnsDB, err := sqlx.Open("postgres", os.Getenv("CNSLOCAL_DB_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer cnsDB.Close()

	cnsClient := services.NewCNSClient(cfg)
	userCache := services.NewUserCache(cfg, db, cnsClient)
	recent := handlers.NewRecentUploadsHandler(cfg, db, realtime.NewHub(nil))
	router := gin.New()
	router.Use(middleware.CNSAuthMiddleware(cfg))
	router.Use(middleware.UserCacheSyncMiddleware(userCache))
	router.POST("/webhooks/cns", handlers.NewCNSWebhookHandler(cfg.CNSWebhookSecrets, db).Handle)
	router.GET("/api/users/lookup", recent.LookupUsers)
	router.GET("/api/users/:id/identity-key", recent.GetUserIdentityKey)

	listener, err := net.Listen("tcp", os.Getenv("CNSLOCAL_WEBHOOK_ADDR"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: router}
	go server.Serve(listener)
	defer server.Close()

	cns := &cnsLocal{t: t, base: cfg.CNSAuthURL, db: cnsDB}
	// The shared test database also holds made-up users from other live
	// tests, which CNS doesn't know; the sweep may delete them all. The
	// mass-deletion limit has its own unit tests.
	sweep := func() services.DirectoryResult { return userCache.VerifyWithDirectory(context.Background(), time.Now(), -1) }
	ctx := context.Background()
	const pw = "sendly sync check pw 1"

	// signIn opens a Sendly session and makes one request with it, which is
	// how the cache learns about a user.
	signIn := func(username string) cnsTokens {
		t.Helper()
		tokens := cns.sendlyLogin(username, pw)
		w := sendlyRequest(router, tokens, "/api/users/lookup?q=zz")
		if w.Code != http.StatusOK {
			t.Fatalf("signed-in Sendly request for %s: %d %s", username, w.Code, w.Body)
		}
		return tokens
	}
	cached := func(id int64) *models.User {
		u, err := db.GetUser(ctx, id)
		if errors.Is(err, models.ErrUserNotFound) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	search := func(as cnsTokens, q string) []int64 {
		t.Helper()
		w := sendlyRequest(router, as, "/api/users/lookup?q="+q)
		if w.Code != http.StatusOK {
			t.Fatalf("lookup %q: %d %s", q, w.Code, w.Body)
		}
		var body struct {
			Items []struct {
				UserID int64 `json:"user_id"`
			} `json:"items"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		ids := []int64{}
		for _, item := range body.Items {
			ids = append(ids, item.UserID)
		}
		return ids
	}

	// A searcher who stays signed in throughout.
	searcherName := cns.username("s")
	cns.signup(searcherName, pw)
	searcher := signIn(searcherName)

	t.Run("first request caches the user", func(t *testing.T) {
		name := cns.username("a")
		id := cns.signup(name, pw)
		signIn(name)
		eventually(t, 5*time.Second, func() bool { u := cached(id); return u != nil && u.Username == name })
		if ids := search(searcher, name); len(ids) != 1 || ids[0] != id {
			t.Fatalf("search %q = %v, want [%d]", name, ids, id)
		}
	})

	t.Run("rename and deletion arrive by webhook and free the name", func(t *testing.T) {
		name := cns.username("b")
		id := cns.signup(name, pw)
		signIn(name)
		eventually(t, 5*time.Second, func() bool { return cached(id) != nil })

		renamed := cns.username("b")
		cnsToken := cns.firstPartyLogin(name, pw)
		cnsToken = cns.rename(cnsToken, pw, renamed)
		eventually(t, 10*time.Second, func() bool { return cached(id).Username == renamed })
		if ids := search(searcher, name); len(ids) != 0 {
			t.Fatalf("old name still found: %v", ids)
		}
		if ids := search(searcher, renamed); len(ids) != 1 || ids[0] != id {
			t.Fatalf("new name not found: %v", ids)
		}

		cns.deleteAccount(cnsToken, pw)
		eventually(t, 10*time.Second, func() bool { return cached(id).Status == models.UserStatusDeleted })
		if u := cached(id); u.Username != "" || u.AvatarURL.Valid {
			t.Fatalf("deleted profile not erased: %+v", u)
		}
		if ids := search(searcher, renamed); len(ids) != 0 {
			t.Fatalf("deleted user still found: %v", ids)
		}
		w := sendlyRequest(router, searcher, "/api/users/"+strconv.FormatInt(id, 10)+"/identity-key")
		if w.Code != http.StatusNotFound || !bytes.Contains(w.Body.Bytes(), []byte("USER_NOT_FOUND")) {
			t.Fatalf("sharing to a deleted user should be refused: %d %s", w.Code, w.Body)
		}

		// CNS frees the name; a new account takes it and is the only match.
		newID := cns.signup(renamed, pw)
		signIn(renamed)
		eventually(t, 5*time.Second, func() bool { return cached(newID) != nil })
		if ids := search(searcher, renamed); len(ids) != 1 || ids[0] != newID {
			t.Fatalf("search %q = %v, want only the new account %d", renamed, ids, newID)
		}
	})

	t.Run("signed-out users are caught by the directory sweep", func(t *testing.T) {
		name := cns.username("c")
		id := cns.signup(name, pw)
		tokens := signIn(name)
		eventually(t, 5*time.Second, func() bool { return cached(id) != nil })
		cns.logoutSendly(tokens)

		// CNS only sends user.updated to apps the user is still signed in to.
		renamed := cns.username("c")
		cns.rename(cns.firstPartyLogin(name, pw), pw, renamed)
		time.Sleep(3 * time.Second)
		if u := cached(id); u.Username != name {
			t.Fatalf("expected no webhook after sign-out, but cache has %q", u.Username)
		}
		result := sweep()
		if result.Err != nil {
			t.Fatalf("directory pass: %+v", result)
		}
		if u := cached(id); u.Username != renamed || u.Status != models.UserStatusActive {
			t.Fatalf("directory pass didn't pick up the rename: %+v", u)
		}
	})

	t.Run("deletion still arrives after sign-out", func(t *testing.T) {
		name := cns.username("d")
		id := cns.signup(name, pw)
		tokens := signIn(name)
		eventually(t, 5*time.Second, func() bool { return cached(id) != nil })
		cns.logoutSendly(tokens)
		cns.deleteAccount(cns.firstPartyLogin(name, pw), pw)
		eventually(t, 10*time.Second, func() bool { return cached(id).Status == models.UserStatusDeleted })
	})

	t.Run("directory sweep records deletions no webhook reported", func(t *testing.T) {
		// Known to Sendly, but never signed in through Sendly's OAuth client,
		// so CNS has no reason to tell Sendly about it.
		name := cns.username("e")
		id := cns.signup(name, pw)
		if err := db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: id, Username: name, ObservedAt: time.Now().Add(-48 * time.Hour)}, nil); err != nil {
			t.Fatal(err)
		}
		cns.deleteAccount(cns.firstPartyLogin(name, pw), pw)
		time.Sleep(2 * time.Second)
		if u := cached(id); u.Status != models.UserStatusActive {
			t.Fatalf("unexpected change before the sweep: %+v", u)
		}
		result := sweep()
		if result.Err != nil || result.Deleted < 1 {
			t.Fatalf("directory pass: %+v", result)
		}
		if u := cached(id); u.Status != models.UserStatusDeleted || u.Username != "" {
			t.Fatalf("directory pass didn't record the deletion: %+v", u)
		}
	})

	t.Run("a key without users:read is refused by CNS", func(t *testing.T) {
		noScope := *cfg
		noScope.CNSAuthServiceKey = os.Getenv("CNSLOCAL_NOSCOPE_KEY")
		_, err := services.NewCNSClient(&noScope).GetPublicUsers(ctx, []int64{1})
		if !errors.Is(err, services.ErrCNSDirectoryDenied) {
			t.Fatalf("expected ErrCNSDirectoryDenied, got %v", err)
		}
	})

	t.Run("CNS considers every delivery handled", func(t *testing.T) {
		var pending, failed int
		if err := cnsDB.Get(&pending, `SELECT COUNT(*) FROM webhook_deliveries WHERE client_id = 'sendly' AND delivered_at IS NULL AND failed_at IS NULL`); err != nil {
			t.Fatal(err)
		}
		if err := cnsDB.Get(&failed, `SELECT COUNT(*) FROM webhook_deliveries WHERE client_id = 'sendly' AND (failed_at IS NOT NULL OR last_status >= 300)`); err != nil {
			t.Fatal(err)
		}
		if pending != 0 || failed != 0 {
			t.Fatalf("%d deliveries pending, %d failed or retried", pending, failed)
		}
	})
}

type cnsTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type cnsLocal struct {
	t    *testing.T
	base string
	db   *sqlx.DB
}

func (c *cnsLocal) username(prefix string) string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return "e2e" + prefix + hex.EncodeToString(b)
}

// call posts JSON to CNS from a fresh client IP each time, so CNS's per-IP
// rate limits don't trip over the test.
func (c *cnsLocal) call(path, bearer string, body any) (int, map[string]any) {
	c.t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	n, _ := rand.Int(rand.Reader, big.NewInt(250*250))
	req.Header.Set("CF-Connecting-IP", fmt.Sprintf("198.51.%d.%d", n.Int64()/250, n.Int64()%250+1))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (c *cnsLocal) signup(username, password string) int64 {
	c.t.Helper()
	status, out := c.call("/api/auth/signup/request", "", map[string]string{
		"username": username, "password": password, "email": username + "@example.com", "tos": "on",
	})
	token, _ := out["signup_token"].(string)
	if token == "" {
		c.t.Fatalf("signup request for %s: %d %v", username, status, out)
	}
	var code string
	if err := c.db.Get(&code, `SELECT value::json->>'code' FROM ephemeral_state WHERE key = $1`, "pending_signup:"+token); err != nil {
		c.t.Fatal(err)
	}
	if status, out := c.call("/api/auth/signup/verify", "", map[string]string{"token": token, "code": code}); status >= 300 {
		c.t.Fatalf("signup verify for %s: %d %v", username, status, out)
	}
	var id int64
	if err := c.db.Get(&id, `SELECT id FROM users WHERE LOWER(username) = LOWER($1) AND deleted_at IS NULL`, username); err != nil {
		c.t.Fatal(err)
	}
	return id
}

// sendlyLogin runs the authorization code flow for Sendly's OAuth client.
func (c *cnsLocal) sendlyLogin(username, password string) cnsTokens {
	c.t.Helper()
	verifierBytes := make([]byte, 32)
	_, _ = rand.Read(verifierBytes)
	verifier := hex.EncodeToString(verifierBytes)
	sum := sha256.Sum256([]byte(verifier))
	redirect := "https://send.cns.ad/auth/callback"
	status, out := c.call("/api/auth/authorize", "", map[string]string{
		"username": username, "password": password, "response_type": "code", "client_id": "sendly",
		"redirect_uri": redirect, "code_challenge": base64.RawURLEncoding.EncodeToString(sum[:]),
		"code_challenge_method": "S256", "scope": "openid profile", "state": "st",
	})
	code, _ := out["code"].(string)
	if code == "" {
		c.t.Fatalf("authorize %s: %d %v", username, status, out)
	}
	raw, _ := json.Marshal(map[string]string{"code": code, "code_verifier": verifier, "client_id": "sendly", "redirect_uri": redirect, "state": "st"})
	resp, err := http.Post(c.base+"/api/auth/token", "application/json", bytes.NewReader(raw))
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var tokens cnsTokens
	_ = json.NewDecoder(resp.Body).Decode(&tokens)
	if resp.StatusCode != http.StatusOK || tokens.AccessToken == "" || tokens.RefreshToken == "" {
		c.t.Fatalf("token exchange %s: %d", username, resp.StatusCode)
	}
	return tokens
}

func (c *cnsLocal) firstPartyLogin(username, password string) string {
	c.t.Helper()
	status, out := c.call("/api/auth/login", "", map[string]string{"username": username, "password": password})
	token, _ := out["access_token"].(string)
	if token == "" {
		c.t.Fatalf("login %s: %d %v", username, status, out)
	}
	return token
}

func (c *cnsLocal) rename(token, password, newUsername string) string {
	c.t.Helper()
	status, out := c.call("/api/me/username", token, map[string]string{"password": password, "new_username": newUsername})
	tokens, _ := out["tokens"].(map[string]any)
	next, _ := tokens["access_token"].(string)
	if status != http.StatusOK || next == "" {
		c.t.Fatalf("rename to %s: %d %v", newUsername, status, out)
	}
	return next
}

func (c *cnsLocal) deleteAccount(token, password string) {
	c.t.Helper()
	if status, out := c.call("/api/me/delete", token, map[string]string{"password": password}); status != http.StatusOK {
		c.t.Fatalf("delete account: %d %v", status, out)
	}
}

func (c *cnsLocal) logoutSendly(tokens cnsTokens) {
	c.t.Helper()
	if status, out := c.call("/api/auth/logout?client_id=sendly", tokens.AccessToken, map[string]string{}); status != http.StatusNoContent {
		c.t.Fatalf("Sendly logout: %d %v", status, out)
	}
}

// sendlyRequest calls Sendly's router with a session's cookies.
func sendlyRequest(router http.Handler, tokens cnsTokens, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: tokens.AccessToken})
	req.AddCookie(&http.Cookie{Name: "auth_expires_at", Value: strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)})
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: tokens.RefreshToken})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func eventually(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if ok() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
