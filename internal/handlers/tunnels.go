package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"sendly/internal/config"
	"sendly/internal/middleware"
	"sendly/internal/models"
	"sendly/internal/storage"

	"github.com/gin-gonic/gin"
)

type TunnelHandler struct {
	cfg *config.Config
	db  *storage.Postgres
	fs  *storage.Filesystem
}

func NewTunnelHandler(cfg *config.Config, db *storage.Postgres, fs *storage.Filesystem) *TunnelHandler {
	return &TunnelHandler{cfg: cfg, db: db, fs: fs}
}

func (h *TunnelHandler) Start(c *gin.Context) {
	var req models.TunnelStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return
	}

	dur, err := models.ParseTunnelDuration(req.Duration)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid tunnel duration", Code: models.ErrInvalidDuration.Code})
		return
	}

	code, err := h.generateUniqueTunnelCode(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to generate tunnel code", Code: "TUNNEL_CODE_FAILED"})
		return
	}

	var initiatorUserID int64
	user := middleware.GetCNSUser(c)
	if user != nil {
		initiatorUserID = int64(user.ID)
	}

	tunnel := &models.Tunnel{
		Code:               code,
		InitiatorCNSUserID: initiatorUserID,
		InitiatorDeviceID:  nullableDeviceID(req.DeviceID),
		DurationMinutes:    int(dur.Minutes()),
		ExpiresAt:          time.Now().Add(dur),
	}

	var hostToken string
	if initiatorUserID == 0 {
		// A guest host is identified only by this token; never create a
		// guest tunnel without one.
		tokenBytes := make([]byte, 32)
		if _, err := rand.Read(tokenBytes); err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to create tunnel", Code: "TUNNEL_CREATE_FAILED"})
			return
		}
		hostToken = hex.EncodeToString(tokenBytes)
		tunnel.HostToken = hostToken
	}

	if err := h.db.CreateTunnel(c.Request.Context(), tunnel); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to create tunnel", Code: "TUNNEL_CREATE_FAILED"})
		return
	}

	_ = h.db.AddTunnelParticipant(c.Request.Context(), tunnel.ID, initiatorUserID, req.DeviceID)

	participants, _ := h.db.GetTunnelParticipants(c.Request.Context(), tunnel.ID)

	c.JSON(http.StatusOK, models.TunnelStartResponse{
		Tunnel:       *tunnel,
		QRPayload:    h.buildQRPayload(tunnel),
		Participants: participants,
		HostToken:    hostToken,
	})
}

func (h *TunnelHandler) Join(c *gin.Context) {
	var req models.TunnelJoinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return
	}

	tunnel, err := h.db.GetTunnelByCode(c.Request.Context(), req.Code)
	if err != nil {
		status := http.StatusBadRequest
		if err == models.ErrFileExpired {
			status = http.StatusGone
		}
		c.JSON(status, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return
	}

	if tunnel.Status == models.TunnelStatusEnded || tunnel.Status == models.TunnelStatusExpired {
		c.JSON(http.StatusGone, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return
	}
	if tunnel.Status == models.TunnelStatusActive {
		c.JSON(http.StatusGone, models.ErrorResponse{Error: "Tunnel is already active and no longer accepting new members", Code: "TUNNEL_ALREADY_ACTIVE"})
		return
	}

	var peerUserID int64
	user := middleware.GetCNSUser(c)
	if user != nil {
		peerUserID = int64(user.ID)
	}

	join := models.TunnelJoin{
		UserID:             peerUserID,
		DeviceID:           req.DeviceID,
		PresentedTokenHash: hashParticipantToken(c.GetHeader(headerParticipantToken)),
		PublicKeyJWK:       req.PublicKeyJWK,
		KeyAlgorithm:       req.KeyAlgorithm,
		KeyVersion:         req.KeyVersion,
	}
	var participantToken string
	if peerUserID == 0 {
		token, tokenHash, tokenErr := newParticipantToken()
		if tokenErr != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to join tunnel", Code: "TUNNEL_JOIN_FAILED"})
			return
		}
		participantToken, join.NewTokenHash = token, tokenHash
	}

	joined, issued, joinErr := h.db.JoinTunnel(c.Request.Context(), tunnel.ID, join)
	if joinErr != nil {
		if joinErr == models.ErrParticipantConflict {
			c.JSON(http.StatusConflict, models.ErrorResponse{Error: models.ErrParticipantConflict.Message, Code: models.ErrParticipantConflict.Code})
			return
		}
		if joinErr == models.ErrGuestDeviceRequired {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: models.ErrGuestDeviceRequired.Message, Code: models.ErrGuestDeviceRequired.Code})
			return
		}
		if joinErr == models.ErrFileExpired {
			c.JSON(http.StatusGone, models.ErrorResponse{Error: "Tunnel expired", Code: "TUNNEL_EXPIRED"})
			return
		}
		if joinErr == models.ErrFileNotFound {
			c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to join tunnel", Code: "TUNNEL_JOIN_FAILED"})
		return
	}

	participants, _ := h.db.GetTunnelParticipants(c.Request.Context(), tunnel.ID)

	resp := models.TunnelStartResponse{
		Tunnel:       *joined,
		QRPayload:    h.buildQRPayload(joined),
		Participants: participants,
	}
	if issued {
		resp.ParticipantToken = participantToken
	}
	c.JSON(http.StatusOK, resp)
}

func (h *TunnelHandler) Confirm(c *gin.Context) {
	tunnelID := c.Param("id")
	if tunnelID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing tunnel id", Code: "INVALID_REQUEST"})
		return
	}

	var req models.TunnelConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return
	}

	current, err := h.db.GetTunnelByID(c.Request.Context(), tunnelID)
	if err != nil {
		status := http.StatusBadRequest
		if err == models.ErrFileExpired {
			status = http.StatusGone
		}
		c.JSON(status, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return
	}

	var userID int64
	user := middleware.GetCNSUser(c)
	if user != nil {
		userID = int64(user.ID)
	}

	// Guests confirm for a device ID; it has to be their own (the host's
	// initiator device for the host, their participant device otherwise).
	if userID == 0 {
		caller, authErr := authorizeTunnelCaller(c, h.db, current)
		if authErr != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to confirm tunnel", Code: "TUNNEL_CONFIRM_FAILED"})
			return
		}
		if caller == nil || !caller.ownsDevice(req.DeviceID) {
			c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Not a participant of this tunnel", Code: "TUNNEL_FORBIDDEN"})
			return
		}
	}

	tunnel, err := h.db.ConfirmTunnel(c.Request.Context(), tunnelID, userID, req.DeviceID)
	if err != nil {
		if err == models.ErrFileNotFound {
			c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
			return
		}
		if err == models.ErrFileExpired {
			c.JSON(http.StatusGone, models.ErrorResponse{Error: "Tunnel expired", Code: "TUNNEL_EXPIRED"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to confirm tunnel", Code: "TUNNEL_CONFIRM_FAILED"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"tunnel": tunnel})
}

func (h *TunnelHandler) End(c *gin.Context) {
	tunnelID := c.Param("id")
	if tunnelID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing tunnel id", Code: "INVALID_REQUEST"})
		return
	}

	tunnel, err := h.db.GetTunnelByID(c.Request.Context(), tunnelID)
	if err != nil && (tunnel == nil || err != models.ErrFileExpired) {
		status := http.StatusBadRequest
		if err == models.ErrFileNotFound {
			status = http.StatusNotFound
		}
		c.JSON(status, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return
	}
	caller, ok := h.requireTunnelCaller(c, tunnel)
	if !ok {
		return
	}

	user := middleware.GetCNSUser(c)
	var userID int64
	if user != nil {
		userID = int64(user.ID)
	}

	// Callers can only remove themselves: a guest by its own device.
	deviceID := ""
	if userID == 0 {
		switch {
		case caller.participant != nil && caller.participant.DeviceID.Valid:
			deviceID = caller.participant.DeviceID.String
		case caller.isHost && tunnel.InitiatorDeviceID.Valid:
			deviceID = tunnel.InitiatorDeviceID.String
		default:
			c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Not a participant of this tunnel", Code: "TUNNEL_FORBIDDEN"})
			return
		}
	}

	if err := h.db.RemoveTunnelParticipant(c.Request.Context(), tunnelID, userID, deviceID); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to leave tunnel", Code: "TUNNEL_LEAVE_FAILED"})
		return
	}

	count, countErr := h.db.CountTunnelParticipants(c.Request.Context(), tunnelID)
	if countErr != nil {
		// Never treat a failed count as "empty": that would delete the files.
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to leave tunnel", Code: "TUNNEL_LEAVE_FAILED"})
		return
	}
	if count == 0 {
		fileIDs, _ := h.db.GetTunnelFileIDs(c.Request.Context(), tunnelID)
		for _, fileID := range fileIDs {
			_ = h.fs.DeleteFile(fileID)
		}
		_ = h.db.DeleteTunnel(c.Request.Context(), tunnelID)
		c.JSON(http.StatusOK, gin.H{"success": true, "tunnel_ended": true})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "tunnel_ended": false, "remaining": count})
}

func (h *TunnelHandler) Get(c *gin.Context) {
	tunnelID := c.Param("id")
	if tunnelID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing tunnel id", Code: "INVALID_REQUEST"})
		return
	}

	tunnel, err := h.db.GetTunnelByID(c.Request.Context(), tunnelID)
	if err != nil {
		status := http.StatusBadRequest
		if err == models.ErrFileExpired {
			status = http.StatusGone
		}
		c.JSON(status, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return
	}
	caller, ok := h.requireTunnelCaller(c, tunnel)
	if !ok {
		return
	}

	// Joiners waiting for the host's approval see the lobby, not the files.
	files := []models.TunnelFileListItem{}
	if caller.approved() {
		var err error
		files, err = h.db.GetTunnelFiles(c.Request.Context(), tunnelID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to load tunnel files", Code: "TUNNEL_FILES_FAILED"})
			return
		}
	}

	participants, _ := h.db.GetTunnelParticipants(c.Request.Context(), tunnelID)

	c.JSON(http.StatusOK, gin.H{
		"tunnel":       tunnel,
		"files":        files,
		"participants": participants,
	})
}

func (h *TunnelHandler) Files(c *gin.Context) {
	tunnelID := c.Param("id")
	if tunnelID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing tunnel id", Code: "INVALID_REQUEST"})
		return
	}
	if caller, ok := h.loadTunnelForCaller(c, tunnelID); !ok || !h.requireApproved(c, caller) {
		return
	}

	files, err := h.db.GetTunnelFiles(c.Request.Context(), tunnelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to load tunnel files", Code: "TUNNEL_FILES_FAILED"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": files})
}

func (h *TunnelHandler) Participants(c *gin.Context) {
	tunnelID := c.Param("id")
	if tunnelID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing tunnel id", Code: "INVALID_REQUEST"})
		return
	}
	if _, ok := h.loadTunnelForCaller(c, tunnelID); !ok {
		return
	}

	participants, err := h.db.GetTunnelParticipants(c.Request.Context(), tunnelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to load participants", Code: "TUNNEL_PARTICIPANTS_FAILED"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": participants})
}

func (h *TunnelHandler) PeerWrapKey(c *gin.Context) {
	tunnelID := c.Param("id")
	if tunnelID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing tunnel id", Code: "INVALID_REQUEST"})
		return
	}

	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	tunnel, err := h.db.GetTunnelByID(c.Request.Context(), tunnelID)
	if err != nil {
		status := http.StatusBadRequest
		if err == models.ErrFileExpired {
			status = http.StatusGone
		}
		c.JSON(status, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return
	}

	if tunnel.Status != models.TunnelStatusActive {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Tunnel is not active", Code: "TUNNEL_NOT_ACTIVE"})
		return
	}

	peerUserID, peerDeviceID := resolveTunnelPeerRecipient(tunnel, int64(user.ID))
	if peerUserID == 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Peer key sharing is only needed for cross-account tunnels", Code: "PEER_KEY_NOT_REQUIRED"})
		return
	}

	if strings.TrimSpace(peerDeviceID) == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Peer device is not ready for key sharing", Code: "PEER_DEVICE_NOT_READY"})
		return
	}
	if approved, err := tunnelPeerApproved(c, h.db, tunnel, peerUserID, peerDeviceID); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to inspect peer device", Code: "PEER_DEVICE_LOOKUP_FAILED"})
		return
	} else if !approved {
		c.JSON(http.StatusConflict, models.ErrorResponse{Error: models.ErrParticipantNotApproved.Message, Code: models.ErrParticipantNotApproved.Code})
		return
	}

	devices, err := h.db.GetActiveDevicesByUser(c.Request.Context(), peerUserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to inspect peer device", Code: "PEER_DEVICE_LOOKUP_FAILED"})
		return
	}

	for _, device := range devices {
		if strings.EqualFold(device.ID, peerDeviceID) {
			c.JSON(http.StatusOK, models.TunnelPeerWrapKeyResponse{
				PeerCNSUserID: peerUserID,
				PeerDeviceID:  peerDeviceID,
				PublicKeyJWK:  device.PublicKeyJWK,
				KeyAlgorithm:  device.KeyAlgorithm,
				KeyVersion:    device.KeyVersion,
			})
			return
		}
	}

	c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "Peer device is not trusted", Code: "PEER_DEVICE_NOT_TRUSTED"})
}

func (h *TunnelHandler) GuestFileAccess(c *gin.Context) {
	tunnelID := c.Param("id")
	fileID := c.Param("file_id")
	if tunnelID == "" || fileID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "tunnel id and file_id are required", Code: "INVALID_REQUEST"})
		return
	}

	tunnel, err := h.db.GetTunnelByID(c.Request.Context(), tunnelID)
	if err != nil {
		status := http.StatusBadRequest
		if err == models.ErrFileExpired {
			status = http.StatusGone
		}
		c.JSON(status, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return
	}

	if tunnel.Status != models.TunnelStatusActive && tunnel.Status != models.TunnelStatusPending {
		c.JSON(http.StatusGone, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return
	}
	if caller, ok := h.requireTunnelCaller(c, tunnel); !ok || !h.requireApproved(c, caller) {
		return
	}

	file, fileEnvelope, err := h.db.GetTunnelFileWithEnvelope(c.Request.Context(), tunnelID, fileID)
	if err != nil {
		status := http.StatusInternalServerError
		if err == models.ErrFileNotFound || err == models.ErrFileExpired || err == models.ErrFileDeleted {
			status = http.StatusNotFound
		}
		c.JSON(status, models.ErrorResponse{Error: "File not found in tunnel", Code: "FILE_NOT_FOUND"})
		return
	}

	resp := models.FileAccessResponse{
		File: *file.ToMetadata(),
		FileKeyEnvelope: models.FileKeyEnvelopeResponse{
			WrappedDEKB64:   base64.StdEncoding.EncodeToString(fileEnvelope.WrappedDEK),
			DEKWrapAlg:      fileEnvelope.DEKWrapAlg,
			DEKWrapVersion:  fileEnvelope.DEKWrapVersion,
			DEKWrapNonceB64: base64.StdEncoding.EncodeToString(fileEnvelope.DEKWrapNonce),
		},
	}

	c.JSON(http.StatusOK, resp)
}



func (h *TunnelHandler) GetParticipantPublicKeys(c *gin.Context) {
	tunnelID := c.Param("id")
	if tunnelID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing tunnel id", Code: "INVALID_REQUEST"})
		return
	}

	tunnel, err := h.db.GetTunnelByID(c.Request.Context(), tunnelID)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Tunnel not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return
	}

	
	isHost := h.callerIsHost(c, tunnel)
	if !isHost {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Only the tunnel host can list participant keys", Code: "FORBIDDEN"})
		return
	}

	participants, err := h.db.GetParticipantsWithPublicKeys(c.Request.Context(), tunnelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to fetch participants", Code: "PARTICIPANTS_FETCH_FAILED"})
		return
	}

	result := make([]models.TunnelParticipantPublicKey, 0, len(participants))
	for _, p := range participants {
		if len(p.PublicKeyJWK) == 0 || !p.DeviceID.Valid {
			continue
		}
		
		if h.participantIsHost(p, tunnel) {
			continue
		}
		hasEnv, _ := h.db.ParticipantHasEnvelope(c.Request.Context(), tunnelID, p.DeviceID.String)
		result = append(result, models.TunnelParticipantPublicKey{
			ParticipantID: p.ID,
			DeviceID:      p.DeviceID.String,
			PublicKeyJWK:  p.PublicKeyJWK,
			KeyAlgorithm:  p.KeyAlgorithm.String,
			KeyVersion:    int(p.KeyVersion.Int32),
			Approved:      p.Approved,
			HasEnvelope:   hasEnv,
		})
	}

	c.JSON(http.StatusOK, gin.H{"participants": result})
}



func (h *TunnelHandler) PushParticipantEnvelope(c *gin.Context) {
	tunnelID := c.Param("id")
	if tunnelID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Missing tunnel id", Code: "INVALID_REQUEST"})
		return
	}

	var req models.TunnelPushEnvelopeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_REQUEST", Details: err.Error()})
		return
	}

	tunnel, err := h.db.GetTunnelByID(c.Request.Context(), tunnelID)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Tunnel not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return
	}
	if tunnel.Status != models.TunnelStatusActive {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Tunnel is not active", Code: "TUNNEL_NOT_ACTIVE"})
		return
	}

	if !h.callerIsHost(c, tunnel) {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Only the tunnel host can push envelopes", Code: "FORBIDDEN"})
		return
	}

	// The session key only goes to participants the host approved.
	target, err := h.db.GetTunnelParticipantByDevice(c.Request.Context(), tunnelID, req.ParticipantDeviceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to save envelope", Code: "ENVELOPE_SAVE_FAILED"})
		return
	}
	if target == nil || !target.Approved {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: models.ErrParticipantNotApproved.Message, Code: models.ErrParticipantNotApproved.Code})
		return
	}

	wrappedDEK, err := base64.StdEncoding.DecodeString(req.WrappedDEKB64)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid wrapped DEK", Code: "INVALID_WRAPPED_DEK"})
		return
	}

	var nonce []byte
	if req.DEKWrapNonceB64 != "" {
		nonce, err = base64.StdEncoding.DecodeString(req.DEKWrapNonceB64)
		if err != nil {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid nonce", Code: "INVALID_NONCE"})
			return
		}
	}

	version := req.DEKWrapVersion
	if version == 0 {
		version = 1
	}

	if err := h.db.SaveTunnelParticipantEnvelope(
		c.Request.Context(),
		tunnelID,
		req.ParticipantDeviceID,
		wrappedDEK,
		nonce,
		req.DEKWrapAlg,
		version,
	); err != nil {
		if err == models.ErrEnvelopeExists {
			c.JSON(http.StatusConflict, models.ErrorResponse{Error: err.(*models.AppError).Message, Code: models.ErrEnvelopeExists.Code})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to save envelope", Code: "ENVELOPE_SAVE_FAILED"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}



func (h *TunnelHandler) GetParticipantEnvelope(c *gin.Context) {
	tunnelID := c.Param("id")
	deviceID := c.Param("device_id")
	if tunnelID == "" || deviceID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "tunnel_id and device_id required", Code: "INVALID_REQUEST"})
		return
	}

	tunnel, err := h.db.GetTunnelByID(c.Request.Context(), tunnelID)
	if err != nil {
		c.JSON(http.StatusGone, models.ErrorResponse{Error: "Tunnel not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return
	}
	if tunnel.Status == models.TunnelStatusEnded || tunnel.Status == models.TunnelStatusExpired {
		c.JSON(http.StatusGone, models.ErrorResponse{Error: "Tunnel not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return
	}
	// Only the participant the envelope was wrapped for may fetch it.
	caller, ok := h.requireTunnelCaller(c, tunnel)
	if !ok {
		return
	}
	if !caller.ownsDevice(deviceID) {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Not your device", Code: "TUNNEL_FORBIDDEN"})
		return
	}

	envelope, err := h.db.GetTunnelParticipantEnvelope(c.Request.Context(), tunnelID, deviceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to fetch envelope", Code: "ENVELOPE_FETCH_FAILED"})
		return
	}
	if envelope == nil {
		
		c.JSON(http.StatusAccepted, gin.H{"ready": false})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ready": true, "envelope": envelope})
}





func (h *TunnelHandler) callerIsHost(c *gin.Context, tunnel *models.Tunnel) bool {
	return isTunnelHost(c, tunnel)
}

// requireTunnelCaller authenticates the caller as the tunnel's host or one of
// its participants, writing a 403 (or 500) response when that fails.
func (h *TunnelHandler) requireTunnelCaller(c *gin.Context, tunnel *models.Tunnel) (*tunnelCaller, bool) {
	caller, err := authorizeTunnelCaller(c, h.db, tunnel)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to authorize tunnel access", Code: "TUNNEL_AUTH_FAILED"})
		return nil, false
	}
	if caller == nil {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Not a participant of this tunnel", Code: "TUNNEL_FORBIDDEN"})
		return nil, false
	}
	return caller, true
}

// requireApproved writes a 403 unless the host has approved the caller.
func (h *TunnelHandler) requireApproved(c *gin.Context, caller *tunnelCaller) bool {
	if caller.approved() {
		return true
	}
	c.JSON(http.StatusForbidden, models.ErrorResponse{Error: models.ErrParticipantNotApproved.Message, Code: models.ErrParticipantNotApproved.Code})
	return false
}

// ApproveParticipant lets the host admit a joiner; only approved participants
// receive the session key or get file keys wrapped for them.
func (h *TunnelHandler) ApproveParticipant(c *gin.Context) {
	tunnel, participantID, ok := h.loadTunnelForHost(c)
	if !ok {
		return
	}
	if err := h.db.ApproveTunnelParticipant(c.Request.Context(), tunnel.ID, participantID); err != nil {
		if err == models.ErrFileNotFound {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "Participant not found", Code: "PARTICIPANT_NOT_FOUND"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to approve participant", Code: "PARTICIPANT_APPROVE_FAILED"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RejectParticipant lets the host remove a joiner (and any key envelope or
// peer assignment it had).
func (h *TunnelHandler) RejectParticipant(c *gin.Context) {
	tunnel, participantID, ok := h.loadTunnelForHost(c)
	if !ok {
		return
	}
	participants, err := h.db.GetTunnelParticipants(c.Request.Context(), tunnel.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to remove participant", Code: "PARTICIPANT_REJECT_FAILED"})
		return
	}
	for _, p := range participants {
		if p.ID == participantID && h.participantIsHost(p, tunnel) {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "The host cannot be removed", Code: "INVALID_REQUEST"})
			return
		}
	}
	if _, err := h.db.RejectTunnelParticipant(c.Request.Context(), tunnel.ID, participantID); err != nil {
		if err == models.ErrFileNotFound {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "Participant not found", Code: "PARTICIPANT_NOT_FOUND"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Failed to remove participant", Code: "PARTICIPANT_REJECT_FAILED"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *TunnelHandler) loadTunnelForHost(c *gin.Context) (*models.Tunnel, string, bool) {
	tunnelID := c.Param("id")
	participantID := strings.TrimSpace(c.Param("participant_id"))
	if tunnelID == "" || participantID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "tunnel id and participant id are required", Code: "INVALID_REQUEST"})
		return nil, "", false
	}
	tunnel, err := h.db.GetTunnelByID(c.Request.Context(), tunnelID)
	if err != nil {
		status := http.StatusBadRequest
		if err == models.ErrFileExpired {
			status = http.StatusGone
		}
		c.JSON(status, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return nil, "", false
	}
	if !h.callerIsHost(c, tunnel) {
		c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "Only the tunnel host can manage participants", Code: "FORBIDDEN"})
		return nil, "", false
	}
	return tunnel, participantID, true
}

// loadTunnelForCaller loads an available tunnel and authenticates the caller.
func (h *TunnelHandler) loadTunnelForCaller(c *gin.Context, tunnelID string) (*tunnelCaller, bool) {
	tunnel, err := h.db.GetTunnelByID(c.Request.Context(), tunnelID)
	if err != nil {
		status := http.StatusBadRequest
		if err == models.ErrFileExpired {
			status = http.StatusGone
		}
		c.JSON(status, models.ErrorResponse{Error: "Tunnel is not available", Code: "TUNNEL_NOT_AVAILABLE"})
		return nil, false
	}
	return h.requireTunnelCaller(c, tunnel)
}

func (h *TunnelHandler) participantIsHost(p models.TunnelParticipant, tunnel *models.Tunnel) bool {
	if tunnel.InitiatorCNSUserID != 0 && p.CNSUserID.Valid &&
		p.CNSUserID.Int64 == tunnel.InitiatorCNSUserID {
		return true
	}
	if tunnel.InitiatorDeviceID.Valid && p.DeviceID.Valid &&
		strings.EqualFold(p.DeviceID.String, tunnel.InitiatorDeviceID.String) {
		return true
	}
	return false
}

func (h *TunnelHandler) generateUniqueTunnelCode(ctx context.Context) (string, error) {
	for i := 0; i < 20; i++ {
		code := generateVerificationCode(4)
		exists, err := h.db.TunnelCodeExists(ctx, code)
		if err != nil {
			return "", err
		}
		if !exists {
			return code, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique tunnel code")
}

func (h *TunnelHandler) buildQRPayload(tunnel *models.Tunnel) string {
	payload, err := json.Marshal(gin.H{
		"p": "sendly-tunnel-v1",
		"s": h.cfg.BaseURL,
		"c": tunnel.Code,
	})
	if err != nil {
		return ""
	}
	return string(payload)
}

func nullableDeviceID(value string) sql.NullString {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: trimmed, Valid: true}
}