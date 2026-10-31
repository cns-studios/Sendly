# Sendly Operations

Running, configuring and maintaining a Sendly instance. See [ARCHITECTURE.md](ARCHITECTURE.md) for how the parts fit together and [SECURITY.md](SECURITY.md) for hardening.

## Running

Sendly needs Go (see `go.mod`) and PostgreSQL. The `Makefile` wraps the common Docker Compose flows. For several replicas on Kubernetes, see [KUBERNETES.md](KUBERNETES.md).

| Command | What it does |
|---|---|
| `make dev-up` | Start the dev stack (app, PostgreSQL) in the background. |
| `make dev-full` | Rebuild images and start the dev stack. |
| `make dev-build` | Build dev images only. |
| `make prod-build` / `make prod-up` | Build / start with `docker-compose.prod.yaml` layered on top. |
| `make down` | Stop the dev stack. |
| `make logs` | Follow the app logs. |
| `make migrate` | Run migrations manually (`go run cmd/migrate/main.go`). |

Without Docker: `cp .env.example .env`, start PostgreSQL, then `go run ./cmd/server`. The app listens on `PORT` (default `8085`).

Staging and other non-prod stacks should use the base `docker-compose.yaml` alone (named volumes), never the prod override ([storage ownership](#storage-ownership)).

## Configuration

All settings are environment variables; `.env.example` is the annotated template.

### Server
- `PORT` (`8085`): listen port.
- `BASE_URL` (`http://localhost:8085`): public origin, used for share links, auth callbacks, cookie security and CSP.
- `TOS_VERSION`: terms version exposed to clients.
- `GIN_MODE`: `release` for production.

### PostgreSQL
- `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`
- `POSTGRES_SSLMODE` (`disable`): `disable`, `require`, `verify-ca` or `verify-full`.
- `MIGRATIONS_DIR` (`db/migrations`): migrations run at startup and are tracked in the schema history.

### Replicas and shutdown
- `POD_NAME`: names this replica in upload assembly leases; defaults to the hostname. Kubernetes sets it from the pod name.
- `SHUTDOWN_TIMEOUT_SECONDS` (`25`): after SIGTERM, in-flight requests and upload assemblies get this long. Unfinished assemblies are then handed to another replica.

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
- `CNS_AUTH_URL`, `CNS_AUTH_CLIENT_ID`, `CNS_AUTH_SERVICE_KEY` (scopes `push:send` and `users:read`, see [CNS user cache](#cns-user-cache))
- `CNS_AUTH_DESKTOP_CLIENT_ID`: kept for non-web clients.
- `CNS_SERVICE_API_URL`, `CNS_SERVICE_SLUG` (`sendly`): CNS service gateway and this service's slug.
- `CNS_API_URL`: where Sendly's server calls the CNS API (token checks and refreshes, code exchange, logout, push, the directory, `/api/service/me`, `/api/data`). Unset falls back to `CNS_SERVICE_API_URL`, then `CNS_AUTH_URL`. `CNS_AUTH_URL` itself is only where browsers are sent, because `accounts.cns.ad` is the accounts web app and serves neither the directory nor push. In Kubernetes set it to the accounts-api Service (`http://accounts-api.accounts-api.svc.cluster.local:3845`): the calls then skip Cloudflare and accounts-web, and forward the user's IP as `CF-Connecting-IP`, which CNS trusts from the cluster network, so its per-IP rate limits count each user instead of all of Sendly. Every call is repeated once when it reaches a CNS replica that is going away (refused connection, 503; also reset, 502 and 504 for repeatable calls); a token check that still fails serves that request signed out without touching the cookies, and only a 401/403 from CNS refreshes or clears them.
- `CNS_WEBHOOK_SECRET`: the `whsec_...` secret CNS showed when the webhook was set. Comma-separate two while rotating. Unset disables `POST /webhooks/cns`.
- `USER_CACHE_TTL_HOURS` (`24`): how long a signed-in user's own profile is trusted before Sendly fetches it again.
- `USER_CACHE_RECONCILE_INTERVAL_MINUTES` (`60`): how often one replica runs the reconciliation job.
- `USER_CACHE_RECHECK_HOURS` (`24`): how long a cached user goes before the job re-checks them against the CNS directory. Must be shorter than the stale period.
- `USER_CACHE_STALE_AFTER_DAYS` (`30`): users CNS hasn't confirmed for this long leave recipient search (only happens when the directory can't be reached).
- `USER_CACHE_MAX_DELETIONS_PER_RUN` (`25`): above this many users missing from the directory in one run, the run deletes none of them.

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
Strong database credentials, `BASE_URL` set to the public HTTPS origin, `GIN_MODE=release`, rate limits tuned to expected traffic, PostgreSQL not exposed publicly, `TRUSTED_PROXIES` set if behind a proxy, `CNS_AUTH_*` configured for accounts.

## Startup and shutdown

Startup order: load config → connect PostgreSQL (retried for up to 2 minutes) → run migrations → init filesystem storage → storage ownership check → start listening for realtime events → start cleanup and upload services → listen.

Shutdown is graceful: readiness starts failing, WebSockets are closed with 1012 so clients reconnect elsewhere, in-flight requests and upload assemblies get `SHUTDOWN_TIMEOUT_SECONDS`, unfinished assemblies are released to other replicas, then background services stop.

## Health checks

- `GET /health` probes PostgreSQL and storage (a durable write into `DATA_DIR` and `CHUNK_DIR`, plus the instance marker), 2 s timeout each, cached for 5 s. It answers `200 {"status":"healthy","checks":{...}}` or `503 {"status":"unhealthy",...}` with the failing check marked `error`. Details go to the log (`Health check "postgres" failed: ...`), never to the response. The Docker `HEALTHCHECK` uses it. It does not verify migration consistency.
- `GET /readyz` is `/health` plus the shutdown state: it fails as soon as the replica starts draining. Use it as the readiness probe.
- `GET /livez` only reports that the process answers. Use it as the liveness probe, so a database outage never restarts pods.

## Cleanup

- Every 5 minutes, on one replica of the deployment: mark expired files deleted, delete their blobs, remove orphaned chunk directories, interrupted assemblies and blobs that neither a file nor an upload session refers to, purge old rate limit counters and token refresh results, then apply data retention.
- Every minute, on every replica: remove expired upload sessions with their chunks and unfinalized blobs.

## CNS user cache

The `users` table caches the CNS username and avatar of everyone who signed in to Sendly, for recipient search and display. It stays current through:

- **Sign-in:** a signed-in request refreshes the user's own row from `GET /api/service/me` once per `USER_CACHE_TTL_HOURS`. Only this source knows whether the account is locked; a locked account leaves search until the user's next request.
- **Webhooks:** CNS posts `user.updated` (rename, new avatar) and `user.deleted` to `POST /webhooks/cns` within seconds. Deliveries are signed (HMAC-SHA256 over timestamp and body, 5 minute window), applied once per event id, and older events never overwrite newer data. Renames only arrive for users who are still signed in to Sendly; deletions always arrive.
- **Directory check:** the reconciliation job re-checks every user against CNS's `users:read` directory once per `USER_CACHE_RECHECK_HOURS`, at most 2000 users per run. A user missing from the batch answer counts as deleted only after a single lookup returns CNS's `not_found`.

A deleted account is final: its username and avatar are erased (also the copy on its files), it disappears from search, and sharing to it answers `404 USER_NOT_FOUND`. Its row stays, empty, so late events can't bring it back. CNS lets someone else take the freed name; if two cached rows ever hold the same name, the one observed earlier leaves search until it is re-checked.

Setup, once per environment (CNS admin API):

1. Add `users:read` to Sendly's service key: `PATCH /api/admin/service-clients/{id}` with `"scopes": ["push:send", "users:read"]`. The key itself doesn't change.
2. Point Sendly's OAuth client at the webhook: `PUT /api/admin/clients/{CNS_AUTH_CLIENT_ID}/webhook` with `{"webhook_url": "https://<BASE_URL host>/webhooks/cns", "reason": "..."}`. Put the returned `webhook_secret` into `CNS_WEBHOOK_SECRET` and deploy.
3. Check it: `POST /api/admin/clients/{CNS_AUTH_CLIENT_ID}/webhook/test` sends a `ping` (logged as `cns webhook: ping ... received`), and `GET .../webhook/deliveries` should show `204`s.

Setting the webhook again issues a new secret and CNS signs with it immediately. Deliveries signed with a secret Sendly doesn't have yet fail and are retried for about a day, so deploy promptly; to avoid failures, deploy `CNS_WEBHOOK_SECRET=<new>,<old>` right after rotating and drop the old one later.

Without `users:read` the job logs `directory check stopped early: cns directory access denied` every run and falls back to aging users out after `USER_CACHE_STALE_AFTER_DAYS`.

If a run finds more than `USER_CACHE_MAX_DELETIONS_PER_RUN` users missing, it deletes none and logs `CNS directory no longer returns N cached user(s)`. That protects the cache from a CNS fault. If CNS really deleted them, run `admin sync-users --max-deletions N`.

## Storage ownership

Orphan cleanup deletes every blob and every chunk directory its database does not know about. Two instances sharing storage (for example staging started with the prod override, which bind-mounts `/mnt/shareit`) would delete each other's files within minutes.

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

`cmd/admin` (shipped in the Docker image as `./admin`) works against the same database and storage: `view`, `delete`, `download`, `list`, `stats`, `tracking`, `reports <file_id>` and `cleanup`. It also has desktop API key commands (`create-key`, `revoke-key`, `list-keys`, ...) and `sync-users [--all] [--max-deletions N]`, which runs the [CNS user cache](#cns-user-cache) directory check now. Run `admin help` for usage.

## Troubleshooting

- **Upload finalize fails:** check the session in `upload_sessions` (state, error, assembly lease), that the assembled file exists on disk, the duration or tunnel rules, and the identity envelope fields.
- **Device enrollment stuck:** check device ownership and trust state, the enrollment expiry, the verification code, and the WebSocket connection of the approving device.
- **Tunnel errors:** check tunnel status (`pending`, `joined`, `active`, `ended`, `expired`), membership and host approval, and the expiry time.
- **Everyone shares one rate limit:** the app sees the proxy's IP; set `TRUSTED_PROXIES`.
- **Renamed or deleted users still show up in search:** check the CNS delivery log (`GET /api/admin/clients/{id}/webhook/deliveries`; `401` from Sendly means `CNS_WEBHOOK_SECRET` is wrong) and the `user cache:` log lines of the reconciliation job. See [CNS user cache](#cns-user-cache).
- **Server refuses to start on storage:** see [Storage ownership](#storage-ownership).
