# ShareIt

ShareIt is an encrypted file sharing backend with three client surfaces:

- Browser API: `/api`
- Desktop API: `/desktop`
- Mobile API: `/android`

## Quick Start

Prerequisites:

- Go 1.18+
- PostgreSQL 12+
- Redis 6+

Run locally:

```bash
go mod download
cp .env.example .env
go run cmd/server/main.go
```

## Configuration Snapshot

Important env variables:

```env
PORT=8085
BASE_URL=http://localhost:8085

POSTGRES_HOST=localhost
POSTGRES_PORT=5432
POSTGRES_USER=shareit
POSTGRES_PASSWORD=changeme
POSTGRES_DB=shareit

REDIS_HOST=localhost
REDIS_PORT=6379

DATA_DIR=./data
CHUNK_DIR=

MAX_FILE_SIZE=786432000
AUTH_MAX_FILE_SIZE=1610612736
AUTO_DELETE_REPORT_COUNT=3

CNS_AUTH_URL=
CNS_AUTH_CLIENT_ID=
CNS_AUTH_DESKTOP_CLIENT_ID=
CNS_AUTH_SERVICE_KEY=

DISCORD_WEBHOOK_URL=
MIGRATIONS_DIR=db/migrations

RATE_LIMIT_MAX_PER_MINUTE=2
RATE_LIMIT_WINDOW_SECONDS=60
RATE_LIMIT_STRICT_MAX_PER_MINUTE=1
RATE_LIMIT_STRICT_WINDOW_SECONDS=60
RATE_LIMIT_DOWNLOAD_MAX_PER_MINUTE=10
RATE_LIMIT_DOWNLOAD_WINDOW_SECONDS=60
```

## Documentation Hub

## API Docs (Detailed)

- [Web API (`/api`)](docs/api/WEB.md)
- [Desktop API (`/desktop`)](docs/api/DESKTOP.md)
- [Mobile API (`/mobile` docs, runtime `/android`)](docs/api/MOBILE.md)

## Backend Docs (Quick Explanations + Deep Links)

- [Architecture](docs/ARCHITECTURE.md)
  - Component map, request flow, background workers, and websocket channels.
- [Configuration](docs/CONFIGURATION.md)
  - Every env var, defaults, and behavior impact.
- [Security](docs/SECURITY.md)
  - Auth boundaries, authorization checks, abuse controls, and hardening notes.
- [Operations](docs/OPERATIONS.md)
  - Startup lifecycle, runtime checks, cleanup behavior, migration/rate-limit tuning.
- [Data Model](docs/DATA_MODEL.md)
  - Core entities across postgres, redis, and filesystem.

## Migrations

Migrations run automatically on startup from `MIGRATIONS_DIR`.

Manual run:

```bash
make migrate
```
