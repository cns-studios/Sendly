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
  - `POST /api/me/devices/identity-key/envelopes`
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

Reports are de-duplicated per signed-in user (or per client IP for anonymous reports); a repeat returns `409 ALREADY_REPORTED`. If the number of distinct signed-in reporters crosses `AUTO_DELETE_REPORT_COUNT`, the file is auto-marked deleted and the response message reflects auto-removal. Anonymous reports are recorded but never trigger auto-removal on their own. The endpoint uses the strict rate limiter.

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

## Transfers

User-to-user sends (`POST /api/file/:id/share-to-user`). The recipient accepts or declines; declining deletes their key envelope.

### `GET /api/me/transfers`
List the caller's transfers.

Query params:

- `view` (optional): `pending` (default) lists received transfers awaiting an answer whose file is still available; `history` lists past transfers, most recent activity (answer, else send) first.
- `direction` (optional, `history` only): `all` (default), `received` (accepted or declined transfers sent to the caller) or `sent` (every transfer the caller sent, including ones still pending). Anything else returns `400 INVALID_DIRECTION`.
- `page`, `per_page` (optional, as for recent uploads)

Each item carries `id`, `file_id`, `filename`, `size_bytes`, `expires_at`, `status` (`pending`/`accepted`/`declined`), `sent_at`, `responded_at`, `available`, `direction` (`received`/`sent`, relative to the caller), and both parties as `sender_user_id`/`sender_username`/`sender_avatar_url` and `recipient_user_id`/`recipient_username`/`recipient_avatar_url`.

### `GET /api/me/transfers/pending-count`
`{"count": n}`: received transfers awaiting an answer (the account menu badge).

### `POST /api/me/transfers/:file_id/accept`
### `POST /api/me/transfers/:file_id/decline`
Recipient only, once per transfer. `404 TRANSFER_NOT_FOUND`, `409 TRANSFER_ALREADY_ANSWERED`, `410 TRANSFER_FILE_UNAVAILABLE` (accept only).

### `POST /api/me/transfers/:file_id/report`
Report the file of a transfer the caller received. Recipient only, and only once the transfer is accepted: `404 TRANSFER_NOT_FOUND` for anyone else, `409 TRANSFER_NOT_ACCEPTED` while pending or after a decline. Otherwise it behaves like `POST /api/file/:id/report` (same response, de-duplication, auto-delete threshold and strict rate limiter) and records the transfer on the report. History items from `GET /api/me/transfers` carry `reported: true` once the caller has reported the file.

## Tunnels

Caller authentication for all tunnel endpoints below:

- Host: the initiating CNS user, or for a guest-started tunnel the `host_token` returned by `start`, sent as `X-Host-Token`.
- Participant: a signed-in caller by CNS user; an anonymous joiner by `X-Device-ID` plus the `participant_token` returned by `join`, sent as `X-Participant-Token`.

Non-members get `403 TUNNEL_FORBIDDEN`. Joiners must be approved by the host; until then they only see the lobby (tunnel, participants), and file lists, file access, uploads and key envelopes return `403 PARTICIPANT_NOT_APPROVED` (`409` from `peer-wrap-key` / cross-account finalize).

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
  "code": "1234",
  "device_id": "device-id (required for anonymous joiners)",
  "public_key_jwk": { "kty": "RSA", "n": "...", "e": "AQAB" },
  "key_algorithm": "RSA-OAEP-2048",
  "key_version": 1
}
```

Anonymous joiners receive `participant_token` in the response (returned once). Re-joining with a `device_id` that already belongs to another participant returns `409 PARTICIPANT_CONFLICT`; the owner can re-join (same CNS user, or the same `X-Participant-Token`) to replace its key. Rate-limited with the strict limiter.

### `POST /api/me/tunnels/:id/participants/:participant_id/approve`
Host only. Admit a joiner so the host can wrap the session key for it.

### `POST /api/me/tunnels/:id/participants/:participant_id/reject`
Host only. Remove a joiner together with its key envelope and peer assignment.

### `GET /api/me/tunnels/:id/participant-keys`
Host only. Participants with public keys, including `approved` and `has_envelope`.

### `POST /api/me/tunnels/:id/envelopes`
Host only. Store the session key wrapped for an approved participant's device. Returns `403 PARTICIPANT_NOT_APPROVED` for unapproved participants and `409 ENVELOPE_EXISTS` if one is already stored.

### `GET /api/me/tunnels/:id/envelopes/:device_id`
The participant's own envelope (`:device_id` must be the caller's device).

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
Leave the tunnel (removes only the caller). When the last participant leaves, the tunnel and its stored file blobs are deleted.

## Devices and Enrollment

### `POST /api/me/devices/register`
Register device or bootstrap trust. A `device_id` registered to another account returns `409 DEVICE_ID_CONFLICT`.

A device without an identity key sends a freshly generated one (`identity_public_key_jwk` plus its self-wrapped private key). The first device to do so creates the account's identity key. After that, a device's copy is only stored if its public key matches the account's key; otherwise the device gets no `identity_key_envelope` and waits for a sibling device to wrap the real key for it (see below).

Response fields besides `device_id`, `needs_enrollment` and the envelopes:

- `identity_public_key`: `{ "key_version", "key_algorithm", "public_key_jwk" }` of the account's active identity key. Clients keep a local identity private key only if it belongs to this public key.
- `devices_missing_identity_key`: `[{ "device_id", "public_key_jwk" }]`, the account's other trusted devices without a copy of the identity key. Only sent to a device that holds one.

Set `discard_identity_key_envelope: true` to drop this device's stored copy when it doesn't belong to `identity_public_key`; the device is then listed as missing the key again.

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
- `transfers_updated` (`{"pending_count": n}`): the caller's received pending transfers changed
- `sent_transfers_updated` (`{"file_id": "...", "status": "..."}`): a transfer the caller sent was created, accepted or declined

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

### `POST /api/me/devices/identity-key/envelopes`
Store copies of the identity private key that a trusted device holding it wrapped for the devices listed in its `devices_missing_identity_key`. Only devices without a copy are filled; an existing copy is never replaced. Returns `403 IDENTITY_KEY_NOT_HELD` if the sending device has no copy itself.

Request JSON:

```json
{
  "device_id": "sending-device-id",
  "envelopes": [
    {
      "device_id": "target-device-id",
      "identity_key_version": 1,
      "wrapped_private_key_b64": "...",
      "wrap_alg": "RSA-OAEP-2048+AES-GCM-256-v1",
      "wrap_meta": {}
    }
  ]
}
```

Response JSON: `{ "stored": 1 }`

## Notes

- `/api` endpoints assume browser session context and CSRF controls.
- For non-browser clients, use `/desktop` or `/android` (`/mobile`) surfaces.
