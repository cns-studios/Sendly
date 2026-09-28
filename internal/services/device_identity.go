package services

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"sendly/internal/models"
)

// DeviceIdentity centralizes registration, recovery, trust, and enrollment
// state transitions shared by the web and Android APIs.
type DeviceIdentity struct {
	DB DeviceStore
}

type DeviceStore interface {
	CreateOrUpdateUserDevice(context.Context, *models.UserDevice) error
	GetActiveDevicesByUser(context.Context, int64) ([]models.UserDevice, error)
	CreateEnrollmentRequest(context.Context, *models.DeviceEnrollment) error
	GetPendingEnrollmentForDevice(context.Context, int64, string) (*models.DeviceEnrollment, error)
	TouchExpiredEnrollments(context.Context, int64) error
	ListPendingEnrollments(context.Context, int64) ([]models.DeviceEnrollment, error)
	GetEnrollmentByID(context.Context, int64, string) (*models.DeviceEnrollment, error)
	ApproveEnrollment(context.Context, int64, string, string) error
	RejectEnrollment(context.Context, int64, string) error

	GetActiveUserIdentityKey(context.Context, int64) (*models.UserIdentityKey, error)
	CreateIdentityKeyWithDeviceEnvelope(context.Context, *models.UserIdentityKey, *models.UserIdentityKeyDeviceEnvelope) error
	RecoverIdentityKey(context.Context, *models.UserDevice, *models.UserIdentityKey, *models.UserIdentityKeyDeviceEnvelope) error
	CreateUserIdentityKeyDeviceEnvelope(context.Context, *models.UserIdentityKeyDeviceEnvelope) error
	GetUserIdentityKeyDeviceEnvelope(context.Context, int64, string, int) (*models.UserIdentityKeyDeviceEnvelope, error)
	DeviceHoldsIdentityKey(context.Context, int64, string, int) (bool, error)
	UserHasLegacyUserKey(context.Context, int64) (bool, error)
}

// DeviceRegistrationResult is where a device stands after registering: a
// trusted device gets its copy of the identity key, any other device learns
// what has to happen first (see models.DeviceRegisterResponse).
type DeviceRegistrationResult struct {
	DeviceID               string
	IdentityKeyEnvelope    *models.UserIdentityKeyDeviceEnvelope
	ActiveIdentityKey      *models.UserIdentityKey
	NeedsEnrollment        bool
	NeedsIdentitySetup     bool
	NeedsIdentityMigration bool
}

// Register records a device and reports whether it holds the account's
// identity key. The identity key is the account's only root secret: it is
// created by a brand-new account's first device, handed to further devices
// through enrollment approval, and replaced by a new version on recovery.
func (s *DeviceIdentity) Register(ctx context.Context, userID int64, req models.DeviceRegisterRequest, recover bool) (*DeviceRegistrationResult, error) {
	key, err := normalizePublicKey(req.PublicKeyJWK)
	if err != nil {
		return nil, fmt.Errorf("invalid device public key: %w", err)
	}
	version := req.KeyVersion
	if version <= 0 {
		version = 1
	}
	device := &models.UserDevice{
		ID: req.DeviceID, CNSUserID: userID, DeviceLabel: req.DeviceLabel,
		PublicKeyJWK: key, KeyAlgorithm: req.KeyAlgorithm, KeyVersion: version,
	}

	if recover {
		return s.recover(ctx, userID, device, req)
	}

	if err := s.DB.CreateOrUpdateUserDevice(ctx, device); err != nil {
		return nil, err
	}
	result := &DeviceRegistrationResult{DeviceID: req.DeviceID}

	active, err := s.DB.GetActiveUserIdentityKey(ctx, userID)
	switch {
	case err == nil:
		result.ActiveIdentityKey = active
		envelope, err := s.DB.GetUserIdentityKeyDeviceEnvelope(ctx, userID, req.DeviceID, active.KeyVersion)
		if errors.Is(err, models.ErrDeviceEnvelopeNotFound) {
			result.NeedsEnrollment = true
			return result, nil
		}
		if err != nil {
			return nil, err
		}
		result.IdentityKeyEnvelope = envelope
		return result, nil
	case !errors.Is(err, models.ErrIdentityKeyNotFound):
		return nil, err
	}

	// No identity key yet. An account from before identity keys has to be
	// migrated by a device holding its legacy user key: letting any new
	// device create the key would hand the account to whoever registers first.
	legacy, err := s.DB.UserHasLegacyUserKey(ctx, userID)
	if err != nil {
		return nil, err
	}
	if legacy {
		result.NeedsIdentityMigration = true
		return result, nil
	}

	// A brand-new account: this device creates the identity key.
	idKey, envelope, err := newIdentityKey(userID, req)
	if errors.Is(err, models.ErrIdentityKeyRequired) {
		result.NeedsIdentitySetup = true
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	idKey.KeyVersion, envelope.IdentityKeyVersion = 1, 1
	if err := s.DB.CreateIdentityKeyWithDeviceEnvelope(ctx, idKey, envelope); err != nil {
		// Another device of this account created it first; this one needs
		// that key, so it has to be approved like any other new device.
		if active, getErr := s.DB.GetActiveUserIdentityKey(ctx, userID); getErr == nil {
			result.ActiveIdentityKey = active
			result.NeedsEnrollment = true
			return result, nil
		}
		return nil, err
	}
	result.ActiveIdentityKey = idKey
	result.IdentityKeyEnvelope = envelope
	return result, nil
}

// recover makes device the account's only trusted device, with a new identity
// key version. Every other device is revoked, and files and transfers wrapped
// for older versions stay locked until a device still holding such a version
// re-wraps them.
func (s *DeviceIdentity) recover(ctx context.Context, userID int64, device *models.UserDevice, req models.DeviceRegisterRequest) (*DeviceRegistrationResult, error) {
	idKey, envelope, err := newIdentityKey(userID, req)
	if err != nil {
		return nil, err
	}
	if err := s.DB.RecoverIdentityKey(ctx, device, idKey, envelope); err != nil {
		return nil, err
	}
	return &DeviceRegistrationResult{
		DeviceID:            device.ID,
		ActiveIdentityKey:   idKey,
		IdentityKeyEnvelope: envelope,
	}, nil
}

// newIdentityKey builds a new identity key and the registering device's
// self-wrapped copy of it from the request; the version is set by the caller.
func newIdentityKey(userID int64, req models.DeviceRegisterRequest) (*models.UserIdentityKey, *models.UserIdentityKeyDeviceEnvelope, error) {
	if req.WrappedIdentityPrivateKeyB64 == "" || len(req.IdentityPublicKeyJWK) == 0 {
		return nil, nil, models.ErrIdentityKeyRequired
	}
	publicKey, err := normalizePublicKey(req.IdentityPublicKeyJWK)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid identity public key: %w", err)
	}
	wrapped, err := base64.StdEncoding.DecodeString(req.WrappedIdentityPrivateKeyB64)
	if err != nil || len(wrapped) == 0 {
		return nil, nil, fmt.Errorf("invalid wrapped identity private key")
	}
	algorithm := req.IdentityKeyAlgorithm
	if algorithm == "" {
		algorithm = "RSA-OAEP-2048"
	}
	now := time.Now()
	idKey := &models.UserIdentityKey{
		CNSUserID: userID, PublicKeyJWK: publicKey, KeyAlgorithm: algorithm,
		Status: "active", CreatedAt: now, ActivatedAt: sql.NullTime{Time: now, Valid: true},
	}
	envelope := &models.UserIdentityKeyDeviceEnvelope{
		CNSUserID: userID, DeviceID: req.DeviceID, WrappedPrivateKey: wrapped,
		WrapAlg: req.IdentityKeyWrapAlg, WrapMeta: req.IdentityKeyWrapMeta, CreatedAt: now,
	}
	return idKey, envelope, nil
}

// IsTrusted reports whether a device holds the account's active identity key.
func (s *DeviceIdentity) IsTrusted(ctx context.Context, userID int64, deviceID string) (bool, error) {
	active, err := s.DB.GetActiveUserIdentityKey(ctx, userID)
	if errors.Is(err, models.ErrIdentityKeyNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return s.DB.DeviceHoldsIdentityKey(ctx, userID, deviceID, active.KeyVersion)
}

func (s *DeviceIdentity) CreateEnrollment(ctx context.Context, userID int64, requestDeviceID string) (*models.DeviceEnrollment, error) {
	owned, err := s.ownsDevice(ctx, userID, requestDeviceID)
	if err != nil {
		return nil, err
	}
	if !owned {
		return nil, models.ErrDeviceNotAuthorized
	}
	existing, err := s.DB.GetPendingEnrollmentForDevice(ctx, userID, requestDeviceID)
	if err != nil || existing != nil {
		return existing, err
	}
	item := &models.DeviceEnrollment{
		CNSUserID: userID, RequestDeviceID: requestDeviceID,
		VerificationCode: verificationCode(6), Status: models.EnrollmentStatusPending,
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}
	if err := s.DB.CreateEnrollmentRequest(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *DeviceIdentity) ListPending(ctx context.Context, userID int64) ([]models.PendingEnrollmentItem, error) {
	_ = s.DB.TouchExpiredEnrollments(ctx, userID)
	items, err := s.DB.ListPendingEnrollments(ctx, userID)
	if err != nil {
		return nil, err
	}
	devices, err := s.DB.GetActiveDevicesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]models.UserDevice, len(devices))
	for _, device := range devices {
		if normalized, normalizeErr := normalizePublicKey(device.PublicKeyJWK); normalizeErr == nil {
			device.PublicKeyJWK = normalized
		}
		byID[device.ID] = device
	}
	result := make([]models.PendingEnrollmentItem, 0, len(items))
	for _, item := range items {
		device := byID[item.RequestDeviceID]
		if device.ID == "" {
			device.ID = item.RequestDeviceID
		}
		result = append(result, models.PendingEnrollmentItem{Enrollment: item, RequestDevice: device})
	}
	return result, nil
}

func (s *DeviceIdentity) Approve(ctx context.Context, userID int64, enrollmentID string, req models.ApproveEnrollmentRequest) error {
	owned, err := s.ownsDevice(ctx, userID, req.ApproverDeviceID)
	if err != nil {
		return err
	}
	if !owned {
		return models.ErrDeviceNotAuthorized
	}
	if trusted, _ := s.IsTrusted(ctx, userID, req.ApproverDeviceID); !trusted {
		return models.ErrApproverNotTrusted
	}
	enrollment, err := s.DB.GetEnrollmentByID(ctx, userID, enrollmentID)
	if err != nil {
		return err
	}
	if enrollment.Status != models.EnrollmentStatusPending || time.Now().After(enrollment.ExpiresAt) {
		return models.ErrEnrollmentNotPending
	}
	if !strings.EqualFold(strings.TrimSpace(req.VerificationCode), strings.TrimSpace(enrollment.VerificationCode)) {
		return models.ErrVerificationCodeMismatch
	}
	active, err := s.DB.GetActiveUserIdentityKey(ctx, userID)
	if err != nil {
		return err
	}
	if req.IdentityKeyVersion != active.KeyVersion {
		return models.ErrIdentityKeyStale
	}
	wrapped, err := base64.StdEncoding.DecodeString(req.WrappedIdentityPrivateKeyB64)
	if err != nil || len(wrapped) == 0 {
		return fmt.Errorf("invalid wrapped identity private key")
	}
	if err := s.DB.CreateUserIdentityKeyDeviceEnvelope(ctx, &models.UserIdentityKeyDeviceEnvelope{
		CNSUserID: userID, DeviceID: enrollment.RequestDeviceID, IdentityKeyVersion: active.KeyVersion,
		WrappedPrivateKey: wrapped, WrapAlg: req.IdentityKeyWrapAlg, WrapMeta: req.IdentityKeyWrapMeta,
		CreatedAt: time.Now(),
	}); err != nil {
		return err
	}

	return s.DB.ApproveEnrollment(ctx, userID, enrollmentID, req.ApproverDeviceID)
}

func (s *DeviceIdentity) Reject(ctx context.Context, userID int64, enrollmentID string, approverDeviceID string) error {
	owned, err := s.ownsDevice(ctx, userID, approverDeviceID)
	if err != nil {
		return err
	}
	if !owned {
		return models.ErrDeviceNotAuthorized
	}
	if trusted, _ := s.IsTrusted(ctx, userID, approverDeviceID); !trusted {
		return models.ErrApproverNotTrusted
	}
	return s.DB.RejectEnrollment(ctx, userID, enrollmentID)
}

func (s *DeviceIdentity) ownsDevice(ctx context.Context, userID int64, deviceID string) (bool, error) {
	devices, err := s.DB.GetActiveDevicesByUser(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, device := range devices {
		if device.ID == deviceID {
			return true, nil
		}
	}
	return false, nil
}

func verificationCode(length int) string {
	const digits = "0123456789"
	result := make([]byte, length)
	for i := range result {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(digits))))
		if err != nil {
			result[i] = digits[0]
			continue
		}
		result[i] = digits[n.Int64()]
	}
	return string(result)
}

func normalizePublicKey(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("public_key_jwk is required")
	}
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("public_key_jwk must be valid JSON: %w", err)
	}
	switch value := parsed.(type) {
	case map[string]any:
		return json.Marshal(value)
	case string:
		inner := strings.TrimSpace(value)
		if inner == "" {
			return nil, fmt.Errorf("public_key_jwk string is empty")
		}
		var object map[string]any
		if err := json.Unmarshal([]byte(inner), &object); err != nil {
			return nil, fmt.Errorf("public_key_jwk string must encode a JSON object: %w", err)
		}
		return json.Marshal(object)
	default:
		return nil, fmt.Errorf("public_key_jwk must be a JSON object")
	}
}
