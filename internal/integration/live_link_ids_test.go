package integration

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"sendly/internal/config"
	"sendly/internal/handlers"
	"sendly/internal/models"
	"sendly/internal/services"
	"sendly/internal/storage"

	"github.com/gin-gonic/gin"
)

func TestLiveLinkUploadWithClientFileID(t *testing.T) {
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
	fs, err := storage.NewFilesystem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	tracker := services.NewTracker(db)
	upload := services.NewUpload(cfg, db, fs, tracker)
	uploadHandler := handlers.NewUploadHandler(cfg, db, fs, upload)
	downloadHandler := handlers.NewDownloadHandler(cfg, db, fs, tracker)
	router := gin.New()
	router.POST("/api/upload/init", uploadHandler.Init)
	router.POST("/api/upload/chunk", uploadHandler.Chunk)
	router.POST("/api/upload/complete", uploadHandler.Complete)
	router.GET("/api/upload/status/:session_id", uploadHandler.AssemblyStatus)
	router.POST("/api/upload/finalize", uploadHandler.Finalize)
	router.GET("/api/file/:id", downloadHandler.GetMetadata)
	router.GET("/api/file/:id/download", downloadHandler.Download)

	payload := []byte("short link ciphertext")
	initWith := func(fileID string) *models.UploadInitResponse {
		t.Helper()
		body, _ := json.Marshal(models.UploadInitRequest{
			FileName: "short.txt", FileSize: int64(len(payload)), TotalChunks: 1, ChunkSize: int64(len(payload)), FileID: fileID,
		})
		rec := request(router, http.MethodPost, "/api/upload/init", body, "application/json")
		if rec.Code != http.StatusOK {
			t.Fatalf("init %q status=%d body=%s", fileID, rec.Code, rec.Body.String())
		}
		var resp models.UploadInitResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return &resp
	}
	initStatus := func(fileID string) (int, string) {
		body, _ := json.Marshal(models.UploadInitRequest{
			FileName: "short.txt", FileSize: int64(len(payload)), TotalChunks: 1, ChunkSize: int64(len(payload)), FileID: fileID,
		})
		rec := request(router, http.MethodPost, "/api/upload/init", body, "application/json")
		var errResp models.ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
		return rec.Code, errResp.Code
	}

	generated := initWith("")
	if !models.IsValidFileID(generated.FileID) {
		t.Fatalf("server generated an invalid file id %q", generated.FileID)
	}

	for _, bad := range []string{"short", "UPPERCASEFILEID12", "has-dash-file-id1", "abcdefghijklmnopqr"} {
		if status, code := initStatus(bad); status != http.StatusBadRequest || code != "INVALID_FILE_ID" {
			t.Fatalf("file id %q: status=%d code=%q", bad, status, code)
		}
	}

	fileID := models.GenerateID(models.FileIDLength)
	session := initWith(fileID)
	if session.FileID != fileID {
		t.Fatalf("init returned file id %q, want the client's %q", session.FileID, fileID)
	}
	if status, code := initStatus(fileID); status != http.StatusConflict || code != "FILE_ID_TAKEN" {
		t.Fatalf("second session with the same file id: status=%d code=%q", status, code)
	}

	var chunk bytes.Buffer
	form := multipart.NewWriter(&chunk)
	_ = form.WriteField("session_id", session.SessionID)
	_ = form.WriteField("chunk_index", "0")
	part, _ := form.CreateFormFile("chunk", "short.txt")
	_, _ = part.Write(payload)
	_ = form.Close()
	if rec := request(router, http.MethodPost, "/api/upload/chunk", chunk.Bytes(), form.FormDataContentType()); rec.Code != http.StatusOK {
		t.Fatalf("chunk status=%d body=%s", rec.Code, rec.Body.String())
	}
	completeBody, _ := json.Marshal(models.UploadCompleteRequest{SessionID: session.SessionID, Confirmed: true})
	if rec := request(router, http.MethodPost, "/api/upload/complete", completeBody, "application/json"); rec.Code != http.StatusOK {
		t.Fatalf("complete status=%d body=%s", rec.Code, rec.Body.String())
	}
	waitForAssembly(t, router, session.SessionID)
	finalizeBody, _ := json.Marshal(models.UploadFinalizeRequest{SessionID: session.SessionID, Duration: "7d"})
	rec := request(router, http.MethodPost, "/api/upload/finalize", finalizeBody, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("finalize status=%d body=%s", rec.Code, rec.Body.String())
	}
	var finalized models.UploadFinalizeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &finalized); err != nil {
		t.Fatal(err)
	}
	if finalized.FileID != fileID || finalized.ShareURL != cfg.BaseURL+"/shared/"+fileID {
		t.Fatalf("finalize kept file id %q and share url %q", finalized.FileID, finalized.ShareURL)
	}

	if status, code := initStatus(fileID); status != http.StatusConflict || code != "FILE_ID_TAKEN" {
		t.Fatalf("file id of a stored file was accepted again: status=%d code=%q", status, code)
	}

	rec = request(router, http.MethodGet, "/api/file/"+fileID, nil, "")
	var meta models.FileMetadata
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &meta) != nil || meta.ID != fileID || meta.OriginalName != "short.txt" {
		t.Fatalf("metadata status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = request(router, http.MethodGet, "/api/file/"+fileID+"/download", nil, "")
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), payload) {
		t.Fatalf("download status=%d body=%q", rec.Code, rec.Body.String())
	}
}
