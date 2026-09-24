package integration

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
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
	"sendly/internal/services"
	"sendly/internal/storage"

	"github.com/gin-gonic/gin"
)

func TestLiveAuthenticatedOwnerUploadAccessAndRecentListing(t *testing.T) {
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
	fs, err := storage.NewFilesystem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	tracker := services.NewTracker(db)
	upload := services.NewUpload(cfg, db, rdb, fs, tracker)
	uploadHandler := handlers.NewUploadHandler(cfg, db, rdb, fs, upload)
	recentHandler := handlers.NewRecentUploadsHandler(cfg, db)
	androidHandler := handlers.NewAndroidHandler(cfg, db, fs, upload, tracker)

	userID := time.Now().UnixNano()
	user := &middleware.CNSUser{ID: int(userID), Username: "live-owner"}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(middleware.CNSUserKey, user)
		c.Next()
	})
	router.POST("/api/upload/init", uploadHandler.Init)
	router.POST("/api/upload/chunk", uploadHandler.Chunk)
	router.POST("/api/upload/complete", uploadHandler.Complete)
	router.GET("/api/upload/status/:session_id", uploadHandler.AssemblyStatus)
	router.POST("/api/upload/finalize", uploadHandler.Finalize)
	router.GET("/api/me/recent-uploads", recentHandler.RecentUploads)
	router.GET("/api/me/files/:id/access", recentHandler.FileAccess)
	router.POST("/api/me/devices/register", recentHandler.RegisterDevice)
	router.POST("/android/me/devices/register", androidHandler.RegisterDevice)

	identityPrivate, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	devicePrivate, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	identityVersion := 1
	if err := db.CreateUserIdentityKey(context.Background(), &models.UserIdentityKey{
		CNSUserID: userID, KeyVersion: identityVersion,
		PublicKeyJWK: rsaPublicJWK(&identityPrivate.PublicKey), KeyAlgorithm: "RSA-OAEP-2048",
		Status: "active", CreatedAt: time.Now(), ActivatedAt: sql.NullTime{Time: time.Now(), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	deviceID := fmt.Sprintf("00000000-0000-4000-8000-%012d", userID%1000000000000)
	if err := db.CreateOrUpdateUserDevice(context.Background(), &models.UserDevice{
		ID: deviceID, CNSUserID: userID, DeviceLabel: "live owner",
		PublicKeyJWK: rsaPublicJWK(&devicePrivate.PublicKey), KeyAlgorithm: "RSA-OAEP-2048", KeyVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	identityPrivateDER, err := x509.MarshalPKCS8PrivateKey(identityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	wrappedIdentityPrivate, err := hybridWrap(devicePrivate.PublicKey, identityPrivateDER)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUserIdentityKeyDeviceEnvelope(context.Background(), &models.UserIdentityKeyDeviceEnvelope{
		CNSUserID: userID, DeviceID: deviceID, IdentityKeyVersion: identityVersion,
		WrappedPrivateKey: wrappedIdentityPrivate, WrapAlg: "RSA-OAEP-2048+AES-GCM-256-v1",
		WrapMeta: json.RawMessage(`{}`), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	recoveredDER, err := hybridUnwrap(devicePrivate, wrappedIdentityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	recoveredKey, err := x509.ParsePKCS8PrivateKey(recoveredDER)
	if err != nil {
		t.Fatal(err)
	}
	recoveredIdentityPrivate := recoveredKey.(*rsa.PrivateKey)
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := encryptTestDEK(dek, []byte("identity-backed upload"))
	if err != nil {
		t.Fatal(err)
	}
	identityWrappedDEK, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &identityPrivate.PublicKey, dek, nil)
	if err != nil {
		t.Fatal(err)
	}
	initBody, _ := json.Marshal(models.UploadInitRequest{
		FileName: "live.txt", FileSize: int64(len(ciphertext)), TotalChunks: 1, ChunkSize: int64(len(ciphertext)),
	})
	rec := request(router, http.MethodPost, "/api/upload/init", initBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("init status=%d body=%s", rec.Code, rec.Body.String())
	}
	var initResp models.UploadInitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &initResp); err != nil {
		t.Fatal(err)
	}

	var chunk bytes.Buffer
	form := multipart.NewWriter(&chunk)
	_ = form.WriteField("session_id", initResp.SessionID)
	_ = form.WriteField("chunk_index", "0")
	part, _ := form.CreateFormFile("chunk", "live.txt")
	_, _ = part.Write(ciphertext)
	_ = form.Close()
	rec = request(router, http.MethodPost, "/api/upload/chunk", chunk.Bytes(), form.FormDataContentType())
	if rec.Code != http.StatusOK {
		t.Fatalf("chunk status=%d body=%s", rec.Code, rec.Body.String())
	}

	completeBody, _ := json.Marshal(models.UploadCompleteRequest{SessionID: initResp.SessionID, Confirmed: true})
	rec = request(router, http.MethodPost, "/api/upload/complete", completeBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("complete status=%d body=%s", rec.Code, rec.Body.String())
	}
	waitForAssembly(t, router, initResp.SessionID)

	finalizeBody, _ := json.Marshal(models.UploadFinalizeRequest{
		SessionID: initResp.SessionID, Duration: "90d", DeviceID: deviceID,
		WrappedDEKB64: base64.StdEncoding.EncodeToString(identityWrappedDEK),
		DEKWrapAlg:    "RSA-OAEP-2048-v1", DEKWrapVersion: 1,
		IdentityWrappedDEKB64: base64.StdEncoding.EncodeToString(identityWrappedDEK),
		IdentityDEKWrapAlg:    "RSA-OAEP-2048-v1", IdentityDEKWrapVersion: 1,
		IdentityKeyVersion: identityVersion,
	})
	rec = request(router, http.MethodPost, "/api/upload/finalize", finalizeBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("finalize status=%d body=%s", rec.Code, rec.Body.String())
	}
	var finalized models.UploadFinalizeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &finalized); err != nil {
		t.Fatal(err)
	}
	if grant, grantErr := db.GetFileAccessKeyEnvelope(context.Background(), finalized.FileID, userID); grantErr != nil {
		t.Fatalf("owner identity grant missing after finalize: %v", grantErr)
	} else if len(grant.WrappedDEK) == 0 {
		t.Fatal("owner identity grant is empty")
	}

	rec = request(router, http.MethodGet, "/api/me/recent-uploads", nil, "")
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(finalized.FileID)) {
		t.Fatalf("recent uploads status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = request(router, http.MethodGet, "/api/me/files/"+finalized.FileID+"/access?device_id="+deviceID, nil, "")
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("file_access_key_envelope")) {
		t.Fatalf("file access status=%d body=%s", rec.Code, rec.Body.String())
	}
	var accessResp models.FileAccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &accessResp); err != nil {
		t.Fatal(err)
	}
	if accessResp.IdentityFileAccessEnvelope == nil {
		t.Fatal("identity access envelope missing from response")
	}
	returnedDEK, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, recoveredIdentityPrivate, mustBase64(accessResp.IdentityFileAccessEnvelope.WrappedDEKB64), nil)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := decryptTestDEK(returnedDEK, ciphertext, nonce)
	if err != nil || string(decrypted) != "identity-backed upload" {
		t.Fatalf("identity-wrapped DEK failed to decrypt uploaded file: err=%v plaintext=%q", err, decrypted)
	}

	register := func(deviceID, wrapped string) []byte {
		body, _ := json.Marshal(models.DeviceRegisterRequest{
			DeviceID: deviceID, DeviceLabel: deviceID,
			PublicKeyJWK: json.RawMessage(`{"kty":"RSA","n":"test","e":"AQAB"}`),
			KeyAlgorithm: "RSA-OAEP-2048", KeyVersion: 1,
			WrappedUserKeyB64: base64.StdEncoding.EncodeToString([]byte(wrapped)),
			UKWrapAlg:         "RSA-OAEP-2048-v1", UKWrapMeta: json.RawMessage(`{"type":"test"}`),
		})
		return body
	}
	rec = request(router, http.MethodPost, "/api/me/devices/register", register(fmt.Sprintf("00000000-0000-4000-8000-%012d", (userID+3)%1000000000000), "owner-uk"), "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("web registration status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = request(router, http.MethodPost, "/android/me/devices/register", register(fmt.Sprintf("00000000-0000-4000-8000-%012d", (userID+4)%1000000000000), "android-uk"), "application/json")
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"needs_enrollment":true`)) {
		t.Fatalf("android registration parity status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLiveUnauthenticatedTunnelGuestUploadAccessAndExpiration(t *testing.T) {
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
	fs, err := storage.NewFilesystem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	upload := services.NewUpload(cfg, db, rdb, fs, services.NewTracker(db))
	uploadHandler := handlers.NewUploadHandler(cfg, db, rdb, fs, upload)
	tunnelHandler := handlers.NewTunnelHandler(cfg, db, fs)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if id, parseErr := strconv.Atoi(c.GetHeader("X-Test-User")); parseErr == nil && id > 0 {
			c.Set(middleware.CNSUserKey, &middleware.CNSUser{ID: id, Username: "live-user"})
		}
		c.Next()
	})
	router.POST("/api/me/tunnels/start", tunnelHandler.Start)
	router.POST("/api/me/tunnels/join", tunnelHandler.Join)
	router.POST("/api/me/tunnels/:id/confirm", tunnelHandler.Confirm)
	router.GET("/api/me/tunnels/:id/peer-wrap-key", tunnelHandler.PeerWrapKey)
	router.POST("/api/upload/init", uploadHandler.Init)
	router.POST("/api/upload/chunk", uploadHandler.Chunk)
	router.POST("/api/upload/complete", uploadHandler.Complete)
	router.GET("/api/upload/status/:session_id", uploadHandler.AssemblyStatus)
	router.POST("/api/upload/finalize", uploadHandler.Finalize)
	router.GET("/api/tunnels/:id/files/:file_id/access", tunnelHandler.GuestFileAccess)
	router.GET("/api/me/tunnels/:id/participant-keys", tunnelHandler.GetParticipantPublicKeys)

	startBody, _ := json.Marshal(models.TunnelStartRequest{Duration: "10m", DeviceID: "00000000-0000-4000-8000-000000000011"})
	rec := request(router, http.MethodPost, "/api/me/tunnels/start", startBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("guest tunnel start status=%d body=%s", rec.Code, rec.Body.String())
	}
	var started models.TunnelStartResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	joinBody, _ := json.Marshal(models.TunnelJoinRequest{Code: started.Tunnel.Code, DeviceID: "00000000-0000-4000-8000-000000000012"})
	rec = request(router, http.MethodPost, "/api/me/tunnels/join", joinBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("guest tunnel join status=%d body=%s", rec.Code, rec.Body.String())
	}
	confirmBody, _ := json.Marshal(models.TunnelConfirmRequest{DeviceID: "00000000-0000-4000-8000-000000000011"})
	// The host's device ID is visible to participants; it must not be
	// enough to act as the host without the host token.
	rec = request(router, http.MethodPost, "/api/me/tunnels/"+started.Tunnel.ID+"/confirm", confirmBody, "application/json")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("host confirm without host token status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = requestWithHeaders(router, http.MethodGet, "/api/me/tunnels/"+started.Tunnel.ID+"/participant-keys", nil, "",
		map[string]string{"X-Device-ID": "00000000-0000-4000-8000-000000000011"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("participant keys with host device ID only status=%d body=%s", rec.Code, rec.Body.String())
	}
	hostHeaders := map[string]string{"X-Host-Token": started.HostToken}
	rec = requestWithHeaders(router, http.MethodPost, "/api/me/tunnels/"+started.Tunnel.ID+"/confirm", confirmBody, "application/json", hostHeaders)
	if rec.Code != http.StatusOK {
		t.Fatalf("guest initiator confirm status=%d body=%s", rec.Code, rec.Body.String())
	}
	confirmBody, _ = json.Marshal(models.TunnelConfirmRequest{DeviceID: "00000000-0000-4000-8000-000000000012"})
	rec = request(router, http.MethodPost, "/api/me/tunnels/"+started.Tunnel.ID+"/confirm", confirmBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("guest peer confirm status=%d body=%s", rec.Code, rec.Body.String())
	}
	initBody, _ := json.Marshal(models.UploadInitRequest{FileName: "guest.txt", FileSize: 5, TotalChunks: 1, ChunkSize: 5, TunnelID: started.Tunnel.ID})
	rec = request(router, http.MethodPost, "/api/upload/init", initBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("guest init status=%d body=%s", rec.Code, rec.Body.String())
	}
	var initResp models.UploadInitResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &initResp)
	var chunk bytes.Buffer
	form := multipart.NewWriter(&chunk)
	_ = form.WriteField("session_id", initResp.SessionID)
	_ = form.WriteField("chunk_index", "0")
	part, _ := form.CreateFormFile("chunk", "guest.txt")
	_, _ = part.Write([]byte("guest"))
	_ = form.Close()
	rec = request(router, http.MethodPost, "/api/upload/chunk", chunk.Bytes(), form.FormDataContentType())
	if rec.Code != http.StatusOK {
		t.Fatalf("guest chunk status=%d body=%s", rec.Code, rec.Body.String())
	}
	completeBody, _ := json.Marshal(models.UploadCompleteRequest{SessionID: initResp.SessionID, Confirmed: true})
	rec = request(router, http.MethodPost, "/api/upload/complete", completeBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("guest complete status=%d body=%s", rec.Code, rec.Body.String())
	}
	waitForAssembly(t, router, initResp.SessionID)
	finalizeBody, _ := json.Marshal(models.UploadFinalizeRequest{
		SessionID: initResp.SessionID, TunnelID: started.Tunnel.ID,
		WrappedDEKB64: base64.StdEncoding.EncodeToString([]byte("guest-dek")),
		DEKWrapAlg:    "RAW-DEK", DEKWrapVersion: 1,
	})
	rec = request(router, http.MethodPost, "/api/upload/finalize", finalizeBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("guest finalize status=%d body=%s", rec.Code, rec.Body.String())
	}
	var finalized models.UploadFinalizeResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &finalized)
	rec = request(router, http.MethodGet, "/api/tunnels/"+started.Tunnel.ID+"/files/"+finalized.FileID+"/access", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("guest access status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := db.DeleteTunnel(context.Background(), started.Tunnel.ID); err != nil {
		t.Fatal(err)
	}
	rec = request(router, http.MethodGet, "/api/tunnels/"+started.Tunnel.ID+"/files/"+finalized.FileID+"/access", nil, "")
	if rec.Code == http.StatusOK {
		t.Fatal("guest access remained available after tunnel deletion")
	}
}
func TestLiveCrossAccountTunnelUploadAndRecipientAccess(t *testing.T) {
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
	fs, err := storage.NewFilesystem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	upload := services.NewUpload(cfg, db, rdb, fs, services.NewTracker(db))
	uploadHandler := handlers.NewUploadHandler(cfg, db, rdb, fs, upload)
	tunnelHandler := handlers.NewTunnelHandler(cfg, db, fs)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if id, parseErr := strconv.Atoi(c.GetHeader("X-Test-User")); parseErr == nil && id > 0 {
			c.Set(middleware.CNSUserKey, &middleware.CNSUser{ID: id, Username: "live-user-" + strconv.Itoa(id)})
		}
		c.Next()
	})
	router.POST("/api/me/tunnels/start", tunnelHandler.Start)
	router.POST("/api/me/tunnels/join", tunnelHandler.Join)
	router.POST("/api/me/tunnels/:id/confirm", tunnelHandler.Confirm)
	router.GET("/api/me/tunnels/:id/peer-wrap-key", tunnelHandler.PeerWrapKey)
	router.POST("/api/upload/init", uploadHandler.Init)
	router.POST("/api/upload/chunk", uploadHandler.Chunk)
	router.POST("/api/upload/complete", uploadHandler.Complete)
	router.GET("/api/upload/status/:session_id", uploadHandler.AssemblyStatus)
	router.POST("/api/upload/finalize", uploadHandler.Finalize)
	router.GET("/api/me/files/:id/access", func(c *gin.Context) {
		handlers.NewRecentUploadsHandler(cfg, db).FileAccess(c)
	})

	initiatorDevice := "00000000-0000-4000-8000-000000000021"
	peerDevice := "00000000-0000-4000-8000-000000000022"
	peerKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateOrUpdateUserDevice(context.Background(), &models.UserDevice{
		ID: peerDevice, CNSUserID: 991004, DeviceLabel: "live peer",
		PublicKeyJWK: rsaPublicJWK(&peerKey.PublicKey), KeyAlgorithm: "RSA-OAEP-2048", KeyVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	startBody, _ := json.Marshal(models.TunnelStartRequest{Duration: "10m", DeviceID: initiatorDevice})
	rec := requestAs(router, 991003, http.MethodPost, "/api/me/tunnels/start", startBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", rec.Code, rec.Body.String())
	}
	var started models.TunnelStartResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	joinBody, _ := json.Marshal(models.TunnelJoinRequest{
		Code: started.Tunnel.Code, DeviceID: peerDevice,
		PublicKeyJWK: rsaPublicJWK(&peerKey.PublicKey),
		KeyAlgorithm: "RSA-OAEP-2048", KeyVersion: 1,
	})
	rec = requestAs(router, 991004, http.MethodPost, "/api/me/tunnels/join", joinBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("join status=%d body=%s", rec.Code, rec.Body.String())
	}
	confirmBody, _ := json.Marshal(models.TunnelConfirmRequest{DeviceID: initiatorDevice})
	rec = requestAs(router, 991003, http.MethodPost, "/api/me/tunnels/"+started.Tunnel.ID+"/confirm", confirmBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("initiator confirm status=%d body=%s", rec.Code, rec.Body.String())
	}
	confirmBody, _ = json.Marshal(models.TunnelConfirmRequest{DeviceID: peerDevice})
	rec = requestAs(router, 991004, http.MethodPost, "/api/me/tunnels/"+started.Tunnel.ID+"/confirm", confirmBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("peer confirm status=%d body=%s", rec.Code, rec.Body.String())
	}
	peerKeyResp := requestAs(router, 991003, http.MethodGet,
		"/api/me/tunnels/"+started.Tunnel.ID+"/peer-wrap-key", nil, "")
	if peerKeyResp.Code != http.StatusOK {
		t.Fatalf("peer key status=%d body=%s", peerKeyResp.Code, peerKeyResp.Body.String())
	}
	var peerKeyPayload models.TunnelPeerWrapKeyResponse
	if err := json.Unmarshal(peerKeyResp.Body.Bytes(), &peerKeyPayload); err != nil {
		t.Fatal(err)
	}
	peerWrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &peerKey.PublicKey, []byte("peer-dek"), nil)
	if err != nil {
		t.Fatal(err)
	}

	initBody, _ := json.Marshal(models.UploadInitRequest{FileName: "cross-account.txt", FileSize: 5, TotalChunks: 1, ChunkSize: 5, TunnelID: started.Tunnel.ID})
	rec = requestAs(router, 991003, http.MethodPost, "/api/upload/init", initBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("init status=%d body=%s", rec.Code, rec.Body.String())
	}
	var initResp models.UploadInitResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &initResp)
	var chunk bytes.Buffer
	form := multipart.NewWriter(&chunk)
	_ = form.WriteField("session_id", initResp.SessionID)
	_ = form.WriteField("chunk_index", "0")
	part, _ := form.CreateFormFile("chunk", "cross-account.txt")
	_, _ = part.Write([]byte("cross"))
	_ = form.Close()
	rec = requestAs(router, 991003, http.MethodPost, "/api/upload/chunk", chunk.Bytes(), form.FormDataContentType())
	if rec.Code != http.StatusOK {
		t.Fatalf("chunk status=%d body=%s", rec.Code, rec.Body.String())
	}
	completeBody, _ := json.Marshal(models.UploadCompleteRequest{SessionID: initResp.SessionID, Confirmed: true})
	rec = requestAs(router, 991003, http.MethodPost, "/api/upload/complete", completeBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("complete status=%d body=%s", rec.Code, rec.Body.String())
	}
	waitForAssembly(t, router, initResp.SessionID)
	finalizeBody, _ := json.Marshal(models.UploadFinalizeRequest{
		SessionID: initResp.SessionID, TunnelID: started.Tunnel.ID,
		WrappedDEKB64: base64.StdEncoding.EncodeToString([]byte("owner-dek")),
		DEKWrapAlg:    "RSA-OAEP-2048-v1", DEKWrapVersion: 1,
		PeerWrappedDEKB64: base64.StdEncoding.EncodeToString(peerWrapped),
		PeerDEKWrapAlg:    "RSA-OAEP-2048-v1", PeerDEKWrapVersion: 1,
	})
	rec = requestAs(router, 991003, http.MethodPost, "/api/upload/finalize", finalizeBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("finalize status=%d body=%s", rec.Code, rec.Body.String())
	}
	var finalized models.UploadFinalizeResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &finalized)
	rec = requestAs(router, 991004, http.MethodGet, "/api/me/files/"+finalized.FileID+"/access?device_id="+peerDevice, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("recipient access status=%d body=%s", rec.Code, rec.Body.String())
	}
	var access models.FileAccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &access); err != nil {
		t.Fatal(err)
	}
	storedPeerWrapped, err := base64.StdEncoding.DecodeString(access.FileKeyEnvelope.WrappedDEKB64)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, peerKey, storedPeerWrapped, nil)
	if err != nil || string(decrypted) != "peer-dek" {
		t.Fatalf("peer envelope did not round-trip: %v", err)
	}
}

func rsaPublicJWK(key *rsa.PublicKey) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"kty":"RSA","n":"%s","e":"AQAB"}`,
		base64.RawURLEncoding.EncodeToString(key.N.Bytes())))
}

func hybridWrap(publicKey rsa.PublicKey, plaintext []byte) ([]byte, error) {
	aesKey := make([]byte, 32)
	if _, err := rand.Read(aesKey); err != nil {
		return nil, err
	}
	wrappedKey, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &publicKey, aesKey, nil)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return append(append(append([]byte{}, wrappedKey...), nonce...), gcm.Seal(nil, nonce, plaintext, nil)...), nil
}

func hybridUnwrap(privateKey *rsa.PrivateKey, wrapped []byte) ([]byte, error) {
	if len(wrapped) < 268 {
		return nil, fmt.Errorf("wrapped identity key is too short")
	}
	aesKey, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, privateKey, wrapped[:256], nil)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, wrapped[256:268], wrapped[268:], nil)
}

func encryptTestDEK(dek, plaintext []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return gcm.Seal(nil, nonce, plaintext, nil), nonce, nil
}

func decryptTestDEK(dek, ciphertext, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func mustBase64(value string) []byte {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		panic(err)
	}
	return decoded
}

func request(router http.Handler, method, path string, body []byte, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func requestWithHeaders(router http.Handler, method, path string, body []byte, contentType string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func requestAs(router http.Handler, userID int, method, path string, body []byte, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("X-Test-User", strconv.Itoa(userID))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func waitForAssembly(t *testing.T, router http.Handler, sessionID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		rec := request(router, http.MethodGet, "/api/upload/status/"+sessionID, nil, "")
		if rec.Code == http.StatusOK && bytes.Contains(rec.Body.Bytes(), []byte(`"status":"done"`)) {
			return
		}
		if time.Now().After(ctxDeadline(ctx)) {
			t.Fatalf("assembly did not complete: status=%d body=%s", rec.Code, rec.Body.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func ctxDeadline(ctx context.Context) time.Time {
	deadline, _ := ctx.Deadline()
	return deadline
}
