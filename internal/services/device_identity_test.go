package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"sendly/internal/models"
)

type memoryDeviceStore struct {
	devices           map[string]models.UserDevice
	legacyUserKeys    map[string]bool
	enrollments       map[string]models.DeviceEnrollment
	identityKeys      map[string]models.UserIdentityKey
	identityEnvelopes map[string]models.UserIdentityKeyDeviceEnvelope
}

func newMemoryDeviceStore() *memoryDeviceStore {
	return &memoryDeviceStore{
		devices:           map[string]models.UserDevice{},
		legacyUserKeys:    map[string]bool{},
		enrollments:       map[string]models.DeviceEnrollment{},
		identityKeys:      map[string]models.UserIdentityKey{},
		identityEnvelopes: map[string]models.UserIdentityKeyDeviceEnvelope{},
	}
}

func envelopeKey(userID int64, deviceID string, version int) string {
	return fmt.Sprintf("%d:%s:%d", userID, deviceID, version)
}

func (m *memoryDeviceStore) CreateOrUpdateUserDevice(_ context.Context, d *models.UserDevice) error {
	m.devices[d.ID] = *d
	return nil
}
func (m *memoryDeviceStore) GetActiveDevicesByUser(_ context.Context, _ int64) ([]models.UserDevice, error) {
	result := make([]models.UserDevice, 0, len(m.devices))
	for _, d := range m.devices {
		if !d.RevokedAt.Valid {
			result = append(result, d)
		}
	}
	return result, nil
}
func (m *memoryDeviceStore) CreateEnrollmentRequest(_ context.Context, e *models.DeviceEnrollment) error {
	e.ID = fmt.Sprintf("enrollment-%d", len(m.enrollments)+1)
	m.enrollments[e.ID] = *e
	return nil
}
func (m *memoryDeviceStore) GetPendingEnrollmentForDevice(_ context.Context, _ int64, id string) (*models.DeviceEnrollment, error) {
	for _, e := range m.enrollments {
		if e.RequestDeviceID == id && e.Status == models.EnrollmentStatusPending && time.Now().Before(e.ExpiresAt) {
			return &e, nil
		}
	}
	return nil, nil
}
func (m *memoryDeviceStore) TouchExpiredEnrollments(_ context.Context, _ int64) error { return nil }
func (m *memoryDeviceStore) ListPendingEnrollments(_ context.Context, _ int64) ([]models.DeviceEnrollment, error) {
	result := []models.DeviceEnrollment{}
	for _, e := range m.enrollments {
		if e.Status == models.EnrollmentStatusPending && time.Now().Before(e.ExpiresAt) {
			result = append(result, e)
		}
	}
	return result, nil
}
func (m *memoryDeviceStore) GetEnrollmentByID(_ context.Context, _ int64, id string) (*models.DeviceEnrollment, error) {
	e, ok := m.enrollments[id]
	if !ok {
		return nil, models.ErrFileNotFound
	}
	return &e, nil
}
func (m *memoryDeviceStore) ApproveEnrollment(_ context.Context, _ int64, id, approver string) error {
	e := m.enrollments[id]
	e.Status = models.EnrollmentStatusApproved
	e.ApprovedByDeviceID.String = approver
	e.ApprovedByDeviceID.Valid = true
	m.enrollments[id] = e
	return nil
}
func (m *memoryDeviceStore) RejectEnrollment(_ context.Context, _ int64, id string) error {
	e := m.enrollments[id]
	e.Status = models.EnrollmentStatusRejected
	m.enrollments[id] = e
	return nil
}

func (m *memoryDeviceStore) GetActiveUserIdentityKey(_ context.Context, userID int64) (*models.UserIdentityKey, error) {
	for _, k := range m.identityKeys {
		if k.CNSUserID == userID && k.Status == "active" {
			return &k, nil
		}
	}
	return nil, models.ErrIdentityKeyNotFound
}
func (m *memoryDeviceStore) CreateIdentityKeyWithDeviceEnvelope(ctx context.Context, k *models.UserIdentityKey, env *models.UserIdentityKeyDeviceEnvelope) error {
	if _, err := m.GetActiveUserIdentityKey(ctx, k.CNSUserID); err == nil {
		return errors.New("duplicate active identity key")
	}
	m.identityKeys[fmt.Sprintf("%d:%d", k.CNSUserID, k.KeyVersion)] = *k
	return m.CreateUserIdentityKeyDeviceEnvelope(ctx, env)
}
func (m *memoryDeviceStore) RecoverIdentityKey(_ context.Context, d *models.UserDevice, k *models.UserIdentityKey, env *models.UserIdentityKeyDeviceEnvelope) error {
	for id, device := range m.devices {
		device.RevokedAt.Valid = true
		m.devices[id] = device
	}
	m.devices[d.ID] = *d
	m.identityEnvelopes = map[string]models.UserIdentityKeyDeviceEnvelope{}
	m.legacyUserKeys = map[string]bool{}
	next := 1
	for key, existing := range m.identityKeys {
		if existing.CNSUserID != d.CNSUserID {
			continue
		}
		if existing.KeyVersion >= next {
			next = existing.KeyVersion + 1
		}
		existing.Status = "retired"
		m.identityKeys[key] = existing
	}
	k.KeyVersion, env.IdentityKeyVersion = next, next
	m.identityKeys[fmt.Sprintf("%d:%d", k.CNSUserID, next)] = *k
	m.identityEnvelopes[envelopeKey(env.CNSUserID, env.DeviceID, next)] = *env
	return nil
}
func (m *memoryDeviceStore) CreateUserIdentityKeyDeviceEnvelope(_ context.Context, env *models.UserIdentityKeyDeviceEnvelope) error {
	m.identityEnvelopes[envelopeKey(env.CNSUserID, env.DeviceID, env.IdentityKeyVersion)] = *env
	return nil
}
func (m *memoryDeviceStore) GetUserIdentityKeyDeviceEnvelope(_ context.Context, userID int64, deviceID string, version int) (*models.UserIdentityKeyDeviceEnvelope, error) {
	env, ok := m.identityEnvelopes[envelopeKey(userID, deviceID, version)]
	if !ok {
		return nil, models.ErrDeviceEnvelopeNotFound
	}
	return &env, nil
}
func (m *memoryDeviceStore) DeviceHoldsIdentityKey(_ context.Context, userID int64, deviceID string, version int) (bool, error) {
	_, ok := m.identityEnvelopes[envelopeKey(userID, deviceID, version)]
	return ok && !m.devices[deviceID].RevokedAt.Valid, nil
}
func (m *memoryDeviceStore) UserHasLegacyUserKey(_ context.Context, _ int64) (bool, error) {
	for deviceID := range m.legacyUserKeys {
		if !m.devices[deviceID].RevokedAt.Valid {
			return true, nil
		}
	}
	return false, nil
}

func deviceRequest(id string) models.DeviceRegisterRequest {
	return models.DeviceRegisterRequest{DeviceID: id, DeviceLabel: id, PublicKeyJWK: json.RawMessage(`{"kty":"RSA","n":"dev-` + id + `"}`), KeyAlgorithm: "RSA-OAEP-2048"}
}

// identityRequest is a registration that also sends a new identity keypair
// (identified by n) with the device's self-wrapped copy of its private key.
func identityRequest(id, n string) models.DeviceRegisterRequest {
	req := deviceRequest(id)
	req.IdentityPublicKeyJWK = json.RawMessage(fmt.Sprintf(`{"kty":"RSA","n":%q,"e":"AQAB"}`, n))
	req.WrappedIdentityPrivateKeyB64 = encode([]byte("identity-private-" + n))
	req.IdentityKeyWrapAlg = "RSA-OAEP-2048+AES-GCM-256-v1"
	req.IdentityKeyWrapMeta = json.RawMessage(`{}`)
	return req
}

func encode(value []byte) string { return base64.StdEncoding.EncodeToString(value) }

// samePublicKey reports whether two RSA public JWKs describe the same key.
func samePublicKey(a, b json.RawMessage) bool {
	var ka, kb struct{ Kty, N, E string }
	if json.Unmarshal(a, &ka) != nil || json.Unmarshal(b, &kb) != nil {
		return false
	}
	return ka.N != "" && ka.Kty == kb.Kty && ka.N == kb.N && ka.E == kb.E
}

func approval(approver, code string, version int) models.ApproveEnrollmentRequest {
	return models.ApproveEnrollmentRequest{
		ApproverDeviceID: approver, VerificationCode: code, IdentityKeyVersion: version,
		WrappedIdentityPrivateKeyB64: encode([]byte("identity-for-requester")), IdentityKeyWrapAlg: "RSA-OAEP-2048+AES-GCM-256-v1",
	}
}

func TestDeviceIdentityLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newMemoryDeviceStore()
	service := &DeviceIdentity{DB: store}

	t.Run("new-account-asks-for-an-identity-key", func(t *testing.T) {
		result, err := service.Register(ctx, 1, deviceRequest("device-1"), false)
		if err != nil || !result.NeedsIdentitySetup || result.IdentityKeyEnvelope != nil {
			t.Fatalf("expected needs_identity_setup: %#v %v", result, err)
		}
	})
	t.Run("first-device-creates-the-identity-key", func(t *testing.T) {
		result, err := service.Register(ctx, 1, identityRequest("device-1", "key-1"), false)
		if err != nil || result.IdentityKeyEnvelope == nil || result.ActiveIdentityKey == nil || result.ActiveIdentityKey.KeyVersion != 1 {
			t.Fatalf("unexpected first registration: %#v %v", result, err)
		}
	})
	t.Run("re-registration-of-trusted-device", func(t *testing.T) {
		result, err := service.Register(ctx, 1, deviceRequest("device-1"), false)
		if err != nil || result.NeedsEnrollment || result.IdentityKeyEnvelope == nil {
			t.Fatalf("trusted re-registration failed: %#v %v", result, err)
		}
	})
	t.Run("new-device-requires-approval-and-cannot-replace-the-key", func(t *testing.T) {
		result, err := service.Register(ctx, 1, identityRequest("device-2", "key-2"), false)
		if err != nil || !result.NeedsEnrollment || result.IdentityKeyEnvelope != nil {
			t.Fatalf("expected approval: %#v %v", result, err)
		}
		if !samePublicKey(result.ActiveIdentityKey.PublicKeyJWK, identityRequest("", "key-1").IdentityPublicKeyJWK) {
			t.Fatal("active identity key changed")
		}
	})
	t.Run("enrollment-request-creation-and-deduplication", func(t *testing.T) {
		item, err := service.CreateEnrollment(ctx, 1, "device-2")
		if err != nil || item == nil {
			t.Fatalf("create enrollment: %#v %v", item, err)
		}
		again, err := service.CreateEnrollment(ctx, 1, "device-2")
		if err != nil || again.ID != item.ID {
			t.Fatalf("dedupe failed: %#v %v", again, err)
		}
	})
	t.Run("approval-for-a-stale-identity-key-version-is-refused", func(t *testing.T) {
		item, _ := store.GetPendingEnrollmentForDevice(ctx, 1, "device-2")
		if err := service.Approve(ctx, 1, item.ID, approval("device-1", item.VerificationCode, 7)); err != models.ErrIdentityKeyStale {
			t.Fatalf("expected ErrIdentityKeyStale, got %v", err)
		}
	})
	t.Run("untrusted-device-cannot-approve", func(t *testing.T) {
		item, _ := store.GetPendingEnrollmentForDevice(ctx, 1, "device-2")
		if err := service.Approve(ctx, 1, item.ID, approval("device-2", item.VerificationCode, 1)); err != models.ErrApproverNotTrusted {
			t.Fatalf("expected ErrApproverNotTrusted, got %v", err)
		}
	})
	t.Run("approval-hands-over-the-identity-key", func(t *testing.T) {
		item, _ := store.GetPendingEnrollmentForDevice(ctx, 1, "device-2")
		if err := service.Approve(ctx, 1, item.ID, approval("device-1", item.VerificationCode, 1)); err != nil {
			t.Fatalf("approval failed: %v", err)
		}
		if store.enrollments[item.ID].Status != models.EnrollmentStatusApproved {
			t.Fatal("enrollment not approved")
		}
		if trusted, _ := service.IsTrusted(ctx, 1, "device-2"); !trusted {
			t.Fatal("approved device is not trusted")
		}
		result, err := service.Register(ctx, 1, deviceRequest("device-2"), false)
		if err != nil || result.IdentityKeyEnvelope == nil || string(result.IdentityKeyEnvelope.WrappedPrivateKey) != "identity-for-requester" {
			t.Fatalf("device-2 should receive its copy: %#v %v", result, err)
		}
	})
	t.Run("enrollment-rejection", func(t *testing.T) {
		if _, err := service.Register(ctx, 1, deviceRequest("device-3"), false); err != nil {
			t.Fatal(err)
		}
		item, err := service.CreateEnrollment(ctx, 1, "device-3")
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Reject(ctx, 1, item.ID, "device-1"); err != nil {
			t.Fatalf("rejection failed: %v", err)
		}
	})
	t.Run("recovery-needs-a-new-identity-key", func(t *testing.T) {
		if _, err := service.Register(ctx, 1, deviceRequest("device-recovered"), true); err != models.ErrIdentityKeyRequired {
			t.Fatalf("expected ErrIdentityKeyRequired, got %v", err)
		}
	})
	t.Run("recovery-creates-the-next-version-and-revokes-old-devices", func(t *testing.T) {
		result, err := service.Register(ctx, 1, identityRequest("device-recovered", "key-recovered"), true)
		if err != nil || result.ActiveIdentityKey.KeyVersion != 2 || result.IdentityKeyEnvelope.IdentityKeyVersion != 2 {
			t.Fatalf("recovery failed: %#v %v", result, err)
		}
		if store.identityKeys["1:1"].Status != "retired" {
			t.Fatal("old identity key version is still active")
		}
		for _, id := range []string{"device-1", "device-2"} {
			if trusted, _ := service.IsTrusted(ctx, 1, id); trusted {
				t.Fatalf("%s is still trusted after recovery", id)
			}
		}
		again, err := service.Register(ctx, 1, deviceRequest("device-1"), false)
		if err != nil || !again.NeedsEnrollment {
			t.Fatalf("old device must be approved again: %#v %v", again, err)
		}
	})
	t.Run("expired-enrollment-is-not-listed", func(t *testing.T) {
		store.enrollments["expired"] = models.DeviceEnrollment{ID: "expired", Status: models.EnrollmentStatusPending, ExpiresAt: time.Now().Add(-time.Minute)}
		items, err := service.ListPending(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.Enrollment.ID == "expired" {
				t.Fatal("expired enrollment listed")
			}
		}
	})
}

// An account from before identity keys must be migrated by a device holding
// its legacy user key; no device may create the identity key on its own.
func TestLegacyAccountNeedsMigration(t *testing.T) {
	ctx := context.Background()
	store := newMemoryDeviceStore()
	service := &DeviceIdentity{DB: store}
	store.devices["legacy-device"] = models.UserDevice{ID: "legacy-device", CNSUserID: 1}
	store.legacyUserKeys["legacy-device"] = true

	for _, id := range []string{"legacy-device", "new-device"} {
		result, err := service.Register(ctx, 1, identityRequest(id, "key-"+id), false)
		if err != nil || !result.NeedsIdentityMigration || result.IdentityKeyEnvelope != nil {
			t.Fatalf("%s: expected needs_identity_migration: %#v %v", id, result, err)
		}
	}
	if len(store.identityKeys) != 0 {
		t.Fatal("an identity key was created for a legacy account")
	}
}

// Two devices of a brand-new account registering at once: only one creates
// the identity key; the other has to be approved.
func TestConcurrentFirstDevices(t *testing.T) {
	ctx := context.Background()
	store := newMemoryDeviceStore()
	service := &DeviceIdentity{DB: store}
	store.identityKeys["1:1"] = models.UserIdentityKey{CNSUserID: 1, KeyVersion: 1, Status: "active", PublicKeyJWK: json.RawMessage(`{"kty":"RSA","n":"winner","e":"AQAB"}`)}
	// The first device's key appeared between this device's lookup and its
	// insert; simulate by calling the insert path directly.
	err := store.CreateIdentityKeyWithDeviceEnvelope(ctx, &models.UserIdentityKey{CNSUserID: 1, KeyVersion: 1, Status: "active"}, &models.UserIdentityKeyDeviceEnvelope{CNSUserID: 1, DeviceID: "loser"})
	if err == nil {
		t.Fatal("a second active identity key was accepted")
	}
	result, err := service.Register(ctx, 1, identityRequest("loser", "loser-key"), false)
	if err != nil || !result.NeedsEnrollment {
		t.Fatalf("expected the second device to need approval: %#v %v", result, err)
	}
}
