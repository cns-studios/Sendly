package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"sendly/internal/middleware"
	"sendly/internal/models"
	"sendly/internal/storage"

	"github.com/gin-gonic/gin"
)

// Quick share callers are authenticated as one of:
//   - the host: the initiating CNS user, or for a guest host the X-Host-Token
//     issued when the tunnel was started;
//   - a participant: a signed-in caller by CNS user; an anonymous caller by
//     X-Device-ID plus the X-Participant-Token issued when it joined.
//
// Device IDs are visible to other participants and never prove anything on
// their own.

const (
	headerHostToken        = "X-Host-Token"
	headerParticipantToken = "X-Participant-Token"
	headerDeviceID         = "X-Device-ID"
)

type tunnelCaller struct {
	tunnel      *models.Tunnel
	isHost      bool
	participant *models.TunnelParticipant
}

// ownsDevice reports whether deviceID is the caller's own device in this
// tunnel (their participant row's device, or the host's initiator device).
func (tc *tunnelCaller) ownsDevice(deviceID string) bool {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return false
	}
	if tc.participant != nil && tc.participant.DeviceID.Valid && strings.EqualFold(tc.participant.DeviceID.String, deviceID) {
		return true
	}
	return tc.isHost && tc.tunnel.InitiatorDeviceID.Valid && strings.EqualFold(tc.tunnel.InitiatorDeviceID.String, deviceID)
}

// approved reports whether the host has admitted the caller (the host always
// is). Only approved callers see files or receive key material.
func (tc *tunnelCaller) approved() bool {
	return tc.isHost || (tc.participant != nil && tc.participant.ApprovedAt.Valid)
}

// tunnelPeerApproved reports whether the tunnel peer a file key would be
// wrapped for has been approved by the host.
func tunnelPeerApproved(c *gin.Context, db *storage.Postgres, tunnel *models.Tunnel, peerUserID int64, peerDeviceID string) (bool, error) {
	if peerUserID == tunnel.InitiatorCNSUserID {
		return true, nil
	}
	participant, err := db.FindTunnelParticipant(c.Request.Context(), tunnel.ID, peerUserID, peerDeviceID, "")
	if err != nil {
		return false, err
	}
	return participant != nil && participant.ApprovedAt.Valid, nil
}

func isTunnelHost(c *gin.Context, tunnel *models.Tunnel) bool {
	if tunnel.InitiatorCNSUserID != 0 {
		user := middleware.GetCNSUser(c)
		return user != nil && int64(user.ID) == tunnel.InitiatorCNSUserID
	}

	hostToken := c.GetHeader(headerHostToken)
	return tunnel.HostToken != "" && hostToken != "" &&
		subtle.ConstantTimeCompare([]byte(tunnel.HostToken), []byte(hostToken)) == 1
}

// authorizeTunnelCaller returns the authenticated caller, or nil if the caller
// is neither the host nor a participant of the tunnel.
func authorizeTunnelCaller(c *gin.Context, db *storage.Postgres, tunnel *models.Tunnel) (*tunnelCaller, error) {
	caller := &tunnelCaller{tunnel: tunnel, isHost: isTunnelHost(c, tunnel)}

	var userID int64
	if user := middleware.GetCNSUser(c); user != nil {
		userID = int64(user.ID)
	}
	participant, err := db.FindTunnelParticipant(c.Request.Context(), tunnel.ID, userID,
		c.GetHeader(headerDeviceID), hashParticipantToken(c.GetHeader(headerParticipantToken)))
	if err != nil {
		return nil, err
	}
	caller.participant = participant

	if !caller.isHost && caller.participant == nil {
		return nil, nil
	}
	return caller, nil
}

func newParticipantToken() (token, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token = hex.EncodeToString(raw)
	return token, hashParticipantToken(token), nil
}

func hashParticipantToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
