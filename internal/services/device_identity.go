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
	ResetTrustedDeviceState(context.Context, *models.UserDevice, *models.UserKeyEnvelope) error
	GetUserKeyEnvelopeForDevice(context.Context, int64, string) (*models.UserKeyEnvelope, error)
	UserHasTrustedKeyEnvelope(context.Context, int64) (bool, error)
	SaveUserKeyEnvelope(context.Context, *models.UserKeyEnvelope) error
	GetActiveDevicesByUser(context.Context, int64) ([]models.UserDevice, error)
	CreateEnrollmentRequest(context.Context, *models.DeviceEnrollment) error
	GetPendingEnrollmentForDevice(context.Context, int64, string) (*models.DeviceEnrollment, error)
	TouchExpiredEnrollments(context.Context, int64) error
	ListPendingEnrollments(context.Context, int64) ([]models.DeviceEnrollment, error)
	GetEnrollmentByID(context.Context, int64, string) (*models.DeviceEnrollment, error)
	ApproveEnrollment(context.Context, int64, string, string) error
	RejectEnrollment(context.Context, int64, string) error

	// Identity keypair methods
	CreateUserIdentityKey(context.Context, *models.UserIdentityKey) error
	GetUserIdentityKey(context.Context, int64, int) (*models.UserIdentityKey, error)
	GetActiveUserIdentityKey(context.Context, int64) (*models.UserIdentityKey, error)
	CreateUserIdentityKeyDeviceEnvelope(context.Context, *models.UserIdentityKeyDeviceEnvelope) error
	GetUserIdentityKeyDeviceEnvelope(context.Context, int64, string, int) (*models.UserIdentityKeyDeviceEnvelope, error)
	DeleteUserIdentityKeyDeviceEnvelopesByUser(context.Context, int64) error
	UpdateUserIdentityKeyPublicKey(context.Context, int64, int, json.RawMessage) error
}

type DeviceRegistrationResult struct {
	DeviceID            string
	NeedsEnrollment     bool
	UserKeyEnvelope     *models.UserKeyEnvelope
	IdentityKeyEnvelope *models.UserIdentityKeyDeviceEnvelope
}

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
		if req.WrappedUserKeyB64 == "" {
			return nil, models.ErrWrappedUserKeyRequired
		}
		wrapped, err := base64.StdEncoding.DecodeString(req.WrappedUserKeyB64)
		if err != nil {
			return nil, fmt.Errorf("invalid wrapped user key: %w", err)
		}
		envelope := &models.UserKeyEnvelope{
			CNSUserID: userID, DeviceID: req.DeviceID, WrappedUserKey: wrapped,
			UKWrapAlg: req.UKWrapAlg, UKWrapMeta: req.UKWrapMeta, KeyVersion: version,
		}
		if err := s.DB.ResetTrustedDeviceState(ctx, device, envelope); err != nil {
			return nil, err
		}

		var identityEnvelope *models.UserIdentityKeyDeviceEnvelope
		if req.WrappedIdentityPrivateKeyB64 != "" {
			wrappedPriv, err := base64.StdEncoding.DecodeString(req.WrappedIdentityPrivateKeyB64)
			if err != nil {
				return nil, fmt.Errorf("invalid wrapped identity private key: %w", err)
			}
			idVersion := req.IdentityKeyVersion
			if idVersion <= 0 {
				idVersion = 1
			}

			if len(req.IdentityPublicKeyJWK) > 0 {
				idAlg := req.IdentityKeyAlgorithm
				if idAlg == "" {
					idAlg = "RSA-OAEP-2048"
				}
				now := time.Now()
				existingKey, err := s.DB.GetUserIdentityKey(ctx, userID, idVersion)
				if errors.Is(err, models.ErrIdentityKeyNotFound) {
					idKey := &models.UserIdentityKey{
						CNSUserID:    userID,
						KeyVersion:   idVersion,
						PublicKeyJWK: req.IdentityPublicKeyJWK,
						KeyAlgorithm: idAlg,
						Status:       "active",
						CreatedAt:    now,
						ActivatedAt:  sql.NullTime{Time: now, Valid: true},
					}
					if err := s.DB.CreateUserIdentityKey(ctx, idKey); err != nil {
						return nil, fmt.Errorf("failed to create user identity key on recover: %w", err)
					}
				} else if err != nil {
					return nil, err
				} else if existingKey != nil {
					if err := s.DB.UpdateUserIdentityKeyPublicKey(ctx, userID, idVersion, req.IdentityPublicKeyJWK); err != nil {
						return nil, fmt.Errorf("failed to update user identity key on recover: %w", err)
					}
				}
			}

			identityEnvelope = &models.UserIdentityKeyDeviceEnvelope{
				CNSUserID:          userID,
				DeviceID:           req.DeviceID,
				IdentityKeyVersion: idVersion,
				WrappedPrivateKey:  wrappedPriv,
				WrapAlg:            req.IdentityKeyWrapAlg,
				WrapMeta:           req.IdentityKeyWrapMeta,
				CreatedAt:          time.Now(),
			}
			if err := s.DB.CreateUserIdentityKeyDeviceEnvelope(ctx, identityEnvelope); err != nil {
				return nil, fmt.Errorf("failed to create identity device envelope on recover: %w", err)
			}
		}

		return &DeviceRegistrationResult{
			DeviceID:            req.DeviceID,
			UserKeyEnvelope:     envelope,
			IdentityKeyEnvelope: identityEnvelope,
		}, nil
	}

	if err := s.DB.CreateOrUpdateUserDevice(ctx, device); err != nil {
		return nil, err
	}
	if existing, err := s.DB.GetUserKeyEnvelopeForDevice(ctx, userID, req.DeviceID); err == nil {
		result := &DeviceRegistrationResult{DeviceID: req.DeviceID, UserKeyEnvelope: existing}
		idEnv, err := s.DB.GetUserIdentityKeyDeviceEnvelope(ctx, userID, req.DeviceID, 1)
		if err == nil {
			result.IdentityKeyEnvelope = idEnv
		} else if errors.Is(err, models.ErrDeviceEnvelopeNotFound) && req.WrappedIdentityPrivateKeyB64 != "" {
			wrappedPriv, err := base64.StdEncoding.DecodeString(req.WrappedIdentityPrivateKeyB64)
			if err != nil {
				return nil, fmt.Errorf("invalid wrapped identity private key: %w", err)
			}
			idVersion := req.IdentityKeyVersion
			if idVersion <= 0 {
				idVersion = 1
			}
			if len(req.IdentityPublicKeyJWK) == 0 {
				return nil, models.ErrIdentityKeyNotFound
			}
			if _, keyErr := s.DB.GetUserIdentityKey(ctx, userID, idVersion); errors.Is(keyErr, models.ErrIdentityKeyNotFound) {
				idAlg := req.IdentityKeyAlgorithm
				if idAlg == "" {
					idAlg = "RSA-OAEP-2048"
				}
				now := time.Now()
				if err := s.DB.CreateUserIdentityKey(ctx, &models.UserIdentityKey{
					CNSUserID: userID, KeyVersion: idVersion, PublicKeyJWK: req.IdentityPublicKeyJWK,
					KeyAlgorithm: idAlg, Status: "active", CreatedAt: now,
					ActivatedAt: sql.NullTime{Time: now, Valid: true},
				}); err != nil {
					return nil, fmt.Errorf("failed to create user identity key: %w", err)
				}
			} else if keyErr != nil {
				return nil, keyErr
			}
			newEnv := &models.UserIdentityKeyDeviceEnvelope{
				CNSUserID: userID, DeviceID: req.DeviceID, IdentityKeyVersion: idVersion,
				WrappedPrivateKey: wrappedPriv, WrapAlg: req.IdentityKeyWrapAlg,
				WrapMeta: req.IdentityKeyWrapMeta, CreatedAt: time.Now(),
			}
			if err := s.DB.CreateUserIdentityKeyDeviceEnvelope(ctx, newEnv); err != nil {
				return nil, fmt.Errorf("failed to create identity device envelope: %w", err)
			}
			result.IdentityKeyEnvelope = newEnv
		}
		return result, nil
	}
	trusted, err := s.DB.UserHasTrustedKeyEnvelope(ctx, userID)
	if err != nil {
		return nil, err
	}
	if trusted {
		return &DeviceRegistrationResult{DeviceID: req.DeviceID, NeedsEnrollment: true}, nil
	}
	envelope, err := decodeUserKeyEnvelope(userID, req, version)
	if err != nil {
		return nil, err
	}
	if err := s.DB.SaveUserKeyEnvelope(ctx, envelope); err != nil {
		return nil, err
	}

	var identityEnvelope *models.UserIdentityKeyDeviceEnvelope
	if req.WrappedIdentityPrivateKeyB64 != "" {
		wrappedPriv, err := base64.StdEncoding.DecodeString(req.WrappedIdentityPrivateKeyB64)
		if err != nil {
			return nil, fmt.Errorf("invalid wrapped identity private key: %w", err)
		}
		idVersion := req.IdentityKeyVersion
		if idVersion <= 0 {
			idVersion = 1
		}
		if len(req.IdentityPublicKeyJWK) > 0 {
			idAlg := req.IdentityKeyAlgorithm
			if idAlg == "" {
				idAlg = "RSA-OAEP-2048"
			}
			now := time.Now()
			_, keyErr := s.DB.GetUserIdentityKey(ctx, userID, idVersion)
			if errors.Is(keyErr, models.ErrIdentityKeyNotFound) {
				idKey := &models.UserIdentityKey{
					CNSUserID:    userID,
					KeyVersion:   idVersion,
					PublicKeyJWK: req.IdentityPublicKeyJWK,
					KeyAlgorithm: idAlg,
					Status:       "active",
					CreatedAt:    now,
					ActivatedAt:  sql.NullTime{Time: now, Valid: true},
				}
				if err := s.DB.CreateUserIdentityKey(ctx, idKey); err != nil {
					return nil, fmt.Errorf("failed to create user identity key: %w", err)
				}
			} else if keyErr != nil {
				return nil, keyErr
			}
		}

		identityEnvelope = &models.UserIdentityKeyDeviceEnvelope{
			CNSUserID:          userID,
			DeviceID:           req.DeviceID,
			IdentityKeyVersion: idVersion,
			WrappedPrivateKey:  wrappedPriv,
			WrapAlg:            req.IdentityKeyWrapAlg,
			WrapMeta:           req.IdentityKeyWrapMeta,
			CreatedAt:          time.Now(),
		}
		if err := s.DB.CreateUserIdentityKeyDeviceEnvelope(ctx, identityEnvelope); err != nil {
			return nil, fmt.Errorf("failed to create identity device envelope: %w", err)
		}
	}

	return &DeviceRegistrationResult{
		DeviceID:            req.DeviceID,
		UserKeyEnvelope:     envelope,
		IdentityKeyEnvelope: identityEnvelope,
	}, nil
}

func (s *DeviceIdentity) IsTrusted(ctx context.Context, userID int64, deviceID string) (bool, error) {
	_, err := s.DB.GetUserKeyEnvelopeForDevice(ctx, userID, deviceID)
	if err != nil {
		return false, nil
	}
	return true, nil
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
	wrapped, err := base64.StdEncoding.DecodeString(req.WrappedUserKeyB64)
	if err != nil {
		return err
	}
	if err := s.DB.SaveUserKeyEnvelope(ctx, &models.UserKeyEnvelope{
		CNSUserID: userID, DeviceID: enrollment.RequestDeviceID, WrappedUserKey: wrapped,
		UKWrapAlg: req.UKWrapAlg, UKWrapMeta: req.UKWrapMeta, KeyVersion: 1,
	}); err != nil {
		return err
	}

	if req.WrappedIdentityPrivateKeyB64 != "" {
		wrappedPriv, err := base64.StdEncoding.DecodeString(req.WrappedIdentityPrivateKeyB64)
		if err != nil {
			return fmt.Errorf("invalid wrapped identity private key: %w", err)
		}
		idVersion := req.IdentityKeyVersion
		if idVersion <= 0 {
			idVersion = 1
		}
		if _, err := s.DB.GetUserIdentityKey(ctx, userID, idVersion); err != nil {
			return fmt.Errorf("identity key version %d is unavailable: %w", idVersion, err)
		}
		idEnvelope := &models.UserIdentityKeyDeviceEnvelope{
			CNSUserID:          userID,
			DeviceID:           enrollment.RequestDeviceID,
			IdentityKeyVersion: idVersion,
			WrappedPrivateKey:  wrappedPriv,
			WrapAlg:            req.IdentityKeyWrapAlg,
			WrapMeta:           req.IdentityKeyWrapMeta,
			CreatedAt:          time.Now(),
		}
		if err := s.DB.CreateUserIdentityKeyDeviceEnvelope(ctx, idEnvelope); err != nil {
			return err
		}
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

func decodeUserKeyEnvelope(userID int64, req models.DeviceRegisterRequest, version int) (*models.UserKeyEnvelope, error) {
	if req.WrappedUserKeyB64 == "" {
		return nil, models.ErrWrappedUserKeyRequired
	}
	wrapped, err := base64.StdEncoding.DecodeString(req.WrappedUserKeyB64)
	if err != nil {
		return nil, err
	}
	return &models.UserKeyEnvelope{
		CNSUserID: userID, DeviceID: req.DeviceID, WrappedUserKey: wrapped,
		UKWrapAlg: req.UKWrapAlg, UKWrapMeta: req.UKWrapMeta, KeyVersion: version,
	}, nil
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
