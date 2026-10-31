// Package cnshttp sends Sendly's server-to-server requests to CNS accounts.
//
// CNS runs as several replicas that are replaced at any time. A request that
// lands on a replica as it goes away fails with a refused or reset connection,
// or with a 502/503/504 from the proxy in front of it; the same request sent
// again reaches another replica. Do repeats such a request once, so a rolling
// update or a rescheduled pod never shows up as a signed-out user.
package cnshttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// retryDelay leaves the cluster a moment to route around the failed replica.
const retryDelay = 250 * time.Millisecond

type clientIPKey struct{}

// WithClientIP records the end user's IP on ctx. Do forwards it to CNS as
// CF-Connecting-IP, which CNS believes only from trusted peers (the cluster
// network), so its per-IP rate limits count each user instead of lumping
// every Sendly user into the address of the Sendly pod.
func WithClientIP(ctx context.Context, ip string) context.Context {
	if parsed := net.ParseIP(ip); parsed != nil {
		return context.WithValue(ctx, clientIPKey{}, parsed.String())
	}
	return ctx
}

func clientIP(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPKey{}).(string)
	return ip
}

// Do sends the request build returns, and sends a fresh one once more when
// the first did not reach a working replica. build is called per attempt, so
// it must create a new request (and body) each time.
//
// A refused connection or a 503 means no replica took the request, so it is
// always repeated. A reset connection, a 502 or a 504 may come after CNS
// acted on it, so those are repeated only when idempotent is true.
func Do(ctx context.Context, client *http.Client, idempotent bool, build func(context.Context) (*http.Request, error)) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	for attempt := 0; ; attempt++ {
		req, err := build(ctx)
		if err != nil {
			return nil, err
		}
		if ip := clientIP(ctx); ip != "" && req.Header.Get("CF-Connecting-IP") == "" {
			req.Header.Set("CF-Connecting-IP", ip)
		}
		resp, err := client.Do(req)
		if attempt > 0 || ctx.Err() != nil || !retryable(resp, err, idempotent) {
			return resp, err
		}
		if resp != nil {
			resp.Body.Close()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retryDelay):
		}
	}
}

func retryable(resp *http.Response, err error, idempotent bool) bool {
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Op == "dial" {
			return true
		}
		return idempotent
	}
	switch resp.StatusCode {
	case http.StatusServiceUnavailable:
		return true
	case http.StatusBadGateway, http.StatusGatewayTimeout:
		return idempotent
	}
	return false
}
