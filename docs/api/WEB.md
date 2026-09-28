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

Request JSON (tunnel upload): `tunnel_id` instead of `duration`.

Signed-in uploads must add the uploader's own copy of the file key, wrapped with their active identity public key (`400 IDENTITY_ENVELOPE_REQUIRED` without it, `400 IDENTITY_KEY_STALE` for another version):

```json
{
  "identity_wrapped_dek_b64": "...",
  "identity_dek_wrap_alg": "RSA-OAEP-2048-v1",
  "identity_dek_wrap_version": 1,
  "identity_key_version": 1
}
```

A guest's quick share upload may instead carry its copy wrapped for its throwaway participant key (`wrapped_dek_b64`, `dek_wrap_alg`, `dek_wrap_nonce_b64`, `dek_wrap_version`); these fields are ignored for signed-in uploads.

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

### `GET /api/me/files/:id/access`
Return the caller's copy of a file key, wrapped with their identity public key: their own upload's (`access_kind: "owner"`) or one sent to them in an accepted transfer (`"share"`).

```json
{
  "file": {},
  "file_access_key_envelope": { "wrapped_dek_b64": "...", "dek_wrap_alg": "RSA-OAEP-2048-v1", "dek_wrap_version": 1 },
  "identity_key_version": 1,
  "access_kind": "owner"
}
```

The key opens only with the identity key version it was wrapped for; a device holding a newer version (after recovery) treats the file as locked. `404 ACCESS_DENIED` if the file is gone or the caller has no copy.

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

Non-members get `403 TUNNEL_FORBIDDEN`, a joiner the host declined gets `403 PARTICIPANT_REJECTED`. Joiners must be approved by the host; until then they only see the lobby (tunnel, participants), and file lists, file access, uploads and key envelopes return `403 PARTICIPANT_NOT_APPROVED`.

Keys: the host's browser generates a session password and wraps it for each approved participant's throwaway public key (`POST /api/me/tunnels/:id/envelopes`); every file in the session is encrypted with it. A signed-in uploader also stores its own copy, wrapped with its identity key (`identity_*` fields on finalize); a guest uploader's copy is wrapped for its throwaway key (`wrapped_dek_*`).

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

Anonymous joiners receive `participant_token` in the response (returned once). Re-joining with a `device_id` that already belongs to another participant returns `409 PARTICIPANT_CONFLICT`; the owner can re-join (same CNS user, or the same `X-Participant-Token`) to replace its key. A joiner the host declined gets `403 PARTICIPANT_REJECTED` (checked before the tunnel's state, so it also applies after the start); otherwise an active tunnel returns `410 TUNNEL_ALREADY_ACTIVE`. Rate-limited with the strict limiter.

### `POST /api/me/tunnels/:id/participants/:participant_id/approve`
Host only. Admit a joiner so the host can wrap the session key for it.

### `POST /api/me/tunnels/:id/participants/:participant_id/reject`
Host only. Remove a joiner together with its key envelope, and block its CNS user or device from joining this tunnel again (`tunnel_rejections`).

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
Host only. Starts the session: the tunnel becomes `active` and every approved participant moves into it. Anyone else gets `403 TUNNEL_NOT_AVAILABLE`.

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
Register this device and learn whether it holds the account's identity key, the account's only root secret. A `device_id` registered to another account returns `409 DEVICE_ID_CONFLICT`.

Request JSON: `device_id`, `device_label`, `public_key_jwk`, `key_algorithm`, `key_version`, and, only when creating an identity key, `identity_public_key_jwk`, `identity_key_algorithm`, `wrapped_identity_private_key_b64` (the new private key wrapped for this device's public key), `identity_key_wrap_alg`, `identity_key_wrap_meta`.

Response JSON: `device_id`, and either

- `identity_key_envelope` (`{ "wrapped_private_key_b64", "wrap_alg", "wrap_meta", "identity_key_version" }`, this device's copy) and `identity_public_key` (`{ "key_version", "key_algorithm", "public_key_jwk" }`, the account's active key): the device is trusted; or
- `needs_enrollment: true`: the account has an identity key this device doesn't hold; another device has to approve it; or
- `needs_identity_setup: true`: a brand-new account; register again with a new identity keypair, which becomes version 1; or
- `needs_identity_migration: true`: the account predates identity keys; a device holding its legacy user key has to migrate it.

### `POST /api/me/devices/recover`
For an account with no device left to approve this one. Same request as `register`, and a new identity keypair is required (`400 IDENTITY_KEY_REQUIRED` otherwise). It becomes the account's next identity key version; the previous version is retired, every other device is revoked, and their pending approval requests expire. Files and transfers wrapped for an older version stay locked until a device still holding that version re-wraps them.

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
Approve enrollment by handing over the identity key: the approving (trusted) device wraps the identity private key for the requesting device's public key.

Request JSON:

```json
{
  "approver_device_id": "...",
  "verification_code": "123456",
  "wrapped_identity_private_key_b64": "...",
  "identity_key_wrap_alg": "RSA-OAEP-2048+AES-GCM-256-v1",
  "identity_key_wrap_meta": {},
  "identity_key_version": 1
}
```

`409 IDENTITY_KEY_STALE` if `identity_key_version` isn't the account's active version; `403 APPROVER_NOT_TRUSTED` if the approver doesn't hold it.

### `POST /api/me/devices/enrollments/:id/reject`
Reject enrollment.

Request JSON:

```json
{
  "approver_device_id": "..."
}
```

## Identity Key Rescue

After a recovery, file keys wrapped for an older identity key version are locked. A browser that still holds such a version (typically a device that was revoked by the recovery) re-wraps them for the active version. Any signed-in session may call these; see the trust note in SECURITY.md.

### `GET /api/me/identity-rescue/locked?version=<n>&after=<file_id>`
The caller's file keys wrapped for identity key `version` (older than the active one), 100 per page ordered by file id: `{ "items": [{ "file_id", "wrapped_dek_b64", "dek_wrap_alg" }], "remaining": n }`.

### `POST /api/me/identity-rescue`
`{ "from_version": 1, "to_version": 2, "items": [{ "file_id", "identity_wrapped_dek_b64", "identity_dek_wrap_alg" }] }`. Replaces only keys that are still locked to `from_version`, and only when `to_version` is the active version; returns `{ "rescued": n }`.

## Identity Migration (temporary)

Moves accounts from before identity keys (legacy AES user key in `user_key_envelopes`) onto them. All crypto happens in the browser (`identity-migration.js`). Removed once every account is migrated.

- `GET /api/me/identity-migration/legacy-key?device_id=` — this device's copy of the legacy user key (`404 LEGACY_USER_KEY_NOT_FOUND` if it has none).
- `POST /api/me/identity-migration/start` — from a device holding the legacy user key, for an account without an identity key: creates identity key version 1 with the device's self-wrapped copy and the escrow (the identity private key encrypted with the user key). `409 IDENTITY_KEY_EXISTS` if another device was first.
- `GET /api/me/identity-migration/escrow?device_id=` — the escrow, to a device holding the legacy user key, if it is for the active identity key.
- `POST /api/me/identity-migration/adopt` — a legacy device stores its own copy of the identity key, decrypted from the escrow; no approval needed.
- `GET /api/me/identity-migration/files?device_id=&after=` — trusted device only: the caller's uploads whose key is still only wrapped with the legacy user key.
- `POST /api/me/identity-migration/files` — trusted device only: their keys re-wrapped for the active identity key; files that already have one are left alone.

## Notes

- `/api` endpoints assume browser session context and CSRF controls.
- For non-browser clients, use `/desktop` or `/android` (`/mobile`) surfaces.
