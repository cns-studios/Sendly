package integration

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"sendly/internal/config"
	"sendly/internal/handlers"
	"sendly/internal/middleware"
	"sendly/internal/models"
	"sendly/internal/storage"

	"github.com/gin-gonic/gin"
)

// Declining the first joiner used to clear the tunnel's single "peer" slot,
// so starting the session never made it active and everyone else stayed on
// the waiting screen. Now the host's Start activates it, and the declined
// joiner is told so and can't come back.
func TestLiveDeclinedJoinerDoesNotBlockTheSession(t *testing.T) {
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
	if err := db.RunMigrations(context.Background(), cfg.MigrationsDir); err != nil {
		t.Fatal(err)
	}
	fs, err := storage.NewFilesystem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	tunnels := handlers.NewTunnelHandler(cfg, db, fs)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if id, parseErr := strconv.Atoi(c.GetHeader("X-Test-User")); parseErr == nil && id > 0 {
			c.Set(middleware.CNSUserKey, &middleware.CNSUser{ID: id, Username: "live-user-" + strconv.Itoa(id)})
		}
		c.Next()
	})
	router.POST("/api/me/tunnels/start", tunnels.Start)
	router.POST("/api/me/tunnels/join", tunnels.Join)
	router.GET("/api/me/tunnels/:id", tunnels.Get)
	router.POST("/api/me/tunnels/:id/confirm", tunnels.Confirm)
	router.POST("/api/me/tunnels/:id/participants/:participant_id/approve", tunnels.ApproveParticipant)
	router.POST("/api/me/tunnels/:id/participants/:participant_id/reject", tunnels.RejectParticipant)

	base := int(time.Now().UnixNano()%1000000000) + 100
	host, declined, accepted := base, base+1, base+2
	device := func(userID int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", userID) }
	call := func(userID int, method, path string, body any) (int, map[string]any) {
		var payload []byte
		if body != nil {
			payload, _ = json.Marshal(body)
		}
		rec := requestAs(router, userID, method, path, payload, "application/json")
		out := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	code, out := call(host, http.MethodPost, "/api/me/tunnels/start", models.TunnelStartRequest{Duration: "10m", DeviceID: device(host)})
	if code != http.StatusOK {
		t.Fatalf("start status=%d %v", code, out)
	}
	tunnel := out["tunnel"].(map[string]any)
	tunnelID, joinCode := tunnel["id"].(string), tunnel["code"].(string)
	join := func(userID int) (int, map[string]any) {
		key, _ := rsa.GenerateKey(rand.Reader, 2048)
		return call(userID, http.MethodPost, "/api/me/tunnels/join", models.TunnelJoinRequest{
			Code: joinCode, DeviceID: device(userID), PublicKeyJWK: rsaPublicJWK(&key.PublicKey), KeyAlgorithm: "RSA-OAEP-2048", KeyVersion: 1,
		})
	}
	for _, joiner := range []int{declined, accepted} {
		if code, out := join(joiner); code != http.StatusOK {
			t.Fatalf("join status=%d %v", code, out)
		}
	}
	participants, err := db.GetTunnelParticipants(context.Background(), tunnelID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range participants {
		action := ""
		switch p.CNSUserID.Int64 {
		case int64(declined):
			action = "reject"
		case int64(accepted):
			action = "approve"
		default:
			continue
		}
		if code, out := call(host, http.MethodPost, "/api/me/tunnels/"+tunnelID+"/participants/"+p.ID+"/"+action, nil); code != http.StatusOK {
			t.Fatalf("%s status=%d %v", action, code, out)
		}
	}

	if code, out := call(host, http.MethodPost, "/api/me/tunnels/"+tunnelID+"/confirm", models.TunnelConfirmRequest{DeviceID: device(host)}); code != http.StatusOK {
		t.Fatalf("start session status=%d %v", code, out)
	}
	code, out = call(accepted, http.MethodGet, "/api/me/tunnels/"+tunnelID, nil)
	if code != http.StatusOK || out["tunnel"].(map[string]any)["status"] != models.TunnelStatusActive {
		t.Fatalf("the approved joiner should see an active session: status=%d %v", code, out)
	}

	code, out = call(declined, http.MethodGet, "/api/me/tunnels/"+tunnelID, nil)
	if code != http.StatusForbidden || out["code"] != models.ErrParticipantRejected.Code {
		t.Fatalf("the declined joiner should be told it was declined: status=%d %v", code, out)
	}
	if code, out := join(declined); code != http.StatusForbidden || out["code"] != models.ErrParticipantRejected.Code {
		t.Fatalf("the declined joiner must not rejoin: status=%d %v", code, out)
	}
	if code, _ := call(accepted, http.MethodPost, "/api/me/tunnels/"+tunnelID+"/confirm", models.TunnelConfirmRequest{DeviceID: device(accepted)}); code == http.StatusOK {
		t.Fatal("only the host may start the session")
	}
}
