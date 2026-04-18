# ShareIt Desktop API (`/desktop`) Reference

This document describes the desktop client API under `/desktop`.

## Base URL

- Local: `http://localhost:8085/desktop`

## CORS and Preflight

Desktop routes include permissive CORS middleware.

- `Access-Control-Allow-Origin: *`
- `Access-Control-Allow-Methods: GET, POST, DELETE, OPTIONS`
- `Access-Control-Allow-Headers: Content-Type, X-API-KEY, Authorization`

`OPTIONS` is implemented for all published desktop routes.

## Auth Model

Desktop supports two auth modes.

- API key mode:
  - Provide `X-API-KEY` header or `key` query parameter.
- Bearer token mode:
  - Provide `Authorization: Bearer <token>` or `token` query parameter.
  - CNS token validation is performed through the auth bridge.

Endpoints under `/desktop` split into:

- Public desktop endpoints (`/auth/*`, `/ws`, `/limits`).
- Auth-required endpoints in the desktop auth group.

## Rate Limiting

Applied middleware on desktop routes:

- Standard limiter:
  - `POST /desktop/upload/finalize`
- Strict limiter:
  - `POST /desktop/me/devices/register`
  - `POST /desktop/me/devices/recover`
  - `POST /desktop/me/devices/enrollments`
  - `POST /desktop/me/devices/enrollments/:id/approve`
  - `POST /desktop/me/devices/enrollments/:id/reject`
- Download limiter:
  - `GET /desktop/files/:id/download`

## Error Envelope

```json
{
  "error": "Message",
  "code": "ERROR_CODE",
  "details": "Optional"
}
```

## Public Desktop Endpoints

### `GET /desktop/auth/verify?key=<api_key>`
Validate API key and return owner metadata.

Response:

```json
{
  "status": "valid",
  "owner": "owner-name"
}
```

### `GET /desktop/auth/oauth/config`
Returns OAuth bridge details used by desktop auth flow.

Response:

```json
{
  "auth_url": "https://auth.example.com",
  "client_id": "client-id"
}
```

### `GET /desktop/auth/oauth/verify`
Validate CNS bearer token.

Required header:

- `Authorization: Bearer <token>`

Response:

```json
{
  "status": "valid",
  "owner": "username",
  "cns_user": {
    "id": 123,
    "username": "username"
  }
}
```

### `GET /desktop/ws`
Desktop websocket for real-time new-file notifications.

Auth options:

- `?token=<bearer>`
- `Authorization: Bearer <token>`
- `?key=<api_key>`

Event payload:

```json
{
  "type": "new_file",
  "file": {
    "id": "...",
    "numeric_code": "...",
    "file_name": "...",
    "file_size": 123,
    "expires_at": "...",
    "uploaded_at": "..."
  },
  "source_device_id": "optional-device-id"
}
```

### `GET /desktop/limits`
Returns effective user tier limits (`max_file_size`, `allowed_durations`, `authenticated`).

## Auth Group Endpoints

The following require API key or bearer auth.

## Upload

### `POST /desktop/upload/init`
Request JSON:

```json
{
  "file_name": "file.bin",
  "file_size": 12345,
  "total_chunks": 3,
  "chunk_size": 4096
}
```

Response JSON:

```json
{
  "session_id": "...",
  "file_id": "...",
  "chunk_size": 4096,
  "total_chunks": 3,
  "api_key_id": "optional"
}
```

### `POST /desktop/upload/chunk`
Multipart chunk upload.

Form fields:

- `session_id`
- `chunk_index`
- `chunk`

### `POST /desktop/upload/complete`
Request JSON:

```json
{
  "session_id": "...",
  "confirmed": true
}
```

### `POST /desktop/upload/finalize`
Request JSON (standard):

```json
{
  "session_id": "...",
  "duration": "7d"
}
```

Request JSON (tunnel + device trust):

```json
{
  "session_id": "...",
  "tunnel_id": "...",
  "device_id": "...",
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
  "file_name": "optional",
  "file_size": 123,
  "expires_at": "optional",
  "share_url": "http://localhost:8085/shared/..."
}
```

### `GET /desktop/upload/status/:session_id`
Returns assembly status for session.

### `DELETE /desktop/upload/cancel`
Cancel upload.

Request JSON:

```json
{
  "session_id": "..."
}
```

## Files

### `GET /desktop/files`
List files visible to current auth identity.

- Bearer mode: files owned by user.
- API key mode: files associated with key.

### `GET /desktop/files/:id`
Get metadata for one file.

### `GET /desktop/files/:id/download`
Stream encrypted file bytes.

Headers include:

- `Content-Disposition: attachment; filename="<file_id>.enc"`
- `X-Original-Filename`

## File Lookup and Report

### `GET /desktop/file/code/:code`
Lookup file metadata by numeric code.

### `POST /desktop/file/:id/report`
Report file.

## Current User

### `GET /desktop/me/recent-uploads`
Paginated recent owned uploads.

### `GET /desktop/me/files/:id/access?device_id=<device_id>`
Get envelope material needed for secure download/decrypt workflows.

## Tunnels

### `POST /desktop/me/tunnels/start`
Create tunnel.

### `POST /desktop/me/tunnels/join`
Join tunnel by short code.

### `GET /desktop/me/tunnels/:id`
Get tunnel and files.

### `GET /desktop/me/tunnels/:id/files`
Get tunnel file list.

### `POST /desktop/me/tunnels/:id/confirm`
Confirm tunnel participation.

### `DELETE /desktop/me/tunnels/:id`
End tunnel and remove tunnel files.

## Devices and Enrollment

### `POST /desktop/me/devices/register`
Register/update device.

### `POST /desktop/me/devices/recover`
Recovery flow that revokes existing trusted devices and creates fresh trusted state.

### `GET /desktop/me/devices/ws`
WebSocket for enrollment events.

Payload:

```json
{
  "type": "device_enrollment_created",
  "enrollment": {},
  "request_device": {},
  "approver_device_id": "",
  "pending_count": 1
}
```

### `POST /desktop/me/devices/enrollments`
Create enrollment request.

### `GET /desktop/me/devices/enrollments/pending`
List pending enrollment requests.

### `POST /desktop/me/devices/enrollments/:id/approve`
Approve enrollment and attach wrapped user key.

### `POST /desktop/me/devices/enrollments/:id/reject`
Reject enrollment.

## Notes

- `/desktop` is ideal for trusted desktop clients and supports mixed API-key and bearer auth.
- For browser session behavior, use `/api`.
- For mobile-native bearer-only behavior, use `/android` (`/mobile`).
