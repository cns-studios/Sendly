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
	"encoding/base64"
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
	"github.com/jmoiron/sqlx"
)

type identityTestEnv struct {
	t      *testing.T
	cfg    *config.Config
	db     *storage.Postgres
	raw    *sqlx.DB
	router *gin.Engine
}

func newIdentityTestEnv(t *testing.T) *identityTestEnv {
	t.Helper()
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
	t.Cleanup(func() { db.Close() })
	if err := db.RunMigrations(context.Background(), cfg.MigrationsDir); err != nil {
		t.Fatal(err)
	}
	// Legacy rows are no longer written by the app; the test inserts them.
	raw, err := sqlx.Connect("postgres", cfg.PostgresDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })

	recent := handlers.NewRecentUploadsHandler(cfg, db)
	migration := handlers.NewIdentityMigrationHandler(db)
	rescue := handlers.NewIdentityRescueHandler(db)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if id, parseErr := strconv.Atoi(c.GetHeader("X-Test-User")); parseErr == nil && id > 0 {
			c.Set(middleware.CNSUserKey, &middleware.CNSUser{ID: id, Username: "live-user-" + strconv.Itoa(id)})
		}
		c.Next()
	})
	router.POST("/api/me/devices/register", recent.RegisterDevice)
	router.POST("/api/me/devices/recover", recent.RecoverDevice)
	router.GET("/api/me/files/:id/access", recent.FileAccess)
	router.GET("/api/me/identity-migration/legacy-key", migration.LegacyKey)
	router.POST("/api/me/identity-migration/start", migration.Start)
	router.GET("/api/me/identity-migration/escrow", migration.Escrow)
	router.POST("/api/me/identity-migration/adopt", migration.Adopt)
	router.GET("/api/me/identity-migration/files", migration.Files)
	router.POST("/api/me/identity-migration/files", migration.StoreFiles)
	router.GET("/api/me/identity-rescue/locked", rescue.Locked)
	router.POST("/api/me/identity-rescue", rescue.Rescue)
	return &identityTestEnv{t: t, cfg: cfg, db: db, raw: raw, router: router}
}

type testDevice struct {
	id  string
	key *rsa.PrivateKey
}

func (e *identityTestEnv) device(userID int, n int) testDevice {
	e.t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		e.t.Fatal(err)
	}
	return testDevice{id: fmt.Sprintf("00000000-0000-4000-8000-%012d", (userID*10+n)%1000000000000), key: key}
}

// legacyDevice registers a device that holds the account's legacy user key,
// as devices trusted before identity keys did.
func (e *identityTestEnv) legacyDevice(userID int, n int, userKey []byte) testDevice {
	e.t.Helper()
	d := e.device(userID, n)
	ctx := context.Background()
	if err := e.db.CreateOrUpdateUserDevice(ctx, &models.UserDevice{
		ID: d.id, CNSUserID: int64(userID), DeviceLabel: "legacy", PublicKeyJWK: rsaPublicJWK(&d.key.PublicKey),
		KeyAlgorithm: "RSA-OAEP-2048", KeyVersion: 1,
	}); err != nil {
		e.t.Fatal(err)
	}
	wrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &d.key.PublicKey, userKey, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.raw.ExecContext(ctx, `
		INSERT INTO user_key_envelopes (id, cns_user_id, device_id, wrapped_user_key, uk_wrap_alg, uk_wrap_meta, key_version)
		VALUES (gen_random_uuid(), $1, $2, $3, 'RSA-OAEP-2048-v1', '{}'::jsonb, 1)
	`, userID, d.id, wrapped); err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *identityTestEnv) call(userID int, method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	rec := requestAs(e.router, userID, method, path, payload, "application/json")
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (e *identityTestEnv) register(userID int, d testDevice, extra map[string]any, recover bool) map[string]any {
	e.t.Helper()
	body := map[string]any{
		"device_id": d.id, "device_label": "test", "key_algorithm": "RSA-OAEP-2048", "key_version": 1,
		"public_key_jwk": json.RawMessage(rsaPublicJWK(&d.key.PublicKey)),
	}
	for k, v := range extra {
		body[k] = v
	}
	path := "/api/me/devices/register"
	if recover {
		path = "/api/me/devices/recover"
	}
	code, out := e.call(userID, http.MethodPost, path, body)
	if code != http.StatusOK {
		e.t.Fatalf("register status=%d body=%v", code, out)
	}
	return out
}

// newIdentity is a fresh identity keypair plus the registration fields that
// hand a device its self-wrapped copy.
func newIdentity(t *testing.T, d testDevice) (*rsa.PrivateKey, map[string]any) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	wrapped, err := hybridWrap(d.key.PublicKey, der)
	if err != nil {
		t.Fatal(err)
	}
	return key, map[string]any{
		"identity_public_key_jwk":          json.RawMessage(rsaPublicJWK(&key.PublicKey)),
		"identity_key_algorithm":           "RSA-OAEP-2048",
		"wrapped_identity_private_key_b64": base64.StdEncoding.EncodeToString(wrapped),
		"identity_key_wrap_alg":            "RSA-OAEP-2048+AES-GCM-256-v1",
	}
}

func aesGCM(t *testing.T, key []byte) cipher.AEAD {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return gcm
}

func (e *identityTestEnv) legacyFile(userID int, userKey, dek []byte) string {
	e.t.Helper()
	gcm := aesGCM(e.t, userKey)
	nonce := make([]byte, gcm.NonceSize())
	_, _ = rand.Read(nonce)
	now := time.Now()
	fileID := models.GenerateID(17)
	if err := e.db.CreateFileWithEnvelope(context.Background(), &models.File{
		ID: fileID, NumericCode: models.GenerateNumericCode(), OriginalName: "legacy.txt", SizeBytes: 1,
		UploaderIP: "127.0.0.1", OwnerCNSUserID: sqlNullInt(int64(userID)), OwnerCNSUserName: sqlNullString("legacy"),
		ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now,
	}, &models.FileKeyEnvelope{
		FileID: fileID, WrappedDEK: gcm.Seal(nil, nonce, dek, nil), DEKWrapAlg: "AES-GCM-UK-v1", DEKWrapNonce: nonce, DEKWrapVersion: 1,
	}, nil); err != nil {
		e.t.Fatal(err)
	}
	return fileID
}

func (e *identityTestEnv) accessDEK(userID int, fileID string, identity *rsa.PrivateKey) (int, []byte) {
	e.t.Helper()
	code, out := e.call(userID, http.MethodGet, "/api/me/files/"+fileID+"/access", nil)
	if code != http.StatusOK {
		return 0, nil
	}
	envelope := out["file_access_key_envelope"].(map[string]any)
	dek, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, identity, mustBase64(envelope["wrapped_dek_b64"].(string)), nil)
	if err != nil {
		return int(out["identity_key_version"].(float64)), nil
	}
	return int(out["identity_key_version"].(float64)), dek
}

// A legacy account moves onto an identity key: the first legacy device creates
// it and re-wraps the account's files, a second one adopts it via the escrow
// without approval, and devices without the legacy key get nothing.
func TestLiveLegacyAccountMigration(t *testing.T) {
	e := newIdentityTestEnv(t)
	userID := int(time.Now().UnixNano() % 1000000000)
	userKey := make([]byte, 32)
	_, _ = rand.Read(userKey)
	deviceA := e.legacyDevice(userID, 1, userKey)
	deviceB := e.legacyDevice(userID, 2, userKey)
	dek := []byte("legacy-file-passphrase")
	fileID := e.legacyFile(userID, userKey, dek)

	if out := e.register(userID, deviceA, nil, false); out["needs_identity_migration"] != true {
		t.Fatalf("expected needs_identity_migration: %v", out)
	}

	// The device's server-side copy of the user key opens with its device key.
	code, out := e.call(userID, http.MethodGet, "/api/me/identity-migration/legacy-key?device_id="+deviceA.id, nil)
	if code != http.StatusOK {
		t.Fatalf("legacy-key status=%d %v", code, out)
	}
	opened, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, deviceA.key, mustBase64(out["wrapped_uk_b64"].(string)), nil)
	if err != nil || !bytes.Equal(opened, userKey) {
		t.Fatalf("legacy user key did not round-trip: %v", err)
	}

	identity, fields := newIdentity(t, deviceA)
	der, _ := x509.MarshalPKCS8PrivateKey(identity)
	gcm := aesGCM(t, userKey)
	nonce := make([]byte, gcm.NonceSize())
	_, _ = rand.Read(nonce)
	start := map[string]any{
		"device_id":                      deviceA.id,
		"escrow_wrapped_private_key_b64": base64.StdEncoding.EncodeToString(gcm.Seal(nil, nonce, der, nil)),
		"escrow_nonce_b64":               base64.StdEncoding.EncodeToString(nonce),
		"escrow_wrap_alg":                "AES-GCM-UK-v1",
	}
	for k, v := range fields {
		start[k] = v
	}
	if code, out := e.call(userID, http.MethodPost, "/api/me/identity-migration/start", start); code != http.StatusOK {
		t.Fatalf("start status=%d %v", code, out)
	}
	if code, _ := e.call(userID, http.MethodPost, "/api/me/identity-migration/start", start); code != http.StatusConflict {
		t.Fatalf("a second start must be refused, got %d", code)
	}
	if out := e.register(userID, deviceA, nil, false); out["identity_key_envelope"] == nil {
		t.Fatalf("device A should now hold the identity key: %v", out)
	}

	// Re-wrap the legacy file for the identity key.
	code, out = e.call(userID, http.MethodGet, "/api/me/identity-migration/files?device_id="+deviceA.id, nil)
	items, _ := out["items"].([]any)
	if code != http.StatusOK || len(items) != 1 {
		t.Fatalf("expected one file to migrate: status=%d %v", code, out)
	}
	item := items[0].(map[string]any)
	oldDEK, err := gcm.Open(nil, mustBase64(item["dek_wrap_nonce_b64"].(string)), mustBase64(item["wrapped_dek_b64"].(string)), nil)
	if err != nil || !bytes.Equal(oldDEK, dek) {
		t.Fatalf("legacy file key did not open: %v", err)
	}
	rewrapped, _ := rsa.EncryptOAEP(sha256.New(), rand.Reader, &identity.PublicKey, oldDEK, nil)
	store := map[string]any{"device_id": deviceA.id, "identity_key_version": 1, "items": []map[string]any{{
		"file_id": fileID, "identity_wrapped_dek_b64": base64.StdEncoding.EncodeToString(rewrapped), "identity_dek_wrap_alg": "RSA-OAEP-2048-v1",
	}}}
	if code, out := e.call(userID, http.MethodPost, "/api/me/identity-migration/files", store); code != http.StatusOK || out["stored"] != float64(1) {
		t.Fatalf("store status=%d %v", code, out)
	}
	if code, out := e.call(userID, http.MethodGet, "/api/me/identity-migration/files?device_id="+deviceA.id, nil); code != http.StatusOK || len(out["items"].([]any)) != 0 {
		t.Fatalf("migrated file still listed: %v", out)
	}
	if version, got := e.accessDEK(userID, fileID, identity); version != 1 || !bytes.Equal(got, dek) {
		t.Fatalf("migrated file key does not open: version=%d", version)
	}

	// Device B picks the identity key up from the escrow.
	if out := e.register(userID, deviceB, nil, false); out["needs_enrollment"] != true {
		t.Fatalf("device B should not hold the key yet: %v", out)
	}
	code, out = e.call(userID, http.MethodGet, "/api/me/identity-migration/escrow?device_id="+deviceB.id, nil)
	if code != http.StatusOK {
		t.Fatalf("escrow status=%d %v", code, out)
	}
	escrowed, err := gcm.Open(nil, mustBase64(out["wrap_nonce_b64"].(string)), mustBase64(out["wrapped_private_key_b64"].(string)), nil)
	if err != nil || !bytes.Equal(escrowed, der) {
		t.Fatalf("escrow did not open with the user key: %v", err)
	}
	wrappedForB, _ := hybridWrap(deviceB.key.PublicKey, escrowed)
	adopt := map[string]any{"device_id": deviceB.id, "identity_key_version": 1,
		"wrapped_identity_private_key_b64": base64.StdEncoding.EncodeToString(wrappedForB), "identity_key_wrap_alg": "RSA-OAEP-2048+AES-GCM-256-v1"}
	if code, out := e.call(userID, http.MethodPost, "/api/me/identity-migration/adopt", adopt); code != http.StatusOK || out["stored"] != true {
		t.Fatalf("adopt status=%d %v", code, out)
	}
	if out := e.register(userID, deviceB, nil, false); out["identity_key_envelope"] == nil {
		t.Fatalf("device B should now hold the identity key: %v", out)
	}

	// A device that never held the legacy key gets nothing from the migration.
	deviceC := e.device(userID, 3)
	e.register(userID, deviceC, nil, false)
	for _, path := range []string{"/api/me/identity-migration/legacy-key", "/api/me/identity-migration/escrow"} {
		if code, _ := e.call(userID, http.MethodGet, path+"?device_id="+deviceC.id, nil); code != http.StatusNotFound {
			t.Fatalf("%s for a device without the legacy key: status=%d", path, code)
		}
	}
	adopt["device_id"] = deviceC.id
	if code, _ := e.call(userID, http.MethodPost, "/api/me/identity-migration/adopt", adopt); code != http.StatusNotFound {
		t.Fatalf("adopt from a device without the legacy key: status=%d", code)
	}
	store["device_id"] = deviceC.id
	if code, _ := e.call(userID, http.MethodPost, "/api/me/identity-migration/files", store); code != http.StatusForbidden {
		t.Fatalf("storing file keys from an untrusted device: status=%d", code)
	}
}

// After a recovery, a device still holding the old identity key version
// re-wraps the locked file keys for the new version; keys that are no longer
// locked can't be replaced.
func TestLiveRescueAfterRecovery(t *testing.T) {
	e := newIdentityTestEnv(t)
	userID := int(time.Now().UnixNano()%1000000000) + 7
	oldDevice := e.device(userID, 1)
	oldIdentity, fields := newIdentity(t, oldDevice)
	if out := e.register(userID, oldDevice, nil, false); out["needs_identity_setup"] != true {
		t.Fatalf("expected needs_identity_setup: %v", out)
	}
	e.register(userID, oldDevice, fields, false)

	dek := []byte("file-passphrase")
	now := time.Now()
	fileID := models.GenerateID(17)
	wrappedOld, _ := rsa.EncryptOAEP(sha256.New(), rand.Reader, &oldIdentity.PublicKey, dek, nil)
	if err := e.db.CreateFileWithEnvelope(context.Background(), &models.File{
		ID: fileID, NumericCode: models.GenerateNumericCode(), OriginalName: "owned.txt", SizeBytes: 1,
		UploaderIP: "127.0.0.1", OwnerCNSUserID: sqlNullInt(int64(userID)), OwnerCNSUserName: sqlNullString("owner"),
		ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now,
	}, nil, &models.FileAccessKeyEnvelope{
		FileID: fileID, RecipientCNSUserID: int64(userID), WrappedDEK: wrappedOld, DEKWrapAlg: "RSA-OAEP-2048-v1",
		DEKWrapVersion: 1, RecipientKeyVersion: 1, AccessKind: "owner", GrantedAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	newDevice := e.device(userID, 2)
	newIdentityKey, recoverFields := newIdentity(t, newDevice)
	if out := e.register(userID, newDevice, recoverFields, true); out["identity_public_key"].(map[string]any)["key_version"] != float64(2) {
		t.Fatalf("recovery should create version 2: %v", out)
	}
	if version, got := e.accessDEK(userID, fileID, newIdentityKey); version != 1 || got != nil {
		t.Fatalf("file should be locked to version 1 after recovery: version=%d", version)
	}

	code, out := e.call(userID, http.MethodGet, "/api/me/identity-rescue/locked?version=1", nil)
	items, _ := out["items"].([]any)
	if code != http.StatusOK || len(items) != 1 {
		t.Fatalf("expected one locked key: status=%d %v", code, out)
	}
	oldDEK, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, oldIdentity, mustBase64(items[0].(map[string]any)["wrapped_dek_b64"].(string)), nil)
	if err != nil {
		t.Fatal(err)
	}
	rewrapped, _ := rsa.EncryptOAEP(sha256.New(), rand.Reader, &newIdentityKey.PublicKey, oldDEK, nil)
	rescue := map[string]any{"from_version": 1, "to_version": 2, "items": []map[string]any{{
		"file_id": fileID, "identity_wrapped_dek_b64": base64.StdEncoding.EncodeToString(rewrapped), "identity_dek_wrap_alg": "RSA-OAEP-2048-v1",
	}}}
	if code, out := e.call(userID, http.MethodPost, "/api/me/identity-rescue", rescue); code != http.StatusOK || out["rescued"] != float64(1) {
		t.Fatalf("rescue status=%d %v", code, out)
	}
	if version, got := e.accessDEK(userID, fileID, newIdentityKey); version != 2 || !bytes.Equal(got, dek) {
		t.Fatalf("rescued key does not open with version 2: version=%d", version)
	}

	// The key is no longer locked, so a second (bogus) rescue changes nothing.
	garbage, _ := rsa.EncryptOAEP(sha256.New(), rand.Reader, &newIdentityKey.PublicKey, []byte("garbage"), nil)
	rescue["items"] = []map[string]any{{"file_id": fileID, "identity_wrapped_dek_b64": base64.StdEncoding.EncodeToString(garbage), "identity_dek_wrap_alg": "RSA-OAEP-2048-v1"}}
	if code, out := e.call(userID, http.MethodPost, "/api/me/identity-rescue", rescue); code != http.StatusOK || out["rescued"] != float64(0) {
		t.Fatalf("an unlocked key was replaced: status=%d %v", code, out)
	}
	if _, got := e.accessDEK(userID, fileID, newIdentityKey); !bytes.Equal(got, dek) {
		t.Fatal("an unlocked key was overwritten")
	}
	rescue["from_version"] = 2
	if code, _ := e.call(userID, http.MethodPost, "/api/me/identity-rescue", rescue); code != http.StatusBadRequest {
		t.Fatalf("rescue into the same version must be refused, got %d", code)
	}
}
