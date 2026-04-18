# ShareIt Mobile API (`/mobile`) Reference

This document describes the mobile app API.

Current backend namespace is `/android`. If your client code calls this surface as "mobile", map `/mobile/*` documentation entries to `/android/*` routes.

## Base URL

- Effective runtime base: `http://localhost:8085/android`
- Documentation naming base: `/mobile` (mapped to `/android`)

## Auth Model

Mobile is bearer-only in this backend.

- Header: `Authorization: Bearer <token>`
- Optional query fallback for websocket/HTTP tooling: `?token=<token>`
- No CSRF requirements on this surface.

## Rate Limiting

Applied middleware classes:

- Standard limiter:
  - `POST /android/upload/init`
  - `POST /android/upload/finalize`
- Strict limiter:
  - `POST /android/me/devices/register`
  - `POST /android/me/devices/recover`
  - `POST /android/me/devices/:id/rename`
  - `POST /android/me/devices/enrollments`
  - `POST /android/me/devices/enrollments/:id/approve`
  - `POST /android/me/devices/enrollments/:id/reject`
- Download limiter:
  - `GET /android/files/:id/download`

## Error Envelope

```json
{
  "error": "Message",
  "code": "ERROR_CODE",
  "details": "Optional"
}
```

## Endpoints

## Upload

### `POST /android/upload/init`
Start chunked upload session.

Request JSON:

```json
{
  "file_name": "video.mp4",
  "file_size": 52428800,
  "total_chunks": 200,
  "chunk_size": 262144
}
```

Response JSON:

```json
{
  "session_id": "...",
  "file_id": "...",
  "chunk_size": 262144,
  "total_chunks": 200
}
```

### `POST /android/upload/chunk`
Upload one chunk.

Multipart fields:

- `session_id`
- `chunk_index`
- `chunk`

Response JSON:

```json
{
  "success": true,
  "chunk_index": 4,
  "uploaded_chunks": 5,
  "total_chunks": 200
}
```

### `POST /android/upload/complete`
Confirm upload completion.

Request JSON:

```json
{
  "session_id": "...",
  "confirmed": true
}
```

### `POST /android/upload/finalize`
Finalize upload and issue share info.

Request JSON (standard):

```json
{
  "session_id": "...",
  "duration": "7d"
}
```

Request JSON (tunnel + envelope):

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
  "share_url": "http://localhost:8085/shared/..."
}
```

## Files

### `GET /android/files`
List owned files (up to 50 items).

Response item fields:

- `file_id`
- `filename`
- `size_bytes`
- `created_at`
- `expires_at`
- `share_url`

### `GET /android/files/:id`
Get metadata for an owned file.

### `GET /android/files/:id/download`
Download encrypted file bytes.

Headers:

- `Content-Type: application/octet-stream`
- `Content-Disposition: attachment; filename="<file_id>.enc"`
- `X-Original-Filename`

## Current User

### `GET /android/me/recent-uploads`
Alias to owned file list.

### `GET /android/me/files/:id/access?device_id=<device_id>`
Return secure envelope material for file access.

## Devices

### `POST /android/me/devices/register`
Register device and attempt trust resolution.

Possible outcomes:

- `needs_enrollment: false` with `user_key_envelope`
- `needs_enrollment: true` if trusted device approval required

### `POST /android/me/devices/recover`
Recovery flow requiring wrapped user key; resets trusted state.

### `GET /android/me/devices`
List connected active devices for the account.

### `POST /android/me/devices/:id/rename`
Rename a connected device.

Request JSON:

```json
{
  "device_label": "My Pixel"
}
```

Response JSON:

```json
{
  "success": true,
  "device": {
    "id": "...",
    "device_label": "My Pixel"
  }
}
```

## Device Enrollment

### `POST /android/me/devices/enrollments`
Create new enrollment challenge.

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

### `GET /android/me/devices/enrollments/pending`
List pending enrollments and request device metadata.

### `POST /android/me/devices/enrollments/:id/approve`
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

### `POST /android/me/devices/enrollments/:id/reject`
Reject enrollment.

Request JSON:

```json
{
  "approver_device_id": "..."
}
```

## WebSockets

All websocket endpoints require mobile auth context.

## `GET /android/me/devices/ws`
General enrollment event stream for authenticated user.

Event example:

```json
{
  "type": "device_enrollment_created",
  "enrollment": {},
  "request_device": {},
  "approver_device_id": "",
  "pending_count": 1
}
```

`type` values include:

- `device_enrollment_created`
- `device_enrollment_approved`
- `device_enrollment_rejected`

## `GET /android/me/devices/ws/pending-approvals`
Pending approvals stream.

Initial snapshot event:

```json
{
  "type": "pending_approvals_snapshot",
  "pending_count": 2
}
```

Update event:

```json
{
  "type": "pending_approvals_updated",
  "pending_count": 1,
  "enrollment": {},
  "request_device": {}
}
```

## `GET /android/me/devices/enrollments/:id/ws`
Waiting-for-approval status stream for one enrollment.

Initial snapshot event:

```json
{
  "type": "enrollment_status_snapshot",
  "enrollment_id": "...",
  "status": "pending",
  "expires_at": "...",
  "request_device_id": "..."
}
```

Update event:

```json
{
  "type": "enrollment_status",
  "enrollment": {},
  "approver_device_id": "..."
}
```

## Notes

- Mobile surface is designed for token-authenticated native clients.
- Use this API for connected device lifecycle and trust-enrollment UX.
- Tunnel operations for mobile currently use the shared `/api` and `/desktop` tunnel handlers on authenticated surfaces.
