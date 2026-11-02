package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// SetAccountLocale stores the language a signed-in user picked on sendly as
// their CNS account language (PUT /api/me/locale), so every CNS service and
// email follows it. CNS only accepts this from first-party apps; for any other
// client it answers 403, and the caller just keeps the language locally.
func (c *CNSClient) SetAccountLocale(ctx context.Context, accessToken, locale string) error {
	base := c.cfg.CNSAPIBaseURL()
	if base == "" || accessToken == "" {
		return fmt.Errorf("cns api is not configured")
	}
	body, err := json.Marshal(map[string]string{"locale": locale})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/api/me/locale", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "Sendly-Auth-Bridge/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("cns locale update failed with status %d: %s", resp.StatusCode, bytes.TrimSpace(respBody))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
