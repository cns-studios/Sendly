# ShareIt Web API (`/api`) Reference

This document describes the browser-oriented API surface under `/api`.

## Base URL

- Local: `http://localhost:8085/api`

## Auth Model

The web API is intended for browser flows.

- Uses cookie-backed CNS auth (`auth_token`) when present.
- Requires CSRF middleware on the entire `/api` group.
- Send `X-CSRF-Token` for mutating requests.
- CSRF token is issued as `csrf_token` cookie by page routes like `/`, `/shared`, `/tos`, `/privacy`.

## Rate Limiting

Current middleware classes applied on `/api`:

- Standard limiter:
  - `POST /api/upload/init`
  - `POST /api/upload/finalize`
- Strict limiter:
  - `POST /api/me/devices/register`
  - `POST /api/me/devices/recover`
  - `POST /api/me/devices/enrollments`
  - `POST /api/me/devices/enrollments/:id/approve`
  - `POST /api/me/devices/enrollments/:id/reject`
- Download limiter:
  - `GET /api/file/:id/download`

All values are configured via environment variables (`RATE_LIMIT_*`).

## Error Envelope

Most failures return:

```json
{
  "error": "Human readable message",
  "code": "ERROR_CODE",
  "details": "Optional details"
}
```

## Endpoints

## Limits

### `GET /api/limits`
Returns effective limits for the current user tier.

Response:

```json
{
  "max_file_size": 786432000,
  "allowed_durations": ["24h", "7d"],
  "authenticated": false
}
```

## Upload

### `POST /api/upload/init`
Start a chunked upload session.

Request JSON:

```json
{
  "file_name": "example.zip",
  "file_size": 1048576,
  "total_chunks": 4,
  "chunk_size": 262144
}
```

Response JSON:

```json
{
  "session_id": "...",
  "file_id": "...",
  "chunk_size": 262144,
  "total_chunks": 4
}
```

### `POST /api/upload/chunk`
Upload one chunk.

Form fields:

- `session_id` (string)
- `chunk_index` (int, zero-based)
- `chunk` (file bytes)

Response JSON:

```json
{
  "success": true,
  "chunk_index": 0,
  "uploaded_chunks": 1,
  "total_chunks": 4
}
```

### `POST /api/upload/complete`
Mark upload complete and trigger assembly.

Request JSON:

```json
{
  "session_id": "...",
  "confirmed": true
}
```

Response JSON:

```json
{
  "session_id": "...",
  "file_id": "...",
  "pending_expires_at": "2026-04-18T12:34:56Z"
}
```

### `GET /api/upload/status/:session_id`
Get assembly status.

Response JSON:

```json
{
  "session_id": "...",
  "status": "pending"
}
```

Possible status values include states like `pending`, `done`, or error-prefixed status text.

### `POST /api/upload/finalize`
Finalize a completed upload.

Request JSON (regular upload):

```json
{
  "session_id": "...",
  "duration": "7d"
}
```

Request JSON (tunnel upload):

```json
{
  "session_id": "...",
  "tunnel_id": "...",
  "wrapped_dek_b64": "...",
  "dek_wrap_alg": "...",
  "dek_wrap_nonce_b64": "...",
  "dek_wrap_version": 1
}
```

Response JSON:

```json
{
  "file_id": "...",
  "numeric_code": "...",
  "share_url": "http://localhost:8085/shared/..."
}
```

### `DELETE /api/upload/cancel`
Cancel an upload session.

Request JSON:

```json
{
  "session_id": "..."
}
```

Response JSON:

```json
{
  "success": true
}
```

## File

### `GET /api/file/:id`
Get metadata for a file by ID.

### `GET /api/file/:id/download`
Stream encrypted file bytes (`application/octet-stream`).

Notable headers:

- `Content-Disposition: attachment; filename="<file_id>.enc"`
- `X-Original-Filename: <original file name>`

### `GET /api/file/code/:code`
Resolve metadata by numeric code.

### `POST /api/file/:id/report`
Report a file.

Response JSON:

```json
{
  "success": true,
  "message": "File has been reported. Thank you for helping keep our platform safe."
}
```

If reports cross threshold (`AUTO_DELETE_REPORT_COUNT`), file is auto-marked deleted and response message reflects auto-removal.

## Current User

## Recent Uploads and Access

### `GET /api/me/recent-uploads`
List owned files.

Query params:

- `page` (optional, default `1`)
- `per_page` (optional, default `10`, max `50`)
- `q` (optional, filename search)

Response JSON:

```json
{
  "items": [],
  "page": 1,
  "per_page": 10,
  "total": 0,
  "total_pages": 0,
  "query": ""
}
```

### `GET /api/me/files/:id/access?device_id=<device_id>`
Return wrapped file key envelope plus wrapped user key envelope for a specific device.

## Tunnels

### `POST /api/me/tunnels/start`
Request JSON:

```json
{
  "duration": "30m",
  "device_id": "optional-device-id"
}
```

Rules:

- Duration must parse as Go-style duration string.
- Allowed range is 10 minutes to 12 hours.

### `POST /api/me/tunnels/join`
Request JSON:

```json
{
  "code": "123456",
  "device_id": "optional-device-id"
}
```

### `GET /api/me/tunnels/:id`
Get tunnel metadata and tunnel file list.

### `GET /api/me/tunnels/:id/files`
Get only tunnel files.

### `POST /api/me/tunnels/:id/confirm`
Request JSON:

```json
{
  "device_id": "optional-device-id"
}
```

### `DELETE /api/me/tunnels/:id`
End tunnel and delete associated stored file blobs.

## Devices and Enrollment

### `POST /api/me/devices/register`
Register device or bootstrap trust.

### `POST /api/me/devices/recover`
Recovery flow that resets trusted device state and provisions a new trusted envelope.

### `GET /api/me/devices/ws`
WebSocket for enrollment-related events.

Event payload shape:

```json
{
  "type": "device_enrollment_created",
  "enrollment": {},
  "request_device": {},
  "approver_device_id": "",
  "pending_count": 1
}
```

Event `type` values include:

- `device_enrollment_created`
- `device_enrollment_approved`
- `device_enrollment_rejected`

### `POST /api/me/devices/enrollments`
Create enrollment request.

Request JSON:

```json
{
  "request_device_id": "..."
}
```

Response JSON:

```json
{
  "enrollment_id": "...",
  "verification_code": "123456",
  "expires_at": "2026-04-18T12:34:56Z"
}
```

### `GET /api/me/devices/enrollments/pending`
List pending enrollments + request device metadata.

### `POST /api/me/devices/enrollments/:id/approve`
Approve enrollment and provide wrapped user key.

Request JSON:

```json
{
  "approver_device_id": "...",
  "verification_code": "123456",
  "wrapped_user_key_b64": "...",
  "uk_wrap_alg": "...",
  "uk_wrap_meta": {}
}
```

### `POST /api/me/devices/enrollments/:id/reject`
Reject enrollment.

Request JSON:

```json
{
  "approver_device_id": "..."
}
```

## Notes

- `/api` endpoints assume browser session context and CSRF controls.
- For non-browser clients, use `/desktop` or `/android` (`/mobile`) surfaces.
