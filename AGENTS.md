# Sendly — Agent Guide

Sendly (Go module `sendly`; some older paths still say "ShareIt") is an end-to-end encrypted file sharing service: link shares, quick shares (tunnels) and user-to-user transfers. It is a subservice of the external **CNS** auth system.

## Quick reference

```bash
# Local dev (postgres + redis must be running)
cp .env.example .env          # then edit as needed
go run cmd/server/main.go     # starts on :8085

# With dependencies via Docker
make dev-full                 # build + up app, postgres, redis
make logs                     # tail app logs
make migrate                  # manual migration run (also runs on startup)
make down

# Build / checks (no linter config; plain Go tooling)
go build ./... && go vet ./...
go test ./...                 # unit tests; live tests skip themselves

# Admin CLI
go run cmd/admin/main.go stats
go run cmd/admin/main.go list
go run cmd/admin/main.go create-key "owner name"
```

## Project structure

- **`cmd/server/main.go`** — HTTP server entrypoint and the full route table. Wires Gin, Postgres, Redis, filesystem and background services.
- **`cmd/admin/main.go`** — admin CLI: `view`, `delete`, `download`, `list`, `stats`, `reports`, `cleanup`, `create-key`, `revoke-key`, `list-keys`, `key-info`, `key-files`.
- **`cmd/migrate/main.go`** — standalone migration runner.
- **`internal/config/`** — config from `.env` + env vars (godotenv). All defaults live in `config.go`.
- **`internal/handlers/`** — Gin handlers by surface: `upload`, `download`, `pages`, `seo`, `auth`, `report`, `desktop`, `android`, `recent_uploads` (owned files, device registration), `sharing` (user lookup, identity keys, share-to-user), `transfers`, `tunnels`/`tunnel_auth`/`tunnel_keys` (quick shares), `device_enrollments_ws` (per-user websocket hub), `device_identity_shared`.
- **`internal/middleware/`** — IP extraction, CNS cookie auth, CSRF, rate limiters, desktop/android auth, guest/auth tiers, locale, user-cache sync.
- **`internal/storage/`** — `postgres.go` (most queries), `transfers.go`, `tunnels.go`, `desktop.go`, `tracker.go`, `redis.go`, `filesystem.go`, `migrator.go`.
- **`internal/services/`** — cleanup, upload lifecycle, device identity, CNS client, user cache, stats reporter/tracker, Discord notifications.
- **`internal/models/`** — data types (`models.go`, `transfer.go`, `tunnel.go`, `user.go`, `desktop.go`) and `AppError` values.
- **`internal/i18n/`** — `en.json` / `de.json`, embedded via `go:embed`.
- **`internal/integration/`** — scenario contracts and live integration tests.
- **`web/templates/`** — Go `html/template` pages; `partials.html` holds shared pieces such as the account menu.
- **`web/static/`** — CSS (`css/style.css`, one file, page-scoped via `body.page-*` classes), JS, fonts, images, favicon, manifest, wordlist.
- **`db/migrations/`** — numbered SQL migrations (`NNN-description.sql`), applied in filename order.
- **`docs/`** — `ARCHITECTURE`, `CONFIGURATION`, `DATA_MODEL`, `OPERATIONS`, `SECURITY`, and `docs/api/{WEB,DESKTOP,MOBILE}.md`.

## Three API surfaces

| Prefix | Auth | Key quirk |
|--------|------|-----------|
| `/api` | Cookie-based CNS auth (optional) + CSRF (`X-CSRF-Token`) | CSRF token issued as the `csrf_token` cookie by page routes |
| `/desktop` | CORS wildcard + `X-API-KEY` or Bearer token | Every OPTIONS route is registered explicitly; add one for each new desktop route |
| `/android` | Bearer token via `AndroidAuthMiddleware` | Documented as "mobile" (`docs/api/MOBILE.md`); the runtime path is `/android` |

Page routes (`/`, `/link`, `/quickshare`, `/shared/:id`, `/transfers`, `/help`, …) and `/auth/*` (login, callback, logout, refresh) are registered on the root router. `/transfers` redirects anonymous visitors to login.

## Rate limiting

Three limiters, state in Redis, tuned with `RATE_LIMIT_*` env vars:

| Tier | Default | Applied to (examples) |
|------|---------|------------|
| Standard | 30 req/min | upload init/finalize, share-to-user, user lookup, identity-key lookup, websockets on desktop/android |
| Strict | 15 req/min | report, tunnel join, device enrollments approve/reject/create, device rename |
| Download | 60 req/min | file downloads |

## Storage architecture

- **PostgreSQL** — persistent metadata: files, devices, key envelopes, identity keys, tunnels and participants, enrollments, transfers, reports, the CNS user cache, `schema_migrations`.
- **Redis** — transient state: upload sessions, chunk tracking, pending flags, assembly status, rate-limit counters.
- **Filesystem** — encrypted file blobs (`DATA_DIR`) and temporary chunks (`CHUNK_DIR`).

## Encryption model

Files are encrypted in the browser/client; the server never sees plaintext or file keys (DEKs).
- Each signed-in user has an **identity keypair** (`user_identity_keys`); its private key is wrapped per trusted device (`user_identity_key_device_envelopes`).
- A file's DEK is wrapped per recipient in `file_access_key_envelopes` (`access_kind` = `owner` or `share`).
- New devices must be approved from a trusted device (enrollments) or the account must be recovered. Recovery replaces the identity key, so files wrapped for the old key show as "locked".

## Upload lifecycle

1. `POST /api/upload/init` — creates session, returns `session_id`
2. `POST /api/upload/chunk` — upload chunk (repeat N times)
3. `POST /api/upload/complete` — triggers async assembly
4. `GET /api/upload/status/:session_id` — poll until assembled
5. `POST /api/upload/finalize` — sets duration/tunnel, persists metadata, returns share URL
6. `DELETE /api/upload/cancel` — aborts

## Transfers (user-to-user sends)

- `POST /api/file/:id/share-to-user` (owner only) creates a `file_transfers` row (`pending`) plus the recipient's `share` envelope, atomically. A file can go to a given user only once, even after a decline.
- The recipient accepts or declines via `POST /api/me/transfers/:file_id/{accept,decline}`. The key is withheld until accepted; declining deletes the envelope.
- The recipient of an accepted transfer can report its file via `POST /api/me/transfers/:file_id/report`. It goes through the same code path as link-share reports (`ReportHandler.reportFile`) and sets `reports.transfer_id`.
- `GET /api/me/transfers?view=pending|history&direction=all|received|sent` backs the `/transfers` page. History shows received transfers after they're answered, plus every sent transfer.
- Live updates go over the per-user websocket `/api/me/devices/ws`: `transfers_updated` (recipient's pending count, drives the account-menu badge) and `sent_transfers_updated` (sender's history). `account-menu.js` owns the socket and re-dispatches `sendly:transfers-updated` / `sendly:sent-transfers-updated` window events. The same socket carries `device_enrollment_*` events, so consumers must filter on `type`.

## Localization

- UI strings live in `internal/i18n/en.json` and `de.json`. **Add every new key to both files.** Keep the existing key order and blank-line grouping: edit the lines directly rather than re-serializing the JSON.
- Locale comes from `?lang=` (persisted to the `lang` cookie), then the cookie, then `Accept-Language`.
- Templates read strings as `{{.t.key}}`. Page JS receives the whole map as `window.CONFIG.t` and uses a `t(key)` / `tpl(key, vars)` helper with `{placeholder}` substitution.

## Testing

- Unit tests: `internal/middleware/ip_test.go`, `internal/services/*_test.go`, `internal/integration/scenarios_test.go`. Run with `go test ./...`.
- Live tests (`internal/integration/live_*_test.go`, `internal/services/live_integration_test.go`) skip unless `SENDLY_LIVE_INTEGRATION=1`. They need real Postgres + Redis (config from env, same variables as the server) and create their own random users and files. Run them against a disposable database, not production. Test binaries run with the package dir as cwd, so set `MIGRATIONS_DIR` to an absolute path.
- The dev compose stack does not publish Postgres/Redis ports. To reach them, run a compiled test binary (`CGO_ENABLED=0 go test -c`) in a container on the stack's network.

## Key conventions and gotchas

- **Custom migration system** — not golang-migrate. Migrations run automatically on startup and are checksum-verified: **editing an applied migration is a fatal error**. Add a new numbered file instead, written idempotently (`IF NOT EXISTS`, `ON CONFLICT DO NOTHING`).
- **`*.md` is in `.gitignore`** — docs are force-added. Use `git add -f` for new or changed Markdown files. Gitignore-aware search tools (ripgrep, ugrep with ignore files) skip Markdown by default.
- **No formatter/linter config** — keep code `gofmt`-clean and matching the surrounding style.
- **Guest vs auth tiers** (`middleware/tiers.go`) — guest: 7d retention, `MAX_FILE_SIZE` (default 750 MB). Signed in with CNS: 90d, `AUTH_MAX_FILE_SIZE` (default 1.5 GB).
- **Auth is optional** — with `CNS_AUTH_*` unset, anonymous upload/download still works, but devices, transfers and signed-in quick-share features are unavailable.
- **CNS integration** — verify any CNS endpoint against the CNS API reference before relying on it; don't assume existing code proves it exists. The service-to-service surface is `GET /api/service/me` and the KV store `/api/data/{service}/`, both requiring `X-Service-Key` + `Authorization: Bearer`. User search (`/api/users/lookup`) is served from the local `users` cache table, not from CNS.
- **User cache** — `users` table mirrors known CNS users (username, avatar). It is refreshed on authenticated requests after `USER_CACHE_TTL_HOURS` and reconciled periodically. Queries that show other users `LEFT JOIN` it and fall back to empty values.
- **Background jobs** — file cleanup every 5 min (expire files, remove blobs and orphaned chunks); abandoned upload sessions every 1 min; user-cache reconcile every `USER_CACHE_RECONCILE_INTERVAL_MINUTES`; stats report every `STATS_REPORT_INTERVAL_MINUTES`.
- **Graceful shutdown** — 30s timeout on SIGINT/SIGTERM.
- **Frontend** — plain JS, no build step. Icons come from Lucide via unpkg: call `lucide.createIcons()` after inserting `data-lucide` elements. There is no dark mode; mobile breakpoints are in `style.css` media queries.
- **Docker compose prod override** adds the `/mnt/shareit` host mount for persistent file data.
- **Desktop WebSocket** at `/desktop/ws` pushes new-file notifications.
- **Wordlist** at `web/static/wordlist.txt` generates human-readable numeric codes.
