package storage

import (
	"context"
	"database/sql"
	"errors"
	"hash/fnv"
	"time"
)

// Coordination between replicas: shared rate limit counters, short-lived
// token refresh results, and periodic jobs that must run once per interval
// across the whole deployment rather than once per replica.

// CheckRateLimit counts one request against key in a fixed window and
// reports whether it stays within maxRequests.
func (p *Postgres) CheckRateLimit(ctx context.Context, key string, maxRequests int64, window time.Duration) (bool, error) {
	windowStart := time.Now().UTC().Truncate(window)
	var count int64
	err := p.db.GetContext(ctx, &count, `
		INSERT INTO rate_limit_counters (key, window_start, count) VALUES ($1, $2, 1)
		ON CONFLICT (key) DO UPDATE SET
			count = CASE WHEN rate_limit_counters.window_start = EXCLUDED.window_start
			             THEN rate_limit_counters.count + 1 ELSE 1 END,
			window_start = EXCLUDED.window_start
		RETURNING count`, key, windowStart)
	if err != nil {
		return false, err
	}
	return count <= maxRequests, nil
}

// PurgeRateLimitCounters drops counters whose window started before olderThan ago.
func (p *Postgres) PurgeRateLimitCounters(ctx context.Context, olderThan time.Duration) (int64, error) {
	res, err := p.db.ExecContext(ctx, `
		DELETE FROM rate_limit_counters WHERE window_start < NOW() - make_interval(secs => $1)`, olderThan.Seconds())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SharedRefresh runs refresh at most once per tokenID across all replicas
// within ttl. Callers presenting the same token wait on an advisory lock;
// whoever comes second gets the sealed result the first one stored. The
// lock is held across refresh, so refresh must be bounded by ctx.
func (p *Postgres) SharedRefresh(ctx context.Context, tokenID []byte, ttl time.Duration, refresh func() ([]byte, error)) (sealed []byte, cached bool, err error) {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey(tokenID)); err != nil {
		return nil, false, err
	}

	err = tx.GetContext(ctx, &sealed, `
		SELECT sealed FROM token_refresh_results WHERE token_id = $1 AND expires_at > NOW()`, tokenID)
	if err == nil {
		return sealed, true, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	sealed, err = refresh()
	if err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO token_refresh_results (token_id, sealed, expires_at)
		VALUES ($1, $2, NOW() + make_interval(secs => $3))
		ON CONFLICT (token_id) DO UPDATE SET sealed = EXCLUDED.sealed, expires_at = EXCLUDED.expires_at`,
		tokenID, sealed, ttl.Seconds()); err != nil {
		return nil, false, err
	}
	return sealed, false, tx.Commit()
}

func (p *Postgres) PurgeTokenRefreshResults(ctx context.Context) (int64, error) {
	res, err := p.db.ExecContext(ctx, `DELETE FROM token_refresh_results WHERE expires_at < NOW()`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RunJob runs fn if no replica has started the job named name within the
// last interval, and no replica is running it right now. It reports whether
// fn ran. The claim is recorded before fn starts, so a replica that dies
// halfway leaves the next run to whichever replica comes after interval.
func (p *Postgres) RunJob(ctx context.Context, name string, interval time.Duration, fn func(context.Context)) (bool, error) {
	conn, err := p.db.Connx(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	key := lockKey([]byte("job:" + name))
	var locked bool
	if err := conn.GetContext(ctx, &locked, `SELECT pg_try_advisory_lock($1)`, key); err != nil {
		return false, err
	}
	if !locked {
		return false, nil
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, key)

	var claimed string
	err = conn.GetContext(ctx, &claimed, `
		INSERT INTO job_runs (name, last_run_at) VALUES ($1, NOW())
		ON CONFLICT (name) DO UPDATE SET last_run_at = NOW()
		WHERE job_runs.last_run_at <= NOW() - make_interval(secs => $2)
		RETURNING name`, name, interval.Seconds())
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	fn(ctx)
	return true, nil
}

// lockKey maps an arbitrary identifier onto the bigint advisory lock space.
func lockKey(id []byte) int64 {
	h := fnv.New64a()
	h.Write([]byte("sendly:"))
	h.Write(id)
	return int64(h.Sum64())
}

// Notify sends payload to every session listening on channel, which is how
// replicas pass realtime events to each other.
func (p *Postgres) Notify(ctx context.Context, channel, payload string) error {
	_, err := p.db.ExecContext(ctx, `SELECT pg_notify($1, $2)`, channel, payload)
	return err
}
