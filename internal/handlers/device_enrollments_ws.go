package handlers

import (
	"context"
	"fmt"
	"net/http"

	"sendly/internal/middleware"
	"sendly/internal/models"
	"sendly/internal/realtime"
	"sendly/internal/storage"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// deviceEnrollmentHub carries account events to the browser sockets of
// /api/me/devices/ws, on whichever replica they are connected.
type deviceEnrollmentHub struct {
	rt *realtime.Hub
}

func newDeviceEnrollmentHub(rt *realtime.Hub) *deviceEnrollmentHub {
	return &deviceEnrollmentHub{rt: rt}
}

func deviceEventsTopic(userID int64) string {
	return fmt.Sprintf("web:user:%d", userID)
}

func (h *deviceEnrollmentHub) add(userID int64, conn *websocket.Conn) *realtime.Conn {
	return h.rt.Add(deviceEventsTopic(userID), conn)
}

func (h *deviceEnrollmentHub) broadcast(userID int64, payload any) {
	h.rt.Publish(context.Background(), deviceEventsTopic(userID), payload)
}

func gatherEnrollmentEventData(ctx context.Context, db *storage.Postgres, userID int64, enrollmentID string) (enrollment *models.DeviceEnrollment, requestDevice models.UserDevice, pendingCount int, ok bool) {
	enrollment, err := db.GetEnrollmentByID(ctx, userID, enrollmentID)
	if err != nil {
		return nil, models.UserDevice{}, 0, false
	}

	devices, err := db.GetActiveDevicesByUser(ctx, userID)
	requestDevice = models.UserDevice{ID: enrollment.RequestDeviceID}
	if err == nil {
		for _, device := range devices {
			if device.ID == enrollment.RequestDeviceID {
				requestDevice = normalizeDevicePublicKeyForResponse(device)
				break
			}
		}
	}

	pendingItems, err := db.ListPendingEnrollments(ctx, userID)
	if err == nil {
		pendingCount = len(pendingItems)
	}

	return enrollment, requestDevice, pendingCount, true
}

func (h *RecentUploadsHandler) Hub() *deviceEnrollmentHub {
	return h.hub
}

func (h *RecentUploadsHandler) wsUpgrader() *websocket.Upgrader {
	return &websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			return origin == "" || origin == h.cfg.BaseURL
		},
	}
}

func (h *RecentUploadsHandler) publishEnrollmentChange(ctx context.Context, userID int64, eventType, enrollmentID, approverDeviceID string) {
	enrollment, requestDevice, pendingCount, ok := gatherEnrollmentEventData(ctx, h.db, userID, enrollmentID)
	if !ok {
		return
	}

	h.hub.broadcast(userID, gin.H{
		"type":               eventType,
		"enrollment":         enrollment,
		"request_device":     requestDevice,
		"approver_device_id": approverDeviceID,
		"pending_count":      pendingCount,
	})

	if h.androidHub != nil {
		h.androidHub.broadcastUser(userID, gin.H{
			"type":               eventType,
			"enrollment":         enrollment,
			"request_device":     requestDevice,
			"approver_device_id": approverDeviceID,
			"pending_count":      pendingCount,
		})
		h.androidHub.broadcastPending(userID, gin.H{
			"type":           "pending_approvals_updated",
			"pending_count":  pendingCount,
			"enrollment":     enrollment,
			"request_device": requestDevice,
		})
		h.androidHub.broadcastEnrollment(enrollmentID, gin.H{
			"type":               "enrollment_status",
			"enrollment":         enrollment,
			"approver_device_id": approverDeviceID,
		})
	}
}

// upgradeResponseCookies carries the Set-Cookie headers already queued on
// the response into the WebSocket handshake. Upgrade hijacks the connection
// and writes the 101 itself, ignoring c.Writer's headers, so when
// CNSAuthMiddleware refreshed the session on this request the rotated
// refresh token never reached the browser. The browser then kept presenting
// the consumed token, and once CNS's grace window passed, the next request
// tripped refresh-token reuse detection and revoked the whole session.
func upgradeResponseCookies(c *gin.Context) http.Header {
	cookies := c.Writer.Header().Values("Set-Cookie")
	if len(cookies) == 0 {
		return nil
	}
	header := http.Header{}
	for _, cookie := range cookies {
		header.Add("Set-Cookie", cookie)
	}
	return header
}

func (h *RecentUploadsHandler) DeviceEvents(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	conn, err := h.wsUpgrader().Upgrade(c.Writer, c.Request, upgradeResponseCookies(c))
	if err != nil {
		return
	}

	h.hub.add(int64(user.ID), conn)
}
