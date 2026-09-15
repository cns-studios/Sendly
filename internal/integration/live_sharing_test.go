package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sendly/internal/config"
	"sendly/internal/handlers"
	"sendly/internal/middleware"
	"sendly/internal/models"
	"sendly/internal/storage"

	"github.com/gin-gonic/gin"
)

func TestLiveUserSharingEndToEnd(t *testing.T) {
	if os.Getenv("SENDLY_LIVE_INTEGRATION") != "1" {
		t.Skip("set SENDLY_LIVE_INTEGRATION=1 to run against live Postgres and Redis")
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
	if err := db.RunMigrations(context.Background(), cfg.MigrationsDir); err != nil {
		t.Fatal(err)
	}
	recent := handlers.NewRecentUploadsHandler(cfg, db)
	ownerID := time.Now().UnixNano() % 1000000000
	recipientID := ownerID + 1
	noKeyID := ownerID + 2
	_, _ = liveSharingIdentity(t, db, ownerID, fmt.Sprintf("00000000-0000-4000-8000-%012d", ownerID%1000000000000))
	recipientKey, recipientDevice := liveSharingIdentity(t, db, recipientID, fmt.Sprintf("00000000-0000-4000-8000-%012d", recipientID%1000000000000))
	fileID := models.GenerateID(20)
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("private shared content")
	ciphertext, nonce, err := encryptTestDEK(dek, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	fs, err := storage.NewFilesystem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fs.GetFilePath(fileID), append(nonce, ciphertext...), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.CreateFileWithEnvelope(context.Background(), &models.File{
		ID: fileID, NumericCode: models.GenerateNumericCode(), OriginalName: "shared.txt",
		SizeBytes: int64(len(append(nonce, ciphertext...))), UploaderIP: "127.0.0.1",
		OwnerCNSUserID: sqlNullInt(ownerID), OwnerCNSUserName: sqlNullString("owner"),
		ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now,
	}, &models.FileKeyEnvelope{
		FileID: fileID, WrappedDEK: []byte("legacy"), DEKWrapAlg: "AES-GCM-UK-v1", DEKWrapVersion: 1,
	}, nil, nil); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		if raw := c.GetHeader("X-Test-User"); raw != "" {
			var id int64
			if _, err := fmt.Sscan(raw, &id); err == nil && id > 0 {
				c.Set(middleware.CNSUserKey, &middleware.CNSUser{ID: int(id), Username: raw})
			}
		}
		c.Next()
	})
	router.GET("/api/users/:id/identity-key", recent.GetUserIdentityKey)
	router.POST("/api/file/:id/share-to-user", recent.ShareFileToUser)
	router.GET("/api/me/shared-with-me", recent.SharedWithMe)
	router.GET("/api/me/files/:id/access", recent.FileAccess)

	lookup := requestAs(router, int(ownerID), http.MethodGet, fmt.Sprintf("/api/users/%d/identity-key", recipientID), nil, "")
	if lookup.Code != http.StatusOK {
		t.Fatalf("recipient key lookup status=%d body=%s", lookup.Code, lookup.Body.String())
	}
	var recipientPublic struct {
		PublicKeyJWK json.RawMessage `json:"public_key_jwk"`
		KeyVersion   int             `json:"key_version"`
	}
	if err := json.Unmarshal(lookup.Body.Bytes(), &recipientPublic); err != nil {
		t.Fatal(err)
	}
	recipientPublicKey, err := parseRSAJWK(recipientPublic.PublicKeyJWK)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, recipientPublicKey, dek, nil)
	if err != nil {
		t.Fatal(err)
	}
	shareBody, _ := json.Marshal(models.ShareFileRequest{
		RecipientUserID: recipientID, WrappedDEK: base64.StdEncoding.EncodeToString(wrapped),
		DEKWrapAlg: "RSA-OAEP-2048-v1", RecipientKeyVersion: recipientPublic.KeyVersion,
	})
	shared := requestAs(router, int(ownerID), http.MethodPost, "/api/file/"+fileID+"/share-to-user", shareBody, "application/json")
	if shared.Code != http.StatusOK {
		t.Fatalf("share status=%d body=%s", shared.Code, shared.Body.String())
	}

	listed := requestAs(router, int(recipientID), http.MethodGet, "/api/me/shared-with-me", nil, "")
	if listed.Code != http.StatusOK || !bytes.Contains(listed.Body.Bytes(), []byte(fileID)) {
		t.Fatalf("shared list status=%d body=%s", listed.Code, listed.Body.String())
	}
	ownerList := requestAs(router, int(ownerID), http.MethodGet, "/api/me/shared-with-me", nil, "")
	if ownerList.Code != http.StatusOK || bytes.Contains(ownerList.Body.Bytes(), []byte(fileID)) {
		t.Fatalf("owner appeared in shared-with-me: status=%d body=%s", ownerList.Code, ownerList.Body.String())
	}
	access := requestAs(router, int(recipientID), http.MethodGet, "/api/me/files/"+fileID+"/access?device_id="+recipientDevice, nil, "")
	if access.Code != http.StatusOK {
		t.Fatalf("shared access status=%d body=%s", access.Code, access.Body.String())
	}
	var accessPayload models.FileAccessResponse
	if err := json.Unmarshal(access.Body.Bytes(), &accessPayload); err != nil {
		t.Fatal(err)
	}
	if accessPayload.IdentityFileAccessEnvelope == nil {
		t.Fatal("shared access response omitted identity envelope")
	}
	recoveredDEK, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, recipientKey, mustBase64(accessPayload.IdentityFileAccessEnvelope.WrappedDEKB64), nil)
	if err != nil {
		t.Fatal(err)
	}
	stored := mustReadFile(t, fs.GetFilePath(fileID))
	decrypted, err := decryptTestDEK(recoveredDEK, stored[12:], stored[:12])
	if err != nil || string(decrypted) != string(plaintext) {
		t.Fatalf("shared file decryption failed: err=%v plaintext=%q", err, decrypted)
	}

	noKeyLookup := requestAs(router, int(ownerID), http.MethodGet, fmt.Sprintf("/api/users/%d/identity-key", noKeyID), nil, "")
	if noKeyLookup.Code != http.StatusNotFound || !bytes.Contains(noKeyLookup.Body.Bytes(), []byte("RECIPIENT_NOT_READY")) {
		t.Fatalf("no-key lookup status=%d body=%s", noKeyLookup.Code, noKeyLookup.Body.String())
	}
	noKeyBody, _ := json.Marshal(models.ShareFileRequest{
		RecipientUserID: noKeyID, WrappedDEK: base64.StdEncoding.EncodeToString(wrapped),
		DEKWrapAlg: "RSA-OAEP-2048-v1", RecipientKeyVersion: 1,
	})
	noKeyShare := requestAs(router, int(ownerID), http.MethodPost, "/api/file/"+fileID+"/share-to-user", noKeyBody, "application/json")
	if noKeyShare.Code != http.StatusNotFound || !bytes.Contains(noKeyShare.Body.Bytes(), []byte("RECIPIENT_NOT_READY")) {
		t.Fatalf("no-key share status=%d body=%s", noKeyShare.Code, noKeyShare.Body.String())
	}
	notOwnerBody, _ := json.Marshal(models.ShareFileRequest{
		RecipientUserID: ownerID, WrappedDEK: base64.StdEncoding.EncodeToString(wrapped),
		DEKWrapAlg: "RSA-OAEP-2048-v1", RecipientKeyVersion: 1,
	})
	notOwner := requestAs(router, int(recipientID), http.MethodPost, "/api/file/"+fileID+"/share-to-user", notOwnerBody, "application/json")
	if notOwner.Code != http.StatusForbidden {
		t.Fatalf("non-owner share status=%d body=%s", notOwner.Code, notOwner.Body.String())
	}
	staleBody, _ := json.Marshal(models.ShareFileRequest{
		RecipientUserID: recipientID, WrappedDEK: base64.StdEncoding.EncodeToString(wrapped),
		DEKWrapAlg: "RSA-OAEP-2048-v1", RecipientKeyVersion: recipientPublic.KeyVersion + 1,
	})
	stale := requestAs(router, int(ownerID), http.MethodPost, "/api/file/"+fileID+"/share-to-user", staleBody, "application/json")
	if stale.Code != http.StatusConflict || !bytes.Contains(stale.Body.Bytes(), []byte("RECIPIENT_KEY_VERSION_MISMATCH")) {
		t.Fatalf("stale-key share status=%d body=%s", stale.Code, stale.Body.String())
	}
}

func liveSharingIdentity(t *testing.T, db *storage.Postgres, userID int64, deviceID string) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUserIdentityKey(context.Background(), &models.UserIdentityKey{
		CNSUserID: userID, KeyVersion: 1, PublicKeyJWK: rsaPublicJWK(&key.PublicKey),
		KeyAlgorithm: "RSA-OAEP-2048", Status: "active", CreatedAt: time.Now(),
		ActivatedAt: sqlNullTime(),
	}); err != nil {
		t.Fatal(err)
	}
	deviceKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateOrUpdateUserDevice(context.Background(), &models.UserDevice{
		ID: deviceID, CNSUserID: userID, DeviceLabel: "sharing device",
		PublicKeyJWK: rsaPublicJWK(&deviceKey.PublicKey), KeyAlgorithm: "RSA-OAEP-2048", KeyVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := hybridWrap(deviceKey.PublicKey, privateDER)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUserIdentityKeyDeviceEnvelope(context.Background(), &models.UserIdentityKeyDeviceEnvelope{
		CNSUserID: userID, DeviceID: deviceID, IdentityKeyVersion: 1,
		WrappedPrivateKey: wrapped, WrapAlg: "RSA-OAEP-2048+AES-GCM-256-v1",
		WrapMeta: json.RawMessage(`{}`), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	recoveredDER, err := hybridUnwrap(deviceKey, wrapped)
	if err != nil {
		t.Fatal(err)
	}
	recoveredKey, err := x509.ParsePKCS8PrivateKey(recoveredDER)
	if err != nil {
		t.Fatal(err)
	}
	return recoveredKey.(*rsa.PrivateKey), deviceID
}

func sqlNullInt(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: true}
}

func sqlNullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: true}
}

func sqlNullTime() sql.NullTime {
	return sql.NullTime{Time: time.Now(), Valid: true}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func parseRSAJWK(raw json.RawMessage) (*rsa.PublicKey, error) {
	var value struct {
		N string `json:"n"`
		E string `json:"e"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(value.N)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(value.E)
	if err != nil {
		return nil, err
	}
	exponent := 0
	for _, b := range eBytes {
		exponent = exponent<<8 | int(b)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: exponent}, nil
}
