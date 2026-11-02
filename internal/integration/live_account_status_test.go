package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"sendly/internal/accountstatus"
	"sendly/internal/config"
	"sendly/internal/handlers"
	"sendly/internal/middleware"
	"sendly/internal/models"
	"sendly/internal/realtime"
	"sendly/internal/storage"

	"github.com/gin-gonic/gin"
)

func requestWithStatus(router http.Handler, userID int64, status accountstatus.Status, method, path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("X-Test-User", strconv.FormatInt(userID, 10))
	req.Header.Set("X-Test-Status", string(status))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestLiveTransfersRespectAccountStatus(t *testing.T) {
	if os.Getenv("SENDLY_LIVE_INTEGRATION") != "1" {
		t.Skip("set SENDLY_LIVE_INTEGRATION=1 to run against live Postgres")
	}
	root := t.TempDir()
	t.Setenv("DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("CHUNK_DIR", filepath.Join(root, "chunks"))
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	db, err := storage.NewPostgres(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.RunMigrations(ctx, cfg.MigrationsDir); err != nil {
		t.Fatal(err)
	}

	ownerID := time.Now().UnixNano()%1000000000 + 5000000000
	recipientID := ownerID + 1
	recipientName := fmt.Sprintf("held-recipient-%d", recipientID)
	_, _ = liveSharingIdentity(t, db, ownerID, fmt.Sprintf("00000000-0000-4000-8000-%012d", ownerID%1000000000000))
	_, _ = liveSharingIdentity(t, db, recipientID, fmt.Sprintf("00000000-0000-4000-8000-%012d", recipientID%1000000000000))
	if err := db.RecordCNSProfile(ctx, models.CNSUserProfile{CNSUserID: recipientID, Username: recipientName, ObservedAt: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordAccountStatus(ctx, recipientID, models.CNSAccountStatus{Status: accountstatus.Limited, ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	fileID := models.GenerateID(20)
	now := time.Now()
	if err := db.CreateFileWithEnvelope(ctx, &models.File{
		ID: fileID, NumericCode: models.GenerateNumericCode(), OriginalName: "held.txt",
		SizeBytes: 64, UploaderIP: "127.0.0.1",
		OwnerCNSUserID: sqlNullInt(ownerID), OwnerCNSUserName: sqlNullString("owner"),
		ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now,
	}, nil, nil); err != nil {
		t.Fatal(err)
	}

	recent := handlers.NewRecentUploadsHandler(cfg, db, realtime.NewHub(nil))
	router := gin.New()
	router.Use(func(c *gin.Context) {
		id, err := strconv.Atoi(c.GetHeader("X-Test-User"))
		if err == nil && id > 0 {
			c.Set(middleware.CNSUserKey, &middleware.CNSUser{ID: id, Username: "u" + strconv.Itoa(id), AccountStatus: &accountstatus.Info{Status: c.GetHeader("X-Test-Status")}})
		}
		c.Next()
	})
	router.GET("/api/users/lookup", recent.LookupUsers)
	router.GET("/api/users/:id/identity-key", recent.GetUserIdentityKey)
	router.POST("/api/file/:id/share-to-user", recent.ShareFileToUser)
	router.GET("/api/me/transfers", recent.ListTransfers)
	router.GET("/api/me/transfers/pending-count", recent.PendingTransferCount)
	router.POST("/api/me/transfers/:file_id/accept", recent.AcceptTransfer)

	lookup := requestWithStatus(router, ownerID, accountstatus.Active, http.MethodGet, "/api/users/lookup?q="+recipientName, nil)
	if lookup.Code != http.StatusOK || bytes.Contains(lookup.Body.Bytes(), []byte(recipientName)) {
		t.Fatalf("restricted recipients must be hidden from lookup: status=%d body=%s", lookup.Code, lookup.Body.String())
	}

	keyResp := requestWithStatus(router, ownerID, accountstatus.Active, http.MethodGet, fmt.Sprintf("/api/users/%d/identity-key", recipientID), nil)
	if keyResp.Code != http.StatusOK {
		t.Fatalf("identity key status=%d body=%s", keyResp.Code, keyResp.Body.String())
	}
	var recipientPublic struct {
		PublicKeyJWK json.RawMessage `json:"public_key_jwk"`
		KeyVersion   int             `json:"key_version"`
	}
	if err := json.Unmarshal(keyResp.Body.Bytes(), &recipientPublic); err != nil {
		t.Fatal(err)
	}
	publicKey, err := parseRSAJWK(recipientPublic.PublicKeyJWK)
	if err != nil {
		t.Fatal(err)
	}
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}
	wrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, publicKey, dek, nil)
	if err != nil {
		t.Fatal(err)
	}
	shareBody, _ := json.Marshal(models.ShareFileRequest{
		RecipientUserID: recipientID, WrappedDEK: base64.StdEncoding.EncodeToString(wrapped),
		DEKWrapAlg: "RSA-OAEP-2048-v1", RecipientKeyVersion: recipientPublic.KeyVersion,
	})

	if denied := requestWithStatus(router, ownerID, accountstatus.Limited, http.MethodPost, "/api/file/"+fileID+"/share-to-user", shareBody); denied.Code != http.StatusForbidden || !bytes.Contains(denied.Body.Bytes(), []byte("ACCOUNT_RESTRICTED")) {
		t.Fatalf("a limited sender must not send: status=%d body=%s", denied.Code, denied.Body.String())
	}
	if shared := requestWithStatus(router, ownerID, accountstatus.Active, http.MethodPost, "/api/file/"+fileID+"/share-to-user", shareBody); shared.Code != http.StatusOK {
		t.Fatalf("sending to a restricted recipient looks normal to the sender: status=%d body=%s", shared.Code, shared.Body.String())
	}

	pending := requestWithStatus(router, recipientID, accountstatus.Limited, http.MethodGet, "/api/me/transfers?view=pending", nil)
	if pending.Code != http.StatusOK || bytes.Contains(pending.Body.Bytes(), []byte(fileID)) {
		t.Fatalf("held transfers must not be listed: status=%d body=%s", pending.Code, pending.Body.String())
	}
	if count := requestWithStatus(router, recipientID, accountstatus.Limited, http.MethodGet, "/api/me/transfers/pending-count", nil); !bytes.Contains(count.Body.Bytes(), []byte(`"count":0`)) {
		t.Fatalf("held transfers must not raise the badge: body=%s", count.Body.String())
	}
	if accept := requestWithStatus(router, recipientID, accountstatus.Limited, http.MethodPost, "/api/me/transfers/"+fileID+"/accept", nil); accept.Code != http.StatusForbidden {
		t.Fatalf("a limited recipient must not accept: status=%d body=%s", accept.Code, accept.Body.String())
	}

	lifted := requestWithStatus(router, recipientID, accountstatus.Active, http.MethodGet, "/api/me/transfers?view=pending", nil)
	if !bytes.Contains(lifted.Body.Bytes(), []byte(fileID)) {
		t.Fatalf("the held transfer must come back once lifted: body=%s", lifted.Body.String())
	}
	if accept := requestWithStatus(router, recipientID, accountstatus.Active, http.MethodPost, "/api/me/transfers/"+fileID+"/accept", nil); accept.Code != http.StatusOK {
		t.Fatalf("accept after lifting: status=%d body=%s", accept.Code, accept.Body.String())
	}
}
