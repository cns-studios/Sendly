package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sendly/internal/models"

	"github.com/gin-gonic/gin"
)

const (
	// cnsWebhookMaxBody is far above any CNS event (a public profile at most).
	cnsWebhookMaxBody = 64 << 10
	// cnsWebhookTolerance is how far the signed timestamp may be from now.
	// CNS signs every retry afresh, so this only has to cover clock skew.
	cnsWebhookTolerance = 5 * time.Minute
)

// CNSWebhookStore applies CNS webhook events, each exactly once per event id.
type CNSWebhookStore interface {
	ApplyCNSUserUpdated(ctx context.Context, eventID string, profile models.CNSUserProfile) (bool, error)
	ApplyCNSUserDeleted(ctx context.Context, eventID string, cnsUserID int64) (bool, error)
}

// CNSSignOutNotifier tells a user's open Sendly pages, on every replica, that
// their session ended, so they leave the signed-in view without waiting for
// their next request.
type CNSSignOutNotifier interface {
	SignedOut(ctx context.Context, cnsUserID int64)
}

// CNSWebhookHandler receives CNS account events for Sendly's OAuth client
// (docs: accounts-api INTEGRATION.md, "react to account changes"). Any 2xx
// tells CNS the event is handled; anything else is retried with backoff for
// about a day, so a failure to apply answers 5xx.
type CNSWebhookHandler struct {
	secrets [][]byte
	store   CNSWebhookStore
	signOut CNSSignOutNotifier
	now     func() time.Time
}

// SetSignOutNotifier enables reacting to app.disconnected. Without one the
// event is acknowledged and nothing else happens: CNS has already revoked
// the user's tokens, so their next request is signed out anyway.
func (h *CNSWebhookHandler) SetSignOutNotifier(n CNSSignOutNotifier) { h.signOut = n }

func NewCNSWebhookHandler(secrets []string, store CNSWebhookStore) *CNSWebhookHandler {
	h := &CNSWebhookHandler{store: store, now: time.Now}
	for _, s := range secrets {
		if s = strings.TrimSpace(s); s != "" {
			h.secrets = append(h.secrets, []byte(s))
		}
	}
	return h
}

type cnsWebhookEvent struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	CreatedAt time.Time       `json:"created_at"`
	Data      json.RawMessage `json:"data"`
}

func (h *CNSWebhookHandler) Handle(c *gin.Context) {
	if len(h.secrets) == 0 {
		c.Status(http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, cnsWebhookMaxBody+1))
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	if len(body) > cnsWebhookMaxBody {
		c.Status(http.StatusRequestEntityTooLarge)
		return
	}
	if !h.verify(c.GetHeader("CNS-Webhook-Timestamp"), c.GetHeader("CNS-Webhook-Signature"), body) {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Invalid webhook signature", Code: "INVALID_SIGNATURE"})
		return
	}

	// Only the body is signed, so the event id comes from there and not from
	// the CNS-Webhook-Id header.
	var event cnsWebhookEvent
	if err := json.Unmarshal(body, &event); err != nil || strings.TrimSpace(event.ID) == "" || event.Type == "" {
		h.reject(c, event, "malformed event")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	switch event.Type {
	case "user.updated":
		var data struct {
			User struct {
				ID       int64   `json:"id"`
				Username string  `json:"username"`
				Avatar   *string `json:"avatar"`
			} `json:"user"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil || data.User.ID <= 0 ||
			strings.TrimSpace(data.User.Username) == "" || event.CreatedAt.IsZero() {
			h.reject(c, event, "user.updated without a user id, username and created_at")
			return
		}
		profile := models.CNSUserProfile{CNSUserID: data.User.ID, Username: data.User.Username, ObservedAt: event.CreatedAt}
		if data.User.Avatar != nil {
			profile.AvatarURL = *data.User.Avatar
		}
		_, err = h.store.ApplyCNSUserUpdated(ctx, event.ID, profile)
	case "user.deleted":
		var data struct {
			UserID int64 `json:"user_id"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil || data.UserID <= 0 {
			h.reject(c, event, "user.deleted without a user id")
			return
		}
		var applied bool
		applied, err = h.store.ApplyCNSUserDeleted(ctx, event.ID, data.UserID)
		if err == nil && applied {
			log.Printf("cns webhook: cns_user_id=%d was deleted; erased the cached profile", data.UserID)
		}
	case "app.disconnected":
		var data struct {
			UserID int64 `json:"user_id"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil || data.UserID <= 0 {
			h.reject(c, event, "app.disconnected without a user id")
			return
		}
		// CNS revoked the tokens before sending this, so every cookie the user
		// holds is already dead. All that is left is their open pages.
		if h.signOut != nil {
			h.signOut.SignedOut(ctx, data.UserID)
		}
		log.Printf("cns webhook: cns_user_id=%d disconnected Sendly in CNS; signed out their open pages", data.UserID)
	case "ping":
		log.Printf("cns webhook: ping %s received", event.ID)
	default:
		// New event types are not an error: acknowledge so CNS stops retrying.
	}
	if err != nil {
		log.Printf("cns webhook: applying %s %s failed: %v", event.Type, event.ID, err)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "Could not apply event", Code: "WEBHOOK_APPLY_FAILED"})
		return
	}
	c.Status(http.StatusNoContent)
}

// reject answers a correctly signed event this handler can't use. It is
// still a 4xx so the delivery shows as failed in CNS's delivery log.
func (h *CNSWebhookHandler) reject(c *gin.Context, event cnsWebhookEvent, reason string) {
	log.Printf("cns webhook: rejected event %q (%s): %s", event.ID, event.Type, reason)
	c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Malformed event", Code: "INVALID_EVENT"})
}

// verify checks CNS-Webhook-Signature ("v1=" + hex HMAC-SHA256 of
// timestamp + "." + body) against every configured secret, and that the
// timestamp is recent.
func (h *CNSWebhookHandler) verify(timestamp, signature string, body []byte) bool {
	ts, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return false
	}
	skew := h.now().Sub(time.Unix(ts, 0))
	if skew > cnsWebhookTolerance || skew < -cnsWebhookTolerance {
		return false
	}
	given, ok := strings.CutPrefix(strings.TrimSpace(signature), "v1=")
	if !ok {
		return false
	}
	givenMAC, err := hex.DecodeString(given)
	if err != nil {
		return false
	}
	valid := false
	for _, secret := range h.secrets {
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(strings.TrimSpace(timestamp)))
		mac.Write([]byte("."))
		mac.Write(body)
		if hmac.Equal(mac.Sum(nil), givenMAC) {
			valid = true
		}
	}
	return valid
}
