package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sendly/internal/config"
	"sendly/internal/models"
	"sendly/internal/storage"
)

// A device ID belongs to the account that first registered it: another
// account re-registering (or recovering onto) the same ID must be refused
// instead of silently moving the device between accounts.
func TestLiveDeviceIDCannotMoveBetweenAccounts(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.RunMigrations(ctx, cfg.MigrationsDir); err != nil {
		t.Fatal(err)
	}

	owner := time.Now().UnixNano()
	other := owner + 1
	deviceID := fmt.Sprintf("00000000-0000-4000-8000-%012d", owner%1000000000000)
	device := func(userID int64, n string) *models.UserDevice {
		return &models.UserDevice{
			ID: deviceID, CNSUserID: userID, DeviceLabel: "ownership-test",
			PublicKeyJWK: json.RawMessage(fmt.Sprintf(`{"kty":"RSA","n":%q,"e":"AQAB"}`, n)),
			KeyAlgorithm: "RSA-OAEP-2048", KeyVersion: 1,
		}
	}

	if err := db.CreateOrUpdateUserDevice(ctx, device(owner, "owner")); err != nil {
		t.Fatal(err)
	}
	// The owner may re-register its own device (e.g. after a revocation).
	if err := db.CreateOrUpdateUserDevice(ctx, device(owner, "owner-rotated")); err != nil {
		t.Fatalf("owner re-registration failed: %v", err)
	}

	if err := db.CreateOrUpdateUserDevice(ctx, device(other, "attacker")); err != models.ErrDeviceIDConflict {
		t.Fatalf("expected ErrDeviceIDConflict for another account, got %v", err)
	}
	err = db.ResetTrustedDeviceState(ctx, device(other, "attacker"), &models.UserKeyEnvelope{
		CNSUserID: other, DeviceID: deviceID, WrappedUserKey: []byte("wrapped"),
		UKWrapAlg: "RSA-OAEP-2048-v1", UKWrapMeta: json.RawMessage(`{}`), KeyVersion: 1,
	})
	if err != models.ErrDeviceIDConflict {
		t.Fatalf("expected ErrDeviceIDConflict on recovery, got %v", err)
	}

	devices, err := db.GetActiveDevicesByUser(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].ID != deviceID || string(devices[0].PublicKeyJWK) == "" {
		t.Fatalf("owner lost its device: %+v", devices)
	}
	var key map[string]string
	if err := json.Unmarshal(devices[0].PublicKeyJWK, &key); err != nil || key["n"] != "owner-rotated" {
		t.Fatalf("owner device key was overwritten: %s", devices[0].PublicKeyJWK)
	}
}
