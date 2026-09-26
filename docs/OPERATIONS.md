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
6. Storage claim (instance marker check, see below)
7. Cleanup background service start
8. Upload pending-cleanup background service start
9. HTTP server listen

## Health and Runtime Checks

- `GET /health` probes PostgreSQL (ping), Redis (ping) and storage (durable
  write into `DATA_DIR` and `CHUNK_DIR`, plus the instance marker) with a 2s
  timeout each. It answers 200 `{"status":"healthy","checks":{...}}`, or 503
  `{"status":"unhealthy",...}` with the failing check marked `error`. Error
  details go to the log (`Health check "redis" failed: ...`), never to the
  response. Results are cached for 5s. The Docker `HEALTHCHECK` uses it.
- `GET /livez` only reports that the process answers requests.
- Logs include component startup milestones.

Not covered by `/health`: migration state consistency.

## Cleanup Behavior

Cleanup service runs every 5 minutes and:

- Marks expired files deleted in DB
- Deletes corresponding file blobs
- Cleans orphaned chunks
- Cleans orphaned files absent from DB

Upload service cleanup runs every minute for pending/session artifacts.

## Storage Ownership

Orphan cleanup deletes every blob that its database doesn't know about and
every chunk session that its Redis doesn't know about. Two instances sharing
storage (for example staging started with the prod compose override, which
bind-mounts `/mnt/shareit`) therefore delete each other's files within
minutes.

To prevent this, each database holds a random ID (`instance_meta`), and the
server writes it to a `.sendly-instance` marker in `DATA_DIR` and in
`CHUNK_DIR` when that lies outside `DATA_DIR`. On startup:

- Marker matches this database: start normally.
- No marker, directory holds no data: claim it and start.
- No marker, directory holds data: refuse to start. If the storage really
  belongs to this database (an existing deployment), start once with
  `SENDLY_ADOPT_DATA_DIR=true`, then unset it.
- Marker from another database: refuse to start. Give the instance its own
  storage. `SENDLY_ADOPT_DATA_DIR` does not override this; only deleting the
  marker by hand does.

The admin CLI only verifies the marker and never claims storage.

Staging and other non-prod stacks should use the base `docker-compose.yaml`
alone (named volumes, namespaced by compose project name), never
`docker-compose.prod.yaml`.

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
