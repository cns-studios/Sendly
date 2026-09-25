package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"sendly/internal/models"
)

type memoryDeviceStore struct {
	devices           map[string]models.UserDevice
	envelopes         map[string]models.UserKeyEnvelope
	enrollments       map[string]models.DeviceEnrollment
	identityKeys      map[string]models.UserIdentityKey
	identityEnvelopes map[string]models.UserIdentityKeyDeviceEnvelope
}

func newMemoryDeviceStore() *memoryDeviceStore {
	return &memoryDeviceStore{
		devices:           map[string]models.UserDevice{},
		envelopes:         map[string]models.UserKeyEnvelope{},
		enrollments:       map[string]models.DeviceEnrollment{},
		identityKeys:      map[string]models.UserIdentityKey{},
		identityEnvelopes: map[string]models.UserIdentityKeyDeviceEnvelope{},
	}
}
func (m *memoryDeviceStore) CreateOrUpdateUserDevice(_ context.Context, d *models.UserDevice) error {
	m.devices[d.ID] = *d
	return nil
}
func (m *memoryDeviceStore) ResetTrustedDeviceState(_ context.Context, d *models.UserDevice, e *models.UserKeyEnvelope) error {
	for id := range m.devices {
		delete(m.devices, id)
	}
	m.devices[d.ID] = *d
	m.envelopes = map[string]models.UserKeyEnvelope{e.DeviceID: *e}
	return nil
}
func (m *memoryDeviceStore) GetUserKeyEnvelopeForDevice(_ context.Context, _ int64, id string) (*models.UserKeyEnvelope, error) {
	e, ok := m.envelopes[id]
	if !ok {
		return nil, models.ErrFileNotFound
	}
	return &e, nil
}
func (m *memoryDeviceStore) UserHasTrustedKeyEnvelope(_ context.Context, _ int64) (bool, error) {
	return len(m.envelopes) > 0, nil
}
func (m *memoryDeviceStore) SaveUserKeyEnvelope(_ context.Context, e *models.UserKeyEnvelope) error {
	m.envelopes[e.DeviceID] = *e
	return nil
}
func (m *memoryDeviceStore) GetActiveDevicesByUser(_ context.Context, _ int64) ([]models.UserDevice, error) {
	result := make([]models.UserDevice, 0, len(m.devices))
	for _, d := range m.devices {
		result = append(result, d)
	}
	return result, nil
}
func (m *memoryDeviceStore) CreateEnrollmentRequest(_ context.Context, e *models.DeviceEnrollment) error {
	e.ID = "enrollment-1"
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

func (m *memoryDeviceStore) CreateUserIdentityKey(_ context.Context, k *models.UserIdentityKey) error {
	key := fmt.Sprintf("%d:%d", k.CNSUserID, k.KeyVersion)
	m.identityKeys[key] = *k
	return nil
}

func (m *memoryDeviceStore) GetUserIdentityKey(_ context.Context, userID int64, version int) (*models.UserIdentityKey, error) {
	key := fmt.Sprintf("%d:%d", userID, version)
	k, ok := m.identityKeys[key]
	if !ok {
		return nil, models.ErrIdentityKeyNotFound
	}
	return &k, nil
}

func (m *memoryDeviceStore) GetActiveUserIdentityKey(_ context.Context, userID int64) (*models.UserIdentityKey, error) {
	for _, k := range m.identityKeys {
		if k.CNSUserID == userID && k.Status == "active" {
			return &k, nil
		}
	}
	return nil, models.ErrIdentityKeyNotFound
}

func (m *memoryDeviceStore) CreateUserIdentityKeyDeviceEnvelope(_ context.Context, env *models.UserIdentityKeyDeviceEnvelope) error {
	key := fmt.Sprintf("%d:%s:%d", env.CNSUserID, env.DeviceID, env.IdentityKeyVersion)
	m.identityEnvelopes[key] = *env
	return nil
}

func (m *memoryDeviceStore) GetUserIdentityKeyDeviceEnvelope(_ context.Context, userID int64, deviceID string, version int) (*models.UserIdentityKeyDeviceEnvelope, error) {
	key := fmt.Sprintf("%d:%s:%d", userID, deviceID, version)
	env, ok := m.identityEnvelopes[key]
	if !ok {
		return nil, models.ErrDeviceEnvelopeNotFound
	}
	return &env, nil
}

func (m *memoryDeviceStore) DeleteUserIdentityKeyDeviceEnvelopesByUser(_ context.Context, userID int64) error {
	for k, env := range m.identityEnvelopes {
		if env.CNSUserID == userID {
			delete(m.identityEnvelopes, k)
		}
	}
	return nil
}

func (m *memoryDeviceStore) DeleteUserIdentityKeyDeviceEnvelope(_ context.Context, userID int64, deviceID string, version int) error {
	delete(m.identityEnvelopes, fmt.Sprintf("%d:%s:%d", userID, deviceID, version))
	return nil
}

func (m *memoryDeviceStore) ListDevicesMissingIdentityKeyEnvelope(_ context.Context, userID int64, version int) ([]models.UserDevice, error) {
	result := []models.UserDevice{}
	for id, d := range m.devices {
		if _, trusted := m.envelopes[id]; !trusted || d.RevokedAt.Valid {
			continue
		}
		if _, ok := m.identityEnvelopes[fmt.Sprintf("%d:%s:%d", userID, id, version)]; !ok {
			result = append(result, d)
		}
	}
	return result, nil
}

func (m *memoryDeviceStore) UpdateUserIdentityKeyPublicKey(_ context.Context, userID int64, version int, publicKeyJWK json.RawMessage) error {
	key := fmt.Sprintf("%d:%d", userID, version)
	k, ok := m.identityKeys[key]
	if !ok {
		return models.ErrIdentityKeyNotFound
	}
	k.PublicKeyJWK = publicKeyJWK
	m.identityKeys[key] = k
	return nil
}

func deviceRequest(id string, key []byte) models.DeviceRegisterRequest {
	return models.DeviceRegisterRequest{DeviceID: id, DeviceLabel: id, PublicKeyJWK: json.RawMessage(`{"kty":"RSA"}`), KeyAlgorithm: "RSA-OAEP-2048", WrappedUserKeyB64: encode(key), UKWrapAlg: "RSA-OAEP-2048-v1", UKWrapMeta: json.RawMessage(`{}`)}
}

func encode(value []byte) string { return base64.StdEncoding.EncodeToString(value) }

func TestDeviceIdentityLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newMemoryDeviceStore()
	service := &DeviceIdentity{DB: store}

	t.Run("first-device-registration", func(t *testing.T) {
		result, err := service.Register(ctx, 1, deviceRequest("device-1", []byte("wrapped")), false)
		if err != nil || result.NeedsEnrollment || result.UserKeyEnvelope == nil {
			t.Fatalf("unexpected first registration: %#v %v", result, err)
		}
	})
	t.Run("re-registration-of-trusted-device", func(t *testing.T) {
		result, err := service.Register(ctx, 1, deviceRequest("device-1", []byte("wrapped")), false)
		if err != nil || result.NeedsEnrollment {
			t.Fatalf("trusted re-registration failed: %#v %v", result, err)
		}
	})
	t.Run("new-device-requires-approval", func(t *testing.T) {
		result, err := service.Register(ctx, 1, deviceRequest("device-2", []byte("wrapped")), false)
		if err != nil || !result.NeedsEnrollment {
			t.Fatalf("expected approval: %#v %v", result, err)
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
	t.Run("enrollment-approval-with-verification-code", func(t *testing.T) {
		item, _ := store.GetPendingEnrollmentForDevice(ctx, 1, "device-2")
		err := service.Approve(ctx, 1, item.ID, models.ApproveEnrollmentRequest{ApproverDeviceID: "device-1", VerificationCode: item.VerificationCode, WrappedUserKeyB64: "d3JhcHBlZA==", UKWrapAlg: "RSA", UKWrapMeta: json.RawMessage(`{}`)})
		if err != nil || store.enrollments[item.ID].Status != models.EnrollmentStatusApproved {
			t.Fatalf("approval failed: %v", err)
		}
	})
	t.Run("enrollment-rejection", func(t *testing.T) {
		_, _ = service.CreateEnrollment(ctx, 1, "device-3")
		if err := service.Reject(ctx, 1, "enrollment-1", "device-1"); err != nil {
			t.Fatalf("rejection failed: %v", err)
		}
	})
	t.Run("device-recovery-revokes-old-state", func(t *testing.T) {
		result, err := service.Register(ctx, 1, deviceRequest("device-recovered", []byte("wrapped")), true)
		if err != nil || result.UserKeyEnvelope == nil || len(store.devices) != 1 || len(store.envelopes) != 1 {
			t.Fatalf("recovery failed: %#v %v", result, err)
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

// identityRequest is a registration from a device that sends a self-wrapped
// copy of the identity keypair it holds (or just generated), identified by n.
func identityRequest(id, n string) models.DeviceRegisterRequest {
	req := deviceRequest(id, []byte("wrapped"))
	req.IdentityPublicKeyJWK = json.RawMessage(fmt.Sprintf(`{"kty":"RSA","n":%q,"e":"AQAB"}`, n))
	req.WrappedIdentityPrivateKeyB64 = encode([]byte("identity-private-" + n))
	req.IdentityKeyWrapAlg = "RSA-OAEP-2048+AES-GCM-256-v1"
	req.IdentityKeyWrapMeta = json.RawMessage(`{}`)
	return req
}

// Devices that were trusted before identity keys existed each generate their
// own keypair on first login. Only the first one may become the account key;
// the others must get that key from a sibling instead of keeping their own.
func TestIdentityKeyForPreexistingDevices(t *testing.T) {
	ctx := context.Background()
	store := newMemoryDeviceStore()
	service := &DeviceIdentity{DB: store}
	for _, id := range []string{"device-a", "device-b"} {
		store.devices[id] = models.UserDevice{ID: id, CNSUserID: 1, PublicKeyJWK: json.RawMessage(`{"kty":"RSA","n":"dev-` + id + `"}`)}
		store.envelopes[id] = models.UserKeyEnvelope{CNSUserID: 1, DeviceID: id, WrappedUserKey: []byte("uk")}
	}

	t.Run("first-device-creates-the-identity-key", func(t *testing.T) {
		result, err := service.Register(ctx, 1, identityRequest("device-a", "key-a"), false)
		if err != nil || result.IdentityKeyEnvelope == nil {
			t.Fatalf("expected device-a to store its copy: %#v %v", result, err)
		}
		if result.ActiveIdentityKey == nil || !samePublicKey(result.ActiveIdentityKey.PublicKeyJWK, identityRequest("", "key-a").IdentityPublicKeyJWK) {
			t.Fatalf("expected key-a to be the active identity key: %#v", result.ActiveIdentityKey)
		}
		if len(result.DevicesMissingIdentityKey) != 1 || result.DevicesMissingIdentityKey[0].ID != "device-b" {
			t.Fatalf("expected device-b to be listed as missing the key: %#v", result.DevicesMissingIdentityKey)
		}
	})
	t.Run("second-device-with-its-own-key-is-not-stored", func(t *testing.T) {
		result, err := service.Register(ctx, 1, identityRequest("device-b", "key-b"), false)
		if err != nil {
			t.Fatal(err)
		}
		if result.IdentityKeyEnvelope != nil {
			t.Fatalf("mismatched copy was stored: %#v", result.IdentityKeyEnvelope)
		}
		if !samePublicKey(result.ActiveIdentityKey.PublicKeyJWK, identityRequest("", "key-a").IdentityPublicKeyJWK) {
			t.Fatal("active identity key changed")
		}
		if len(result.DevicesMissingIdentityKey) != 0 {
			t.Fatal("a device without the key must not be asked to distribute it")
		}
	})
	t.Run("sender-without-a-copy-cannot-distribute", func(t *testing.T) {
		_, err := service.DistributeIdentityKey(ctx, 1, models.DistributeIdentityKeyRequest{DeviceID: "device-b", Envelopes: []models.DistributedIdentityEnvelope{
			{DeviceID: "device-b", IdentityKeyVersion: 1, WrappedPrivateKeyB64: encode([]byte("x"))},
		}})
		if err != models.ErrIdentityKeyNotHeld {
			t.Fatalf("expected ErrIdentityKeyNotHeld, got %v", err)
		}
	})
	t.Run("sibling-distributes-the-key", func(t *testing.T) {
		stored, err := service.DistributeIdentityKey(ctx, 1, models.DistributeIdentityKeyRequest{DeviceID: "device-a", Envelopes: []models.DistributedIdentityEnvelope{
			{DeviceID: "device-b", IdentityKeyVersion: 1, WrappedPrivateKeyB64: encode([]byte("key-a-for-b"))},
			{DeviceID: "device-a", IdentityKeyVersion: 1, WrappedPrivateKeyB64: encode([]byte("overwrite"))},
		}})
		if err != nil || stored != 1 {
			t.Fatalf("expected exactly one stored copy: %d %v", stored, err)
		}
		if string(store.identityEnvelopes["1:device-a:1"].WrappedPrivateKey) != "identity-private-key-a" {
			t.Fatal("an existing copy was overwritten")
		}
		result, err := service.Register(ctx, 1, identityRequest("device-b", "key-b2"), false)
		if err != nil || result.IdentityKeyEnvelope == nil || string(result.IdentityKeyEnvelope.WrappedPrivateKey) != "key-a-for-b" {
			t.Fatalf("device-b should now receive the distributed copy: %#v %v", result, err)
		}
	})
	t.Run("discarded-copy-is-listed-as-missing-again", func(t *testing.T) {
		req := deviceRequest("device-b", []byte("wrapped"))
		req.DiscardIdentityKeyEnvelope = true
		result, err := service.Register(ctx, 1, req, false)
		if err != nil || result.IdentityKeyEnvelope != nil {
			t.Fatalf("expected the copy to be dropped: %#v %v", result, err)
		}
		result, err = service.Register(ctx, 1, identityRequest("device-a", "key-a"), false)
		if err != nil || len(result.DevicesMissingIdentityKey) != 1 {
			t.Fatalf("expected device-b to be missing the key again: %#v %v", result, err)
		}
	})
}
