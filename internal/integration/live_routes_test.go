package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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

	const userID = 991002
	user := &middleware.CNSUser{ID: userID, Username: "live-owner"}
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

	initBody, _ := json.Marshal(models.UploadInitRequest{
		FileName: "live.txt", FileSize: 11, TotalChunks: 1, ChunkSize: 11,
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
	_, _ = part.Write([]byte("hello world"))
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
		SessionID: initResp.SessionID, Duration: "90d",
		DeviceID:      "00000000-0000-4000-8000-000000000003",
		WrappedDEKB64: base64.StdEncoding.EncodeToString([]byte("wrapped-dek")),
		DEKWrapAlg:    "RSA-OAEP-2048-v1", DEKWrapVersion: 1,
	})
	rec = request(router, http.MethodPost, "/api/upload/finalize", finalizeBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("finalize status=%d body=%s", rec.Code, rec.Body.String())
	}
	var finalized models.UploadFinalizeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &finalized); err != nil {
		t.Fatal(err)
	}

	rec = request(router, http.MethodGet, "/api/me/recent-uploads", nil, "")
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(finalized.FileID)) {
		t.Fatalf("recent uploads status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = request(router, http.MethodGet, "/api/me/files/"+finalized.FileID+"/access?device_id=00000000-0000-4000-8000-000000000003", nil, "")
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("RSA-OAEP-2048-v1")) {
		t.Fatalf("file access status=%d body=%s", rec.Code, rec.Body.String())
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
	rec = request(router, http.MethodPost, "/api/me/devices/register", register("00000000-0000-4000-8000-000000000003", "owner-uk"), "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("web registration status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = request(router, http.MethodPost, "/android/me/devices/register", register("00000000-0000-4000-8000-000000000004", "android-uk"), "application/json")
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
	router.POST("/api/upload/init", uploadHandler.Init)
	router.POST("/api/upload/chunk", uploadHandler.Chunk)
	router.POST("/api/upload/complete", uploadHandler.Complete)
	router.GET("/api/upload/status/:session_id", uploadHandler.AssemblyStatus)
	router.POST("/api/upload/finalize", uploadHandler.Finalize)
	router.GET("/api/tunnels/:id/files/:file_id/access", tunnelHandler.GuestFileAccess)

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
	rec = request(router, http.MethodPost, "/api/me/tunnels/"+started.Tunnel.ID+"/confirm", confirmBody, "application/json")
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
	startBody, _ := json.Marshal(models.TunnelStartRequest{Duration: "10m", DeviceID: initiatorDevice})
	rec := requestAs(router, 991003, http.MethodPost, "/api/me/tunnels/start", startBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", rec.Code, rec.Body.String())
	}
	var started models.TunnelStartResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	joinBody, _ := json.Marshal(models.TunnelJoinRequest{Code: started.Tunnel.Code, DeviceID: peerDevice})
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
		PeerWrappedDEKB64: base64.StdEncoding.EncodeToString([]byte("peer-dek")),
		PeerDEKWrapAlg:    "RSA-OAEP-2048-v1", PeerDEKWrapVersion: 1,
	})
	rec = requestAs(router, 991003, http.MethodPost, "/api/upload/finalize", finalizeBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("finalize status=%d body=%s", rec.Code, rec.Body.String())
	}
	var finalized models.UploadFinalizeResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &finalized)
	rec = requestAs(router, 991004, http.MethodGet, "/api/me/files/"+finalized.FileID+"/access?device_id="+peerDevice, nil, "")
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("cGVlci1kZWs=")) {
		t.Fatalf("recipient access status=%d body=%s", rec.Code, rec.Body.String())
	}
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
