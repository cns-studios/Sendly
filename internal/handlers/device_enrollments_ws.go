package handlers

import (
	"context"
	"net/http"
	"sync"

	"shareit/internal/middleware"
	"shareit/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type deviceEnrollmentHub struct {
	mu    sync.Mutex
	conns map[int64][]*websocket.Conn
}

func newDeviceEnrollmentHub() *deviceEnrollmentHub {
	return &deviceEnrollmentHub{conns: make(map[int64][]*websocket.Conn)}
}

func (h *deviceEnrollmentHub) add(userID int64, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.conns[userID] = append(h.conns[userID], conn)
}

func (h *deviceEnrollmentHub) broadcast(userID int64, payload any) {
	h.mu.Lock()
	defer h.mu.Unlock()

	conns := h.conns[userID]
	if len(conns) == 0 {
		return
	}

	alive := conns[:0]
	for _, conn := range conns {
		if err := conn.WriteJSON(payload); err != nil {
			_ = conn.Close()
			continue
		}
		alive = append(alive, conn)
	}
	h.conns[userID] = alive
}

func (h *RecentUploadsHandler) publishEnrollmentChange(ctx context.Context, userID int64, eventType, enrollmentID, approverDeviceID string) {
	enrollment, err := h.db.GetEnrollmentByID(ctx, userID, enrollmentID)
	if err != nil {
		return
	}

	devices, err := h.db.GetActiveDevicesByUser(ctx, userID)
	requestDevice := models.UserDevice{ID: enrollment.RequestDeviceID}
	if err == nil {
		for _, device := range devices {
			if device.ID == enrollment.RequestDeviceID {
				requestDevice = normalizeDevicePublicKeyForResponse(device)
				break
			}
		}
	}

	pendingItems, err := h.db.ListPendingEnrollments(ctx, userID)
	pendingCount := 0
	if err == nil {
		pendingCount = len(pendingItems)
	}

	h.hub.broadcast(userID, gin.H{
		"type":           eventType,
		"enrollment":     enrollment,
		"request_device": requestDevice,
		"approver_device_id": approverDeviceID,
		"pending_count":  pendingCount,
	})
}

func (h *RecentUploadsHandler) DeviceEvents(c *gin.Context) {
	user := middleware.GetCNSUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "Authentication required", Code: "AUTH_REQUIRED"})
		return
	}

	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	h.hub.add(int64(user.ID), conn)

	go func() {
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}()
}
