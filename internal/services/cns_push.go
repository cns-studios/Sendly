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

	"sendly/internal/cnshttp"
)

// NotifyCNSAccounts asks the CNS API (apiURL, config.CNSAPIBaseURL) to wake
// the user's CNS Accounts app (POST /api/service/push; Sendly's service key
// needs the push:send scope). Only the event and an id travel; the app loads
// the rest from Sendly. Runs in the background and never fails the caller.
func NotifyCNSAccounts(apiURL, serviceKey string, userID int64, event, id string) {
	if strings.TrimSpace(apiURL) == "" || strings.TrimSpace(serviceKey) == "" || userID <= 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		body, _ := json.Marshal(map[string]any{"user_id": userID, "event": event, "id": id})
		// Not idempotent: a push is repeated only when no CNS replica took it.
		resp, err := cnshttp.Do(ctx, http.DefaultClient, false, func(ctx context.Context) (*http.Request, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(apiURL, "/")+"/api/service/push", bytes.NewReader(body))
			if err != nil {
				return nil, err
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("x-service-key", serviceKey)
			req.Header.Set("User-Agent", "Sendly-Auth-Bridge/1.0")
			return req, nil
		})
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
