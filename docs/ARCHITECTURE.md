# ShareIt Backend Architecture

This document gives a high-level map of ShareIt backend components and request flow.

## Runtime Stack

- Language: Go
- HTTP framework: Gin
- Database: PostgreSQL
- Cache/session/rate-limit store: Redis
- File storage: local filesystem
- Optional external integration: CNS auth service, Discord webhook

## Service Composition

Server bootstrap lives in `cmd/server/main.go` and wires:

- Config loading (`internal/config`)
- PostgreSQL (`internal/storage/postgres.go`)
- Redis (`internal/storage/redis.go`)
- Filesystem storage (`internal/storage/filesystem.go`)
- Cleanup service (`internal/services/cleanup.go`)
- Upload lifecycle service (`internal/services/upload.go`)

## Request Pipeline

Global middleware order:

1. Recovery + logger
2. Keep-alive headers
3. IP extraction middleware
4. CNS cookie auth bridge middleware

Route groups:

- `/api`: browser-oriented, CSRF protected
- `/desktop`: CORS-enabled desktop API with API-key or bearer auth
- `/android`: bearer-only mobile API (documented as mobile surface)

## Domain Areas

- Upload lifecycle: init, chunk, complete, finalize, cancel
- Download + metadata lookup
- File reporting and auto-delete threshold
- Trusted device registration and recovery
- Device enrollment approval workflow
- Temporary tunnel sessions for peer workflows

## Data Flow: Upload to Share Link

1. Client creates upload session (`init`).
2. Client uploads all chunks.
3. Client marks complete (`complete`) and server assembles chunks asynchronously.
4. Client finalizes (`finalize`) with duration or tunnel context.
5. File metadata is persisted, pending state is removed, share URL is returned.

## Background Work

### Cleanup service

Runs periodically to:

- Mark expired files deleted in DB
- Remove deleted file blobs from disk
- Remove orphaned chunk directories
- Remove orphaned files that are not present in DB

### Upload pending cleanup

Runs every minute to remove abandoned sessions and stale pending upload artifacts.

## Security Boundaries

- Browser safety: CSRF on `/api`
- Token validation via CNS bridge
- Device-level trust with envelope workflows
- Route-level rate limiting (standard, strict, download)
- Ownership checks for file and tunnel access

## WebSocket Channels

- Desktop: `/desktop/ws` new-file push channel
- Browser/desktop device events: `/api|/desktop ... /devices/ws`
- Mobile channels:
  - `/android/me/devices/ws`
  - `/android/me/devices/ws/pending-approvals`
  - `/android/me/devices/enrollments/:id/ws`

## Storage Model Summary

- PostgreSQL stores persistent metadata (files, devices, tunnels, enrollments, reports).
- Redis stores transient operational state (upload sessions, chunk tracking, pending flags, rate-limit counters).
- Filesystem stores encrypted file blobs and temporary chunk fragments.
