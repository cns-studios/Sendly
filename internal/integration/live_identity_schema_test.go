package integration

import (
	"context"
	"database/sql"
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

func TestLiveIdentitySchemaStorageRoundTrip(t *testing.T) {
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

	userID := time.Now().UnixNano()
	suffix := userID % 1000000000000
	deviceID := fmt.Sprintf("00000000-0000-4000-8000-%012d", suffix)
	envelopeID := fmt.Sprintf("00000000-0000-4000-8000-%012d", (suffix+1)%1000000000000)
	fileID := fmt.Sprintf("schema%013d", suffix%10000000000000)
	now := time.Now().UTC().Truncate(time.Microsecond)

	if err := db.CreateOrUpdateUserDevice(ctx, &models.UserDevice{
		ID: deviceID, CNSUserID: userID, DeviceLabel: "schema-test",
		PublicKeyJWK: json.RawMessage(`{"kty":"RSA","n":"test","e":"AQAB"}`),
		KeyAlgorithm: "RSA-OAEP-2048", KeyVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUserIdentityKey(ctx, &models.UserIdentityKey{
		CNSUserID: userID, KeyVersion: 1,
		PublicKeyJWK: json.RawMessage(`{"kty":"RSA","n":"identity","e":"AQAB"}`),
		KeyAlgorithm: "RSA-OAEP-2048", Status: "active",
		CreatedAt: now, ActivatedAt: sqlTime(now),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUserIdentityKeyDeviceEnvelope(ctx, &models.UserIdentityKeyDeviceEnvelope{
		ID: envelopeID, CNSUserID: userID,
		DeviceID: deviceID, IdentityKeyVersion: 1,
		WrappedPrivateKey: []byte("wrapped-private-key"),
		WrapAlg:           "RSA-OAEP-2048-v1", WrapMeta: json.RawMessage(`{"type":"test"}`),
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	key, err := db.GetUserIdentityKey(ctx, userID, 1)
	if err != nil || key.KeyAlgorithm != "RSA-OAEP-2048" {
		t.Fatalf("identity key round-trip failed: key=%+v err=%v", key, err)
	}
	envelope, err := db.GetUserIdentityKeyDeviceEnvelope(ctx, userID, deviceID, 1)
	if err != nil || string(envelope.WrappedPrivateKey) != "wrapped-private-key" {
		t.Fatalf("device envelope round-trip failed: envelope=%+v err=%v", envelope, err)
	}

	if err := db.CreateFileWithEnvelope(ctx, &models.File{
		ID: fileID, NumericCode: fmt.Sprintf("%012d", suffix%1000000000000), OriginalName: "schema.txt",
		SizeBytes: 1, UploaderIP: "127.0.0.1",
		ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now,
	}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFileAccessKeyEnvelope(ctx, &models.FileAccessKeyEnvelope{
		FileID: fileID, RecipientCNSUserID: userID,
		WrappedDEK: []byte("wrapped-dek"), DEKWrapAlg: "RSA-OAEP-2048-v1",
		DEKWrapVersion: 1, RecipientKeyVersion: 1, AccessKind: "owner",
		GrantedAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	access, err := db.GetFileAccessKeyEnvelope(ctx, fileID, userID)
	if err != nil || string(access.WrappedDEK) != "wrapped-dek" || access.AccessKind != "owner" {
		t.Fatalf("file access envelope round-trip failed: envelope=%+v err=%v", access, err)
	}
}

func sqlTime(value time.Time) sql.NullTime {
	return sql.NullTime{Time: value, Valid: true}
}
