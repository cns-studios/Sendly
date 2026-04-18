# ShareIt Backend Configuration

This document lists runtime configuration and behavior impact.

## Core Server

- `PORT` (default `8085`): HTTP listen port.
- `BASE_URL` (default `http://localhost:8085`): external URL used for generated links and auth callbacks.
- `TOS_VERSION`: version identifier exposed to clients.

## PostgreSQL

- `POSTGRES_HOST`
- `POSTGRES_PORT`
- `POSTGRES_USER`
- `POSTGRES_PASSWORD`
- `POSTGRES_DB`

Used to build DSN for metadata persistence.

## Redis

- `REDIS_HOST`
- `REDIS_PORT`

Used for upload sessions, chunk tracking, pending flags, assembly status, and rate limits.

## File Storage

- `DATA_DIR` (required)
- `CHUNK_DIR` (optional)

`CHUNK_DIR` can separate temporary chunk storage from final file storage paths.

## Auth and Identity

- `CNS_AUTH_URL`
- `CNS_AUTH_CLIENT_ID`
- `CNS_AUTH_DESKTOP_CLIENT_ID`
- `CNS_AUTH_SERVICE_KEY`

Notes:

- Browser flow uses `/auth/login` and `/auth/callback` PKCE exchange.
- Desktop and mobile bearer validation uses CNS token validation bridge.

## Tier and File Limits

- `MAX_FILE_SIZE`: guest-tier max upload size.
- `AUTH_MAX_FILE_SIZE`: authenticated-tier max upload size.

Durations:

- Guest: `24h`, `7d`
- Authenticated: `24h`, `7d`, `30d`, `90d`

## Reporting and Moderation

- `AUTO_DELETE_REPORT_COUNT` (default `3`)

When report count reaches threshold, file is marked deleted.

## Notifications

- `DISCORD_WEBHOOK_URL` (optional)

Used for report and auto-delete notifications.

## Migrations

- `MIGRATIONS_DIR` (default `db/migrations`)

Migrations run at startup and are tracked in schema migration history.

## Deployment Mode

- `GIN_MODE=release` enables production mode behavior.
- `BEHIND_CLOUDFLARE` controls client IP extraction behavior.

## Rate Limiting

Standard limiter:

- `RATE_LIMIT_MAX_PER_MINUTE` (default `2`)
- `RATE_LIMIT_WINDOW_SECONDS` (default `60`)

Strict limiter:

- `RATE_LIMIT_STRICT_MAX_PER_MINUTE` (default `1`)
- `RATE_LIMIT_STRICT_WINDOW_SECONDS` (default `60`)

Download limiter:

- `RATE_LIMIT_DOWNLOAD_MAX_PER_MINUTE` (default `10`)
- `RATE_LIMIT_DOWNLOAD_WINDOW_SECONDS` (default `60`)

## Recommended Baselines

Development baseline:

- Keep defaults and use local postgres/redis containers.

Production baseline:

- Set strong DB credentials.
- Set `BASE_URL` to public HTTPS origin.
- Set `GIN_MODE=release`.
- Tune rate-limit values to expected traffic.
- Configure `CNS_AUTH_*` and Discord webhook if needed.
