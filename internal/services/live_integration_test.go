package services

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	"sendly/internal/config"
	"sendly/internal/models"
	"sendly/internal/storage"
)

func liveDB(t *testing.T) (context.Context, *storage.Postgres) {
	t.Helper()
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(func() {
		cancel()
		db.Close()
	})
	if err := db.RunMigrations(ctx, cfg.MigrationsDir); err != nil {
		t.Fatal(err)
	}
	return ctx, db
}

func TestLiveDeviceRecovery(t *testing.T) {
	ctx, db := liveDB(t)
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

func TestLiveIdentityKeypairRegistrationEnrollmentAndRecovery(t *testing.T) {
	ctx, db := liveDB(t)
	userID := time.Now().UnixNano()
	service := &DeviceIdentity{DB: db}
	deviceA, privateA := liveRSADevice(t, userID%1000000+101)
	deviceB, privateB := liveRSADevice(t, userID%1000000+102)
	deviceC, privateC := liveRSADevice(t, userID%1000000+103)
	identityPrivate := []byte(`{"kty":"RSA","n":"identity-private-key-material","d":"test"}`)
	identityPublic := json.RawMessage(`{"kty":"RSA","n":"identity-public-key","e":"AQAB"}`)

	registration := func(device models.UserDevice, wrappedUser, wrappedIdentity []byte) models.DeviceRegisterRequest {
		return models.DeviceRegisterRequest{
			DeviceID: device.ID, DeviceLabel: device.DeviceLabel,
			PublicKeyJWK: device.PublicKeyJWK, KeyAlgorithm: device.KeyAlgorithm, KeyVersion: 1,
			WrappedUserKeyB64: base64.StdEncoding.EncodeToString(wrappedUser),
			UKWrapAlg:         "RSA-OAEP-2048-v1", UKWrapMeta: json.RawMessage(`{"type":"test"}`),
			IdentityPublicKeyJWK: identityPublic, IdentityKeyAlgorithm: "RSA-OAEP-2048",
			IdentityKeyVersion: 1, WrappedIdentityPrivateKeyB64: base64.StdEncoding.EncodeToString(wrappedIdentity),
			IdentityKeyWrapAlg:  "RSA-OAEP-2048+AES-GCM-256-v1",
			IdentityKeyWrapMeta: json.RawMessage(`{"type":"test"}`),
		}
	}

	wrappedA, err := liveWrapIdentityKey(privateA, identityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Register(ctx, userID, registration(deviceA, []byte("old-user-key"), wrappedA), false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetUserKeyEnvelopeForDevice(ctx, userID, deviceA.ID); err != nil {
		t.Fatalf("legacy user-key envelope missing: %v", err)
	}
	if _, err := db.GetUserIdentityKey(ctx, userID, 1); err != nil {
		t.Fatalf("identity key missing: %v", err)
	}
	if _, err := db.GetUserIdentityKeyDeviceEnvelope(ctx, userID, deviceA.ID, 1); err != nil {
		t.Fatalf("device A identity envelope missing: %v", err)
	}

	result, err := service.Register(ctx, userID, registration(deviceB, []byte("new-user-key"), nil), false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NeedsEnrollment {
		t.Fatal("new device did not require enrollment")
	}
	enrollment, err := service.CreateEnrollment(ctx, userID, deviceB.ID)
	if err != nil {
		t.Fatal(err)
	}
	wrappedB, err := liveWrapIdentityKey(privateB, identityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	approval := models.ApproveEnrollmentRequest{
		ApproverDeviceID: deviceA.ID, VerificationCode: enrollment.VerificationCode,
		WrappedUserKeyB64: base64.StdEncoding.EncodeToString([]byte("new-user-key")),
		UKWrapAlg:         "RSA-OAEP-2048-v1", UKWrapMeta: json.RawMessage(`{"type":"approval"}`),
		WrappedIdentityPrivateKeyB64: base64.StdEncoding.EncodeToString(wrappedB),
		IdentityKeyWrapAlg:           "RSA-OAEP-2048+AES-GCM-256-v1",
		IdentityKeyWrapMeta:          json.RawMessage(`{"type":"approval"}`), IdentityKeyVersion: 1,
	}
	if err := service.Approve(ctx, userID, enrollment.ID, approval); err != nil {
		t.Fatal(err)
	}
	storedB, err := db.GetUserIdentityKeyDeviceEnvelope(ctx, userID, deviceB.ID, 1)
	if err != nil {
		t.Fatalf("device B identity envelope missing: %v", err)
	}
	recoveredIdentity, err := liveUnwrapIdentityKey(privateB, storedB.WrappedPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(recoveredIdentity) != string(identityPrivate) {
		t.Fatal("device B identity private key differs from device A material")
	}
	storedA, err := db.GetUserIdentityKeyDeviceEnvelope(ctx, userID, deviceA.ID, 1)
	if err != nil {
		t.Fatalf("device A identity envelope missing: %v", err)
	}
	recoveredA, err := liveUnwrapIdentityKey(privateA, storedA.WrappedPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(recoveredA) != string(recoveredIdentity) {
		t.Fatal("device A and B identity private keys differ")
	}
	if _, err := db.GetUserKeyEnvelopeForDevice(ctx, userID, deviceB.ID); err != nil {
		t.Fatalf("legacy device B envelope missing: %v", err)
	}

	wrappedC, err := liveWrapIdentityKey(privateC, identityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Register(ctx, userID, registration(deviceC, []byte("recovery-user-key"), wrappedC), true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetUserIdentityKeyDeviceEnvelope(ctx, userID, deviceA.ID, 1); err == nil {
		t.Fatal("device A identity envelope survived recovery")
	}
	if _, err := db.GetUserIdentityKeyDeviceEnvelope(ctx, userID, deviceC.ID, 1); err != nil {
		t.Fatalf("device C identity envelope missing after recovery: %v", err)
	}
}

func TestLiveUserCacheUpsertGetAndDeactivateStale(t *testing.T) {
	ctx, db := liveDB(t)
	userID := time.Now().UnixNano()

	if _, err := db.GetUser(ctx, userID); err != models.ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound before first sync, got %v", err)
	}

	if err := db.UpsertUser(ctx, &models.User{
		CNSUserID: userID, Username: "livetest", Status: models.UserStatusActive,
		AvatarURL: sql.NullString{String: "https://cdn.example/livetest.png", Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := db.GetUser(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Username != "livetest" || stored.Status != models.UserStatusActive {
		t.Fatalf("unexpected stored user: %+v", stored)
	}
	if !stored.AvatarURL.Valid || stored.AvatarURL.String != "https://cdn.example/livetest.png" {
		t.Fatalf("unexpected avatar: %+v", stored.AvatarURL)
	}
	firstSync := stored.LastSyncedAt

	if err := db.UpsertUser(ctx, &models.User{
		CNSUserID: userID, Username: "livetest-renamed", Status: models.UserStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	restored, err := db.GetUser(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Username != "livetest-renamed" {
		t.Fatalf("expected upsert to refresh username, got %q", restored.Username)
	}
	if !restored.LastSyncedAt.After(firstSync) {
		t.Fatal("expected upsert to advance last_synced_at")
	}

	count, err := db.DeactivateStaleUsers(ctx, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if count < 1 {
		t.Fatal("expected the just-synced user to be deactivated when staleBefore is in the future")
	}
	deactivated, err := db.GetUser(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if deactivated.Status != models.UserStatusInactive || !deactivated.DeactivatedAt.Valid {
		t.Fatalf("expected user to be marked inactive with a deactivated_at, got %+v", deactivated)
	}

	count, err = db.DeactivateStaleUsers(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected no active users to deactivate on second pass, got %d", count)
	}
}

func liveRSADevice(t *testing.T, suffix int64) (models.UserDevice, *rsa.PrivateKey) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	n := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.PublicKey.E)).Bytes())
	return models.UserDevice{
		ID:           fmt.Sprintf("00000000-0000-4000-8000-%012d", suffix%1000000000000),
		DeviceLabel:  "live identity device",
		PublicKeyJWK: json.RawMessage(fmt.Sprintf(`{"kty":"RSA","n":"%s","e":"%s"}`, n, e)),
		KeyAlgorithm: "RSA-OAEP-2048", KeyVersion: 1,
	}, privateKey
}

func liveWrapIdentityKey(devicePrivate *rsa.PrivateKey, plaintext []byte) ([]byte, error) {
	aesKey := make([]byte, 32)
	if _, err := rand.Read(aesKey); err != nil {
		return nil, err
	}
	wrappedAES, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &devicePrivate.PublicKey, aesKey, nil)
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
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	return append(append(append([]byte{}, wrappedAES...), nonce...), ciphertext...), nil
}

func liveUnwrapIdentityKey(devicePrivate *rsa.PrivateKey, wrapped []byte) ([]byte, error) {
	if len(wrapped) < 268 {
		return nil, fmt.Errorf("identity envelope is too short")
	}
	aesKey, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, devicePrivate, wrapped[:256], nil)
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
