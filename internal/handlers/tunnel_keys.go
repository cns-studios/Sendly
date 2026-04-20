package handlers

import (
	"encoding/base64"
	"fmt"
	"strings"

	"shareit/internal/models"
)

func resolveTunnelPeerRecipient(tunnel *models.Tunnel, actorUserID int64) (int64, string) {
	if tunnel == nil || actorUserID == 0 {
		return 0, ""
	}

	if tunnel.InitiatorCNSUserID == actorUserID {
		if tunnel.PeerCNSUserID.Valid && tunnel.PeerCNSUserID.Int64 != 0 && tunnel.PeerCNSUserID.Int64 != actorUserID && tunnel.PeerDeviceID.Valid {
			return tunnel.PeerCNSUserID.Int64, strings.TrimSpace(tunnel.PeerDeviceID.String)
		}
		return 0, ""
	}

	if tunnel.PeerCNSUserID.Valid && tunnel.PeerCNSUserID.Int64 == actorUserID {
		if tunnel.InitiatorCNSUserID != 0 && tunnel.InitiatorCNSUserID != actorUserID && tunnel.InitiatorDeviceID.Valid {
			return tunnel.InitiatorCNSUserID, strings.TrimSpace(tunnel.InitiatorDeviceID.String)
		}
	}

	return 0, ""
}

func buildRecipientEnvelopeFromRequest(fileID string, recipientUserID int64, recipientDeviceID, wrappedB64, alg, nonceB64 string, version int) (models.FileRecipientKeyEnvelope, error) {
	if recipientUserID == 0 || strings.TrimSpace(recipientDeviceID) == "" {
		return models.FileRecipientKeyEnvelope{}, fmt.Errorf("recipient identity is incomplete")
	}
	if strings.TrimSpace(wrappedB64) == "" {
		return models.FileRecipientKeyEnvelope{}, fmt.Errorf("missing wrapped recipient key")
	}

	wrapped, err := base64.StdEncoding.DecodeString(wrappedB64)
	if err != nil {
		return models.FileRecipientKeyEnvelope{}, fmt.Errorf("invalid wrapped recipient key")
	}

	var nonce []byte
	if strings.TrimSpace(nonceB64) != "" {
		nonce, err = base64.StdEncoding.DecodeString(nonceB64)
		if err != nil {
			return models.FileRecipientKeyEnvelope{}, fmt.Errorf("invalid recipient wrap nonce")
		}
	}

	if version <= 0 {
		version = 1
	}
	alg = strings.TrimSpace(alg)
	if alg == "" {
		alg = "RSA-OAEP-2048-v1"
	}

	return models.FileRecipientKeyEnvelope{
		FileID:             fileID,
		RecipientCNSUserID: recipientUserID,
		RecipientDeviceID:  recipientDeviceID,
		WrappedDEK:         wrapped,
		DEKWrapAlg:         alg,
		DEKWrapNonce:       nonce,
		DEKWrapVersion:     version,
	}, nil
}
