package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"sendly/internal/models"
)

type memoryDeviceStore struct {
	devices     map[string]models.UserDevice
	envelopes   map[string]models.UserKeyEnvelope
	enrollments map[string]models.DeviceEnrollment
}

func newMemoryDeviceStore() *memoryDeviceStore {
	return &memoryDeviceStore{
		devices: map[string]models.UserDevice{}, envelopes: map[string]models.UserKeyEnvelope{},
		enrollments: map[string]models.DeviceEnrollment{},
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
