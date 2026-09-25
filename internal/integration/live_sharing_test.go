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
	"net/http/httptest"
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
	rdb, err := storage.NewRedis(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	if err := db.RunMigrations(context.Background(), cfg.MigrationsDir); err != nil {
		t.Fatal(err)
	}
	recent := handlers.NewRecentUploadsHandler(cfg, db)
	cfg.RateLimitMaxPerMinute = 2
	lookupLimiter := middleware.NewRateLimiter(rdb, cfg.RateLimitMaxPerMinute, time.Minute)
	ownerID := time.Now().UnixNano() % 1000000000
	recipientID := ownerID + 1
	noKeyID := ownerID + 2
	declinerID := ownerID + 3
	_, _ = liveSharingIdentity(t, db, ownerID, fmt.Sprintf("00000000-0000-4000-8000-%012d", ownerID%1000000000000))
	recipientKey, recipientDevice := liveSharingIdentity(t, db, recipientID, fmt.Sprintf("00000000-0000-4000-8000-%012d", recipientID%1000000000000))
	_, declinerDevice := liveSharingIdentity(t, db, declinerID, fmt.Sprintf("00000000-0000-4000-8000-%012d", declinerID%1000000000000))
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
	router.GET("/api/users/lookup", lookupLimiter.Handler(), recent.LookupUsers)
	router.POST("/api/file/:id/share-to-user", recent.ShareFileToUser)
	router.GET("/api/me/shared-with-me", recent.SharedWithMe)
	router.GET("/api/me/recent-share-recipients", recent.RecentShareRecipients)
	router.GET("/api/me/files/:id/access", recent.FileAccess)
	router.GET("/api/me/transfers", recent.ListTransfers)
	router.GET("/api/me/transfers/pending-count", recent.PendingTransferCount)
	router.POST("/api/me/transfers/:file_id/accept", recent.AcceptTransfer)
	router.POST("/api/me/transfers/:file_id/decline", recent.DeclineTransfer)

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("x-service-key") != "live-service-key" {
			http.Error(w, "missing service key", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("q") == "missing-user" {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		_, _ = w.Write([]byte(fmt.Sprintf(`{"items":[{"id":%d,"username":"recipient-user","avatar":""}]}`, recipientID)))
	}))
	defer authServer.Close()
	cfg.CNSAuthURL = authServer.URL
	cfg.CNSAuthServiceKey = "live-service-key"

	// Lookup searches Sendly's local user cache, not CNS.
	if err := db.UpsertUser(context.Background(), &models.User{CNSUserID: recipientID, Username: "recipient-user", Status: models.UserStatusActive}); err != nil {
		t.Fatal(err)
	}
	lookupRequest := httptest.NewRequest(http.MethodGet, "/api/users/lookup?q=recipient", nil)
	lookupRequest.Header.Set("X-Test-User", fmt.Sprint(ownerID))
	lookupRequest.AddCookie(&http.Cookie{Name: "auth_token", Value: "live-token"})
	lookupResponse := httptest.NewRecorder()
	router.ServeHTTP(lookupResponse, lookupRequest)
	if lookupResponse.Code != http.StatusOK || !bytes.Contains(lookupResponse.Body.Bytes(), []byte("recipient-user")) {
		t.Fatalf("user lookup match status=%d body=%s", lookupResponse.Code, lookupResponse.Body.String())
	}
	noMatchRequest := httptest.NewRequest(http.MethodGet, "/api/users/lookup?q=missing-user", nil)
	noMatchRequest.Header.Set("X-Test-User", fmt.Sprint(ownerID))
	noMatchRequest.AddCookie(&http.Cookie{Name: "auth_token", Value: "live-token"})
	noMatchResponse := httptest.NewRecorder()
	router.ServeHTTP(noMatchResponse, noMatchRequest)
	if noMatchResponse.Code != http.StatusOK || !bytes.Contains(noMatchResponse.Body.Bytes(), []byte(`"items":[]`)) {
		t.Fatalf("user lookup no-match status=%d body=%s", noMatchResponse.Code, noMatchResponse.Body.String())
	}
	for i := 0; i < 2; i++ {
		rateRequest := httptest.NewRequest(http.MethodGet, "/api/users/lookup?q=recipient", nil)
		rateRequest.Header.Set("X-Test-User", fmt.Sprint(ownerID))
		rateRequest.AddCookie(&http.Cookie{Name: "auth_token", Value: "live-token"})
		rateResponse := httptest.NewRecorder()
		router.ServeHTTP(rateResponse, rateRequest)
		if rateResponse.Code != http.StatusTooManyRequests {
			t.Fatalf("lookup rate limit attempt %d status=%d body=%s", i+1, rateResponse.Code, rateResponse.Body.String())
		}
	}

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
	recentRecipients := requestAs(router, int(ownerID), http.MethodGet, "/api/me/recent-share-recipients", nil, "")
	if recentRecipients.Code != http.StatusOK || !bytes.Contains(recentRecipients.Body.Bytes(), []byte(fmt.Sprintf(`"user_id":%d`, recipientID))) {
		t.Fatalf("recent recipients status=%d body=%s", recentRecipients.Code, recentRecipients.Body.String())
	}

	// A new transfer is pending: listed for the recipient, but its key is withheld.
	pending := requestAs(router, int(recipientID), http.MethodGet, "/api/me/transfers?view=pending", nil, "")
	if pending.Code != http.StatusOK || !bytes.Contains(pending.Body.Bytes(), []byte(fileID)) || !bytes.Contains(pending.Body.Bytes(), []byte(`"status":"pending"`)) {
		t.Fatalf("pending transfers status=%d body=%s", pending.Code, pending.Body.String())
	}
	if count := requestAs(router, int(recipientID), http.MethodGet, "/api/me/transfers/pending-count", nil, ""); !bytes.Contains(count.Body.Bytes(), []byte(`"count":1`)) {
		t.Fatalf("pending count before accept body=%s", count.Body.String())
	}
	if listed := requestAs(router, int(recipientID), http.MethodGet, "/api/me/shared-with-me", nil, ""); bytes.Contains(listed.Body.Bytes(), []byte(fileID)) {
		t.Fatalf("pending transfer appeared in shared-with-me: body=%s", listed.Body.String())
	}
	if early := requestAs(router, int(recipientID), http.MethodGet, "/api/me/files/"+fileID+"/access?device_id="+recipientDevice, nil, ""); early.Code == http.StatusOK {
		t.Fatalf("pending transfer handed out its key: body=%s", early.Body.String())
	}
	if dup := requestAs(router, int(ownerID), http.MethodPost, "/api/file/"+fileID+"/share-to-user", shareBody, "application/json"); dup.Code != http.StatusConflict || !bytes.Contains(dup.Body.Bytes(), []byte("TRANSFER_EXISTS")) {
		t.Fatalf("duplicate transfer status=%d body=%s", dup.Code, dup.Body.String())
	}
	if other := requestAs(router, int(ownerID), http.MethodPost, "/api/me/transfers/"+fileID+"/accept", nil, ""); other.Code != http.StatusNotFound {
		t.Fatalf("sender accepted a transfer not addressed to them: status=%d body=%s", other.Code, other.Body.String())
	}

	accepted := requestAs(router, int(recipientID), http.MethodPost, "/api/me/transfers/"+fileID+"/accept", nil, "")
	if accepted.Code != http.StatusOK {
		t.Fatalf("accept status=%d body=%s", accepted.Code, accepted.Body.String())
	}
	if again := requestAs(router, int(recipientID), http.MethodPost, "/api/me/transfers/"+fileID+"/decline", nil, ""); again.Code != http.StatusConflict {
		t.Fatalf("answering twice status=%d body=%s", again.Code, again.Body.String())
	}
	if count := requestAs(router, int(recipientID), http.MethodGet, "/api/me/transfers/pending-count", nil, ""); !bytes.Contains(count.Body.Bytes(), []byte(`"count":0`)) {
		t.Fatalf("pending count after accept body=%s", count.Body.String())
	}
	history := requestAs(router, int(recipientID), http.MethodGet, "/api/me/transfers?view=history", nil, "")
	if !bytes.Contains(history.Body.Bytes(), []byte(`"status":"accepted"`)) || !bytes.Contains(history.Body.Bytes(), []byte(fmt.Sprintf(`"sender_user_id":%d`, ownerID))) {
		t.Fatalf("transfer history body=%s", history.Body.String())
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

	// Declining revokes the key: no access, and the same file can't be re-sent to that user.
	declinerLookup := requestAs(router, int(ownerID), http.MethodGet, fmt.Sprintf("/api/users/%d/identity-key", declinerID), nil, "")
	var declinerPublic struct {
		PublicKeyJWK json.RawMessage `json:"public_key_jwk"`
		KeyVersion   int             `json:"key_version"`
	}
	if err := json.Unmarshal(declinerLookup.Body.Bytes(), &declinerPublic); err != nil {
		t.Fatal(err)
	}
	declinerPublicKey, err := parseRSAJWK(declinerPublic.PublicKeyJWK)
	if err != nil {
		t.Fatal(err)
	}
	declinerWrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, declinerPublicKey, dek, nil)
	if err != nil {
		t.Fatal(err)
	}
	declinerBody, _ := json.Marshal(models.ShareFileRequest{
		RecipientUserID: declinerID, WrappedDEK: base64.StdEncoding.EncodeToString(declinerWrapped),
		DEKWrapAlg: "RSA-OAEP-2048-v1", RecipientKeyVersion: declinerPublic.KeyVersion,
	})
	if sent := requestAs(router, int(ownerID), http.MethodPost, "/api/file/"+fileID+"/share-to-user", declinerBody, "application/json"); sent.Code != http.StatusOK {
		t.Fatalf("second recipient share status=%d body=%s", sent.Code, sent.Body.String())
	}
	if declined := requestAs(router, int(declinerID), http.MethodPost, "/api/me/transfers/"+fileID+"/decline", nil, ""); declined.Code != http.StatusOK {
		t.Fatalf("decline status=%d body=%s", declined.Code, declined.Body.String())
	}
	if revoked := requestAs(router, int(declinerID), http.MethodGet, "/api/me/files/"+fileID+"/access?device_id="+declinerDevice, nil, ""); revoked.Code == http.StatusOK {
		t.Fatalf("declined transfer still handed out a key: body=%s", revoked.Body.String())
	}
	if _, err := db.GetFileAccessKeyEnvelope(context.Background(), fileID, declinerID); err != models.ErrFileAccessNotFound {
		t.Fatalf("declined recipient envelope still present: err=%v", err)
	}
	if resend := requestAs(router, int(ownerID), http.MethodPost, "/api/file/"+fileID+"/share-to-user", declinerBody, "application/json"); resend.Code != http.StatusConflict {
		t.Fatalf("re-send after decline status=%d body=%s", resend.Code, resend.Body.String())
	}
	if declinedHistory := requestAs(router, int(declinerID), http.MethodGet, "/api/me/transfers?view=history", nil, ""); !bytes.Contains(declinedHistory.Body.Bytes(), []byte(`"status":"declined"`)) {
		t.Fatalf("declined history body=%s", declinedHistory.Body.String())
	}
	// The sender tracks both outgoing transfers and how each was answered.
	var sentHistory models.TransfersResponse
	sentRes := requestAs(router, int(ownerID), http.MethodGet, "/api/me/transfers?view=history&direction=sent", nil, "")
	if err := json.Unmarshal(sentRes.Body.Bytes(), &sentHistory); err != nil || sentRes.Code != http.StatusOK {
		t.Fatalf("sent history status=%d body=%s", sentRes.Code, sentRes.Body.String())
	}
	sentStatus := map[int64]string{}
	for _, item := range sentHistory.Items {
		if item.FileID == fileID && item.Direction == models.TransferDirectionSent {
			sentStatus[item.RecipientUserID] = item.Status
		}
	}
	if sentStatus[recipientID] != models.TransferStatusAccepted || sentStatus[declinerID] != models.TransferStatusDeclined {
		t.Fatalf("sent history statuses=%v body=%s", sentStatus, sentRes.Body.String())
	}
	if all := requestAs(router, int(ownerID), http.MethodGet, "/api/me/transfers?view=history", nil, ""); !bytes.Contains(all.Body.Bytes(), []byte(`"direction":"sent"`)) {
		t.Fatalf("combined history omitted sent transfers: body=%s", all.Body.String())
	}
	if received := requestAs(router, int(ownerID), http.MethodGet, "/api/me/transfers?view=history&direction=received", nil, ""); bytes.Contains(received.Body.Bytes(), []byte(fileID)) {
		t.Fatalf("sent transfer listed as received: body=%s", received.Body.String())
	}
	if bad := requestAs(router, int(ownerID), http.MethodGet, "/api/me/transfers?view=history&direction=sideways", nil, ""); bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid direction status=%d", bad.Code)
	}
	// The first recipient's accepted access is unaffected by someone else declining.
	if still := requestAs(router, int(recipientID), http.MethodGet, "/api/me/files/"+fileID+"/access?device_id="+recipientDevice, nil, ""); still.Code != http.StatusOK {
		t.Fatalf("accepted recipient lost access: status=%d body=%s", still.Code, still.Body.String())
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
