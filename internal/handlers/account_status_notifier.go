package handlers

import (
	"context"
	"log"

	"sendly/internal/accountstatus"
	"sendly/internal/realtime"
)

type HostedTunnelEnder interface {
	EndTunnelsHostedBy(ctx context.Context, userID int64) (int64, error)
}

type accountStatusNotifier struct {
	hub     *deviceEnrollmentHub
	tunnels HostedTunnelEnder
}

func NewCNSAccountStatusNotifier(rt *realtime.Hub, tunnels HostedTunnelEnder) CNSAccountStatusNotifier {
	return &accountStatusNotifier{hub: newDeviceEnrollmentHub(rt), tunnels: tunnels}
}

func (n *accountStatusNotifier) AccountStatusChanged(ctx context.Context, cnsUserID int64, status accountstatus.Status) {
	if n.tunnels != nil && !status.Allows(accountstatus.QuickShare) {
		if ended, err := n.tunnels.EndTunnelsHostedBy(ctx, cnsUserID); err != nil {
			log.Printf("account status: ending quick shares hosted by cns_user_id=%d failed: %v", cnsUserID, err)
		} else if ended > 0 {
			log.Printf("account status: ended %d quick share(s) hosted by cns_user_id=%d", ended, cnsUserID)
		}
	}
	n.hub.rt.Publish(ctx, deviceEventsTopic(cnsUserID), map[string]any{"type": "account_status_changed", "status": string(status)})
}
