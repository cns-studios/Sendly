package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// NotifyCNSAccounts asks CNS auth to wake the user's CNS Accounts app
// (POST /api/service/push; Sendly's service key needs the push:send scope).
// Only the event and an id travel; the app loads the rest from Sendly. Runs
// in the background and never fails the caller.
func NotifyCNSAccounts(authURL, serviceKey string, userID int64, event, id string) {
	if strings.TrimSpace(authURL) == "" || strings.TrimSpace(serviceKey) == "" || userID <= 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		body, _ := json.Marshal(map[string]any{"user_id": userID, "event": event, "id": id})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(authURL, "/")+"/api/service/push", bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-service-key", serviceKey)
		req.Header.Set("User-Agent", "Sendly-Auth-Bridge/1.0")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("cns push %s failed: %v", event, err)
			return
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			log.Printf("cns push %s failed: %s", event, fmt.Sprint(resp.StatusCode))
		}
	}()
}
