# Sendly Operations

Running, configuring and maintaining a Sendly instance. See [ARCHITECTURE.md](ARCHITECTURE.md) for how the parts fit together and [SECURITY.md](SECURITY.md) for hardening.

## Running

Sendly needs Go (see `go.mod`), PostgreSQL and Redis. The `Makefile` wraps the common Docker Compose flows:

| Command | What it does |
|---|---|
| `make dev-up` | Start the dev stack (app, PostgreSQL, Redis) in the background. |
| `make dev-full` | Rebuild images and start the dev stack. |
| `make dev-build` | Build dev images only. |
| `make prod-build` / `make prod-up` | Build / start with `docker-compose.prod.yaml` layered on top. |
| `make down` | Stop the dev stack. |
| `make logs` | Follow the app logs. |
| `make migrate` | Run migrations manually (`go run cmd/migrate/main.go`). |

Without Docker: `cp .env.example .env`, start PostgreSQL and Redis, then `go run ./cmd/server`. The app listens on `PORT` (default `8085`).

Staging and other non-prod stacks should use the base `docker-compose.yaml` alone (named volumes), never the prod override ([storage ownership](#storage-ownership)).

## Configuration

All settings are environment variables; `.env.example` is the annotated template.

### Server
- `PORT` (`8085`): listen port.
- `BASE_URL` (`http://localhost:8085`): public origin, used for share links, auth callbacks, cookie security and CSP.
- `TOS_VERSION`: terms version exposed to clients.
- `GIN_MODE`: `release` for production.

### PostgreSQL and Redis
- `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`
- `REDIS_HOST`, `REDIS_PORT`
- `MIGRATIONS_DIR` (`db/migrations`): migrations run at startup and are tracked in the schema history.

### Storage
- `DATA_DIR` (`./data`): final encrypted blobs.
- `CHUNK_DIR` (empty): optional separate location for temporary chunks.
- `SENDLY_ADOPT_DATA_DIR` (`false`): claim existing unmarked storage once ([Storage ownership](#storage-ownership)).

### Limits and moderation
- `MAX_FILE_SIZE` (750 MB): guest size cap. Guests keep files for 7 days.
- `AUTH_MAX_FILE_SIZE` (1.5 GB): signed-in size cap. Accounts keep files for 90 days.
- `AUTO_DELETE_REPORT_COUNT` (`3`): distinct signed-in reporters needed to auto-delete a file.
- `DISCORD_WEBHOOK_URL`: report and auto-delete notifications (optional).
- `REPORT_BOT_URL`, `SENDLY_BOT_API_KEY`, `STATS_REPORT_INTERVAL_MINUTES` (`5`): optional usage stats reporting.

### Accounts (CNS Auth, optional)
Leave unset to run guest-only.
- `CNS_AUTH_URL`, `CNS_AUTH_CLIENT_ID`, `CNS_AUTH_SERVICE_KEY`
- `CNS_AUTH_DESKTOP_CLIENT_ID`: kept for non-web clients.
- `CNS_SERVICE_API_URL`, `CNS_SERVICE_SLUG` (`sendly`): CNS service gateway and this service's slug.
- `USER_CACHE_TTL_HOURS` (`24`), `USER_CACHE_RECONCILE_INTERVAL_MINUTES` (`60`), `USER_CACHE_STALE_AFTER_DAYS` (`30`): local cache of CNS profiles used for recipient search.

### Proxies
- `BEHIND_CLOUDFLARE` (`false`): use `CF-Connecting-IP` as the client IP, but only for requests whose direct peer is a trusted proxy.
- `TRUSTED_PROXIES`: comma-separated IPs/CIDRs whose forwarding headers (`X-Forwarded-For`, `X-Real-IP`, `CF-Connecting-IP`) are believed. Empty means no proxy is trusted. With `BEHIND_CLOUDFLARE=true` it defaults to Cloudflare's edge ranges. Behind `cloudflared` or a local reverse proxy, set it to that proxy's address, otherwise every client appears as the proxy.

### Rate limiting
Per signed-in user, or per client IP for guests. `0` blocks every request on that limiter.

| Limiter | Max (default) | Window (default) | Variables |
|---|---|---|---|
| Standard | 30 | 60 s | `RATE_LIMIT_MAX_PER_MINUTE`, `RATE_LIMIT_WINDOW_SECONDS` |
| Strict | 15 | 60 s | `RATE_LIMIT_STRICT_MAX_PER_MINUTE`, `RATE_LIMIT_STRICT_WINDOW_SECONDS` |
| Download | 60 | 60 s | `RATE_LIMIT_DOWNLOAD_MAX_PER_MINUTE`, `RATE_LIMIT_DOWNLOAD_WINDOW_SECONDS` |

Tuning: measure baseline traffic first; raise Standard for upload spikes; keep Strict conservative (device, enrollment, join and report routes); keep Download high enough for normal use but low enough to deter scraping. Roll out gradually and watch `RATE_LIMITED` / `DOWNLOAD_RATE_LIMITED` trends.

### Data retention
Runs in the cleanup cycle (every 5 minutes). `0` disables a purge; negative or invalid values fall back to the default.
- `FILE_RECORD_RETENTION_DAYS` (`30`): hard-deletes soft-deleted file rows with their key envelopes and transfers.
- `UPLOAD_IP_RETENTION_DAYS` (`30`): deletes per-IP upload rows not updated for this long.
- `REPORT_RETENTION_DAYS` (`90`): deletes reports older than this whose file is deleted, expired or gone. Reports on live files are kept.
- `TUNNEL_REJECTION_RETENTION_DAYS` (`30`): deletes rejections for tunnels that have ended or expired.

### Production baseline
Strong database credentials, `BASE_URL` set to the public HTTPS origin, `GIN_MODE=release`, rate limits tuned to expected traffic, PostgreSQL and Redis not exposed publicly, `TRUSTED_PROXIES` set if behind a proxy, `CNS_AUTH_*` configured for accounts.

## Startup and shutdown

Startup order: load config → connect PostgreSQL → run migrations → connect Redis → init filesystem storage → storage ownership check → start cleanup and pending-upload services → listen.

Shutdown is graceful (30 s timeout); background services stop and wait for their goroutines.

## Health checks

- `GET /health` probes PostgreSQL, Redis and storage (a durable write into `DATA_DIR` and `CHUNK_DIR`, plus the instance marker), 2 s timeout each, cached for 5 s. It answers `200 {"status":"healthy","checks":{...}}` or `503 {"status":"unhealthy",...}` with the failing check marked `error`. Details go to the log (`Health check "redis" failed: ...`), never to the response. The Docker `HEALTHCHECK` uses it. It does not verify migration consistency.
- `GET /livez` only reports that the process answers.

## Cleanup

- Every 5 minutes: mark expired files deleted, delete their blobs, remove orphaned chunk directories and blobs absent from the database, then apply data retention.
- Every minute: remove abandoned upload sessions and stale pending artifacts.

## Storage ownership

Orphan cleanup deletes every blob its database does not know about and every chunk session its Redis does not know about. Two instances sharing storage (for example staging started with the prod override, which bind-mounts `/mnt/shareit`) would delete each other's files within minutes.

To prevent that, each database holds a random ID (`instance_meta`), and the server writes it to a `.sendly-instance` marker in `DATA_DIR` and in `CHUNK_DIR` when that lies outside `DATA_DIR`. On startup:

- Marker matches this database: start normally.
- No marker, directory holds no data: claim it and start.
- No marker, directory holds data: refuse to start. If the storage really belongs to this database (an existing deployment), start once with `SENDLY_ADOPT_DATA_DIR=true`, then unset it.
- Marker from another database: refuse to start. Give the instance its own storage. `SENDLY_ADOPT_DATA_DIR` does not override this; only deleting the marker by hand does.

The admin CLI only verifies the marker and never claims storage.

## Migrations

Applied automatically at startup from `MIGRATIONS_DIR`; `make migrate` runs them by hand. Do not edit an already-applied migration (checksum mismatch).

### Retiring the identity migration

Accounts from before identity keys move onto them the first time a device holding their legacy user key signs in. Once no account still needs it (no `user_key_envelopes` rows for users without an active identity key, and no `file_key_envelopes` with `dek_wrap_alg = 'AES-GCM-UK-v1'` on live files), remove:

- `internal/handlers/identity_migration.go`, `internal/storage/legacy_user_key.go`, `internal/models/legacy_migration.go` and the `/api/me/identity-migration` routes;
- `web/static/js/identity-migration.js`, its script tags, the `SendlyIdentityMigration` hooks and the legacy user key helpers in `crypto.js`;
- the `needs_identity_migration` response and the `UserHasLegacyUserKey` check in device registration;

then add a migration dropping `legacy_identity_escrow`, `user_key_envelopes` and `file_recipient_key_envelopes`.

## Admin CLI

`cmd/admin` (shipped in the Docker image as `./admin`) works against the same database and storage: `view`, `delete`, `download`, `list`, `stats`, `tracking`, `reports <file_id>` and `cleanup`. It also has desktop API key commands (`create-key`, `revoke-key`, `list-keys`, ...). Run `admin help` for usage.

## Troubleshooting

- **Upload finalize fails:** check the session's pending status in Redis, that the assembled file exists on disk, the duration or tunnel rules, and the identity envelope fields.
- **Device enrollment stuck:** check device ownership and trust state, the enrollment expiry, the verification code, and the WebSocket connection of the approving device.
- **Tunnel errors:** check tunnel status (`pending`, `joined`, `active`, `ended`, `expired`), membership and host approval, and the expiry time.
- **Everyone shares one rate limit:** the app sees the proxy's IP; set `TRUSTED_PROXIES`.
- **Server refuses to start on storage:** see [Storage ownership](#storage-ownership).
