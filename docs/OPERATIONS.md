# ShareIt Backend Operations Guide

This document covers day-to-day backend operation tasks.

## Local Development

Typical flow:

1. Copy env file and set required values.
2. Start dependencies (postgres, redis) via Docker compose.
3. Run server from `cmd/server/main.go`.

Useful commands:

```bash
make migrate
go run cmd/server/main.go
```

## Startup Lifecycle

On startup, server performs:

1. Config load
2. PostgreSQL connect
3. Migration run
4. Redis connect
5. Filesystem storage init
6. Cleanup background service start
7. Upload pending-cleanup background service start
8. HTTP server listen

## Health and Runtime Checks

- Health endpoint: `GET /health`
- Logs include component startup milestones.

Recommended checks:

- DB connectivity
- Redis connectivity
- Filesystem write permissions on data/chunk paths
- Migration state consistency

## Cleanup Behavior

Cleanup service runs every 5 minutes and:

- Marks expired files deleted in DB
- Deletes corresponding file blobs
- Cleans orphaned chunks
- Cleans orphaned files absent from DB

Upload service cleanup runs every minute for pending/session artifacts.

## Migration Operations

- Migrations auto-run at startup.
- Use `MIGRATIONS_DIR` to control migration source path.
- Avoid editing already-applied migration files (checksum mismatch risk).

## Rate-Limit Tuning Procedure

1. Measure baseline request rates and false positives.
2. Adjust standard limiter for upload spikes.
3. Keep strict limiter conservative on sensitive device/enrollment routes.
4. Set download limiter high enough for normal consumption but low enough for abuse protection.
5. Roll out incrementally and monitor error code `RATE_LIMITED`/`DOWNLOAD_RATE_LIMITED` trends.

## Common Failure Scenarios

### Upload finalization fails

Check:

- Session pending status in redis
- Assembled file existence on disk
- Duration/tunnel validation rules
- Envelope validation for trusted flows

### Device enrollment issues

Check:

- Device ownership and trust state
- Enrollment expiration window
- Verification code matching
- Pending enrollment websocket event flow

### Tunnel access errors

Check:

- Tunnel status (`pending`, `joined`, `active`, `ended`, `expired`)
- Ownership checks
- Expiration timestamp

## Shutdown Behavior

Graceful shutdown uses server `Shutdown` with 30-second timeout.

Background services stop and wait for goroutines to exit cleanly.
