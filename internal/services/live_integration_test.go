package services

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"sendly/internal/config"
	"sendly/internal/models"
	"sendly/internal/storage"
)

func TestLiveDeviceRecovery(t *testing.T) {
	if os.Getenv("SENDLY_LIVE_INTEGRATION") != "1" {
		t.Skip("set SENDLY_LIVE_INTEGRATION=1 to run against live Postgres")
	}

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

	userID := time.Now().UnixNano()

	service := &DeviceIdentity{DB: db}
	request := func(id, wrapped string) models.DeviceRegisterRequest {
		return models.DeviceRegisterRequest{
			DeviceID: id, DeviceLabel: id,
			PublicKeyJWK: json.RawMessage(`{"kty":"RSA","n":"test","e":"AQAB"}`),
			KeyAlgorithm: "RSA-OAEP-2048", KeyVersion: 1,
			WrappedUserKeyB64: wrapped, UKWrapAlg: "RSA-OAEP-2048-v1",
			UKWrapMeta: json.RawMessage(`{"type":"test"}`),
		}
	}

	oldDeviceID := "00000000-0000-4000-8000-000000000001"
	newDeviceID := "00000000-0000-4000-8000-000000000002"
	if _, err := service.Register(ctx, userID, request(oldDeviceID, "b2xk"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Register(ctx, userID, request(newDeviceID, "bmV3"), true); err != nil {
		t.Fatal(err)
	}

	if trusted, err := service.IsTrusted(ctx, userID, oldDeviceID); err != nil || trusted {
		t.Fatalf("old device remains trusted: trusted=%v err=%v", trusted, err)
	}
	if trusted, err := service.IsTrusted(ctx, userID, newDeviceID); err != nil || !trusted {
		t.Fatalf("new device is not trusted: trusted=%v err=%v", trusted, err)
	}
	if _, err := db.GetUserKeyEnvelopeForDevice(ctx, userID, oldDeviceID); err == nil {
		t.Fatal("old device envelope was not removed")
	}
	if _, err := db.GetUserKeyEnvelopeForDevice(ctx, userID, newDeviceID); err != nil {
		t.Fatalf("new device envelope missing: %v", err)
	}
}
