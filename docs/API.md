# Sendly API Reference

The HTTP API used by the Sendly web app, plus the auth, page and health routes around it. It is served by the Go backend (`cmd/server/main.go`); the handlers live in `internal/handlers`.

Sendly is end-to-end encrypted: every key, ciphertext and wrapped key in this API is produced in the browser. The server stores and relays them but cannot read them (see [SECURITY.md](SECURITY.md)). Field names ending in `_b64` are standard base64.

> Other client surfaces (`/desktop`, `/android`) exist in the backend but are intentionally not documented here.

## Contents

1. [Conventions](#conventions)
2. [Auth routes](#auth-routes-auth)
3. [Limits](#limits)
4. [Upload](#upload)
5. [Files](#files)
6. [Reports](#reports)
7. [Your files and sharing](#your-files-and-sharing)
8. [Transfers](#transfers)
9. [Quick share tunnels](#quick-share-tunnels)
10. [Devices and enrollment](#devices-and-enrollment)
11. [Identity key rescue](#identity-key-rescue)
12. [Identity migration (temporary)](#identity-migration-temporary)
13. [Health, pages and static routes](#health-pages-and-static-routes)
14. [Error codes](#error-codes)

## Conventions

**Base URL:** `BASE_URL` (default `http://localhost:8085`). API routes live under `/api`.

**Authentication.** Sign-in goes through CNS Auth (OAuth2 + PKCE) and sets an `auth_token` cookie. Most endpoints work for guests; routes marked **auth** return `401 AUTH_REQUIRED` without a signed-in user. Quick share tunnel routes use their own caller model (host token / participant token), described in that section.

**CSRF.** Every `POST`, `PUT`, `PATCH` and `DELETE` under `/api` must send an `X-CSRF-Token` header equal to the `csrf_token` cookie, otherwise `403 CSRF_FORBIDDEN`. The cookie is issued by page routes (`/`, `/shared/:id`, `/quickshare`, `/tos`, `/privacy`, ...). Load a page first, then call the API.

**Rate limits.** Three limiter classes, counted per signed-in user, or per client IP for guests. A request over the limit gets `429` with `RATE_LIMITED` (`DOWNLOAD_RATE_LIMITED` on downloads). Values come from the `RATE_LIMIT_*` variables ([OPERATIONS.md](OPERATIONS.md#rate-limiting)); defaults are per 60 seconds.

| Class | Default | Applied to |
|---|---|---|
| Standard | 30 | `GET /api/users/lookup`, `GET /api/users/:id/identity-key`, `POST /api/upload/init`, `POST /api/upload/finalize`, `POST /api/file/:id/share-to-user`, `GET /api/me/identity-rescue/locked`, `POST /api/me/identity-rescue`, `GET`/`POST /api/me/identity-migration/files` |
| Strict | 15 | `POST /api/file/:id/report`, `POST /api/me/transfers/:file_id/report`, `POST /api/me/tunnels/join`, `POST /api/me/devices/enrollments`, `POST .../enrollments/:id/approve`, `POST .../enrollments/:id/reject`, `/api/me/identity-migration/*` except `files` |
| Download | 60 | `GET /api/file/:id/download` |

**Error envelope.** Failures return a JSON body with a human-readable `error`, a stable machine-readable `code`, and optional `details`:

```json
{ "error": "Human readable message", "code": "ERROR_CODE", "details": "optional" }
```

**Pagination.** List endpoints take `page` (default `1`) and `per_page` (default `10`, max `50`); invalid values return `400 INVALID_PAGINATION`. They answer with `{ items, page, per_page, total, total_pages }`; a `page` past the end is clamped to the last page.

**Identifiers.** A file has an `id` (opaque, used in `/shared/:id` links) and a short `numeric_code` that can be typed in by hand. Tunnels have a UUID `id` and a short join `code`.

## Auth routes (`/auth`)

Plain redirect/cookie routes, not JSON, and not CSRF protected. They require `CNS_AUTH_URL` and `CNS_AUTH_CLIENT_ID`.

| Route | Description |
|---|---|
| `GET /auth/login` | Starts the PKCE flow: sets short-lived `pkce_verifier`/`pkce_state` cookies and redirects to CNS Auth with an `S256` challenge, scope `openid profile` and `redirect_uri=<BASE_URL>/auth/callback`. |
| `GET /auth/callback` | Validates `state`, exchanges `code` + verifier for tokens, sets `auth_token`, `auth_expires_at`, `auth_avatar` and `refresh_token` cookies (HttpOnly), redirects to `/`. |
| `GET /auth/logout` | Best-effort revokes the refresh-token family at CNS, clears all auth cookies, redirects to `/`. Logout never fails because CNS is unreachable. |
| `POST /auth/refresh` | Uses the `refresh_token` cookie to obtain a new access token. `200 {"ok": true}` or `401 {"error": ...}`; a token CNS reports as dead is cleared. |

## Limits

### `GET /api/limits`
Effective limits for the caller's tier. No auth needed.

```json
{ "max_file_size": 786432000, "allowed_durations": ["7d"], "authenticated": false }
```

| Tier | Max size (default) | Allowed durations |
|---|---|---|
| Guest | `MAX_FILE_SIZE`, 750 MB | `7d` |
| Signed in | `AUTH_MAX_FILE_SIZE`, 1.5 GB | `90d` |

## Upload

A file is encrypted in the browser, cut into chunks, and sent through a session: **init → chunk (×N) → complete → (poll status) → finalize**. An unfinished session is discarded after 10 minutes.

### `POST /api/upload/init`
Starts a session. Files above the tier limit are rejected with `FILE_TOO_LARGE`.

```json
{ "file_name": "example.zip", "file_size": 1048576, "total_chunks": 4, "chunk_size": 262144, "tunnel_id": "optional" }
```

Response: `{ "session_id", "file_id", "chunk_size", "total_chunks" }`. Pass `tunnel_id` to upload into a quick share tunnel.

### `POST /api/upload/chunk`
`multipart/form-data` with `session_id`, `chunk_index` (zero-based) and `chunk` (bytes). Response: `{ "success": true, "chunk_index", "uploaded_chunks", "total_chunks" }`.

### `POST /api/upload/complete`
Declares all chunks sent and starts server-side assembly in the background.

```json
{ "session_id": "...", "confirmed": true }
```

Response: `{ "session_id", "file_id", "pending_expires_at" }`.

### `GET /api/upload/status/:session_id`
`{ "session_id", "status" }` where `status` is `pending`, `done`, or an error-prefixed string. `404 SESSION_NOT_FOUND` for unknown sessions.

### `POST /api/upload/finalize`
Turns the assembled file into a shareable one.

```json
{ "session_id": "...", "duration": "7d" }
```

Send `tunnel_id` instead of `duration` for a tunnel upload (lifetime then follows the tunnel). `duration` must be one of the tier's `allowed_durations`.

**Key envelope.** A signed-in uploader must also send their own copy of the file key, wrapped with their active identity public key (`400 IDENTITY_ENVELOPE_REQUIRED` without it, `400 IDENTITY_KEY_STALE` for the wrong version):

```json
{
  "identity_wrapped_dek_b64": "...",
  "identity_dek_wrap_alg": "RSA-OAEP-2048-v1",
  "identity_dek_wrap_version": 1,
  "identity_key_version": 1
}
```

A guest's quick share upload instead carries a copy wrapped for its throwaway participant key: `wrapped_dek_b64`, `dek_wrap_alg`, `dek_wrap_nonce_b64`, `dek_wrap_version`. These are ignored for signed-in uploads. Regular guest link uploads send no envelope: the key stays in the URL fragment.

Response: `{ "file_id", "numeric_code", "share_url" }`, where `share_url` is `<BASE_URL>/shared/<file_id>`.

### `DELETE /api/upload/cancel`
`{ "session_id": "..." }` → `{ "success": true }`. Removes the session and any chunks.

## Files

### `GET /api/file/:id`
Public metadata: `{ id, numeric_code, original_name, size_bytes, expires_at, created_at }`. `400 INVALID_FILE_ID`, `404 FILE_NOT_FOUND`, `410` for expired or deleted files.

### `GET /api/file/code/:code`
Same response, looked up by numeric code.

### `GET /api/file/:id/download`
Streams the encrypted bytes as `application/octet-stream`. Download-limited. Headers: `Content-Disposition: attachment; filename="<file_id>.enc"` and `X-Original-Filename`. The browser decrypts the stream locally.

## Reports

### `POST /api/file/:id/report`
Reports a file for abuse. Strict-limited, available to guests.

```json
{ "success": true, "message": "File has been reported. Thank you for helping keep our platform safe." }
```

Reports are de-duplicated per signed-in user, or per client IP for anonymous reports; a repeat returns `409 ALREADY_REPORTED`. Once the number of **distinct signed-in reporters** reaches `AUTO_DELETE_REPORT_COUNT` (default 3) the file is deleted and the message says so. Anonymous reports are stored but never trigger auto-removal by themselves.

## Your files and sharing

All **auth**.

### `GET /api/me/recent-uploads`
Your uploads, newest first. Query: `page`, `per_page`, `q` (filename search). Items: `{ file_id, filename, size_bytes, created_at, expires_at, share_url }`.

### `GET /api/me/files/:id/access`
Your copy of a file's key, wrapped with your identity public key: for your own upload (`owner`) or one received in an accepted transfer (`share`).

```json
{
  "file": {},
  "file_access_key_envelope": { "wrapped_dek_b64": "...", "dek_wrap_alg": "RSA-OAEP-2048-v1", "dek_wrap_version": 1 },
  "identity_key_version": 1,
  "access_kind": "owner"
}
```

The key opens only with the identity key version it was wrapped for; a device on a newer version (after recovery) sees the file as locked until it is [rescued](#identity-key-rescue). `404 ACCESS_DENIED` if the file is gone or you have no copy.

### `GET /api/me/shared-with-me`
Files received through accepted transfers. Paginated; item shape as in recent uploads.

### `GET /api/users/lookup?q=<text>`
Searches users by username for the recipient picker (min 3 characters, else an empty list; max 10 results, never yourself). `{ "items": [{ user_id, username, avatar_url }] }`. Searches Sendly's local user cache, so only people who have used Sendly are found.

### `GET /api/me/recent-share-recipients`
Up to 8 people you recently sent files to, same item shape.

### `GET /api/users/:id/identity-key`
A user's active identity public key, needed to wrap a file key for them: `{ "public_key_jwk", "key_version" }`. `404 RECIPIENT_NOT_READY` if they have not set up encryption yet.

### `POST /api/file/:id/share-to-user`
Sends a file you own to another user, creating a pending [transfer](#transfers). The sender wraps the file key for the recipient in the browser first.

```json
{
  "recipient_user_id": 42,
  "wrapped_dek": "<base64>",
  "dek_wrap_alg": "RSA-OAEP-2048-v1",
  "dek_wrap_nonce": "<base64, optional>",
  "recipient_key_version": 1
}
```

Response: `{ "file_id", "recipient_user_id", "status": "pending" }`. Errors: `400 INVALID_RECIPIENT` (yourself), `403 FILE_OWNERSHIP_REQUIRED`, `404 RECIPIENT_NOT_READY`, `409 RECIPIENT_KEY_VERSION_MISMATCH` (their key rotated, refetch it), `409 TRANSFER_EXISTS`.

## Transfers

Person-to-person sends. The recipient accepts or declines; declining deletes their key envelope. All **auth**.

### `GET /api/me/transfers`
Query:
- `view`: `pending` (default; received and unanswered, file still available) or `history` (most recent activity first).
- `direction` (`history` only): `all` (default), `received` (accepted or declined), `sent` (everything you sent, including pending). Anything else: `400 INVALID_DIRECTION`.
- `page`, `per_page`.

Items: `id`, `file_id`, `filename`, `size_bytes`, `expires_at`, `status` (`pending`/`accepted`/`declined`), `sent_at`, `responded_at`, `available`, `direction` (`received`/`sent`), `reported`, and both parties as `sender_user_id`/`sender_username`/`sender_avatar_url` and `recipient_user_id`/`recipient_username`/`recipient_avatar_url`.

### `GET /api/me/transfers/pending-count`
`{ "count": n }`: unanswered received transfers (the menu badge).

### `POST /api/me/transfers/:file_id/accept` and `/decline`
Recipient only, once per transfer. `404 TRANSFER_NOT_FOUND`, `409 TRANSFER_ALREADY_ANSWERED`, `410 TRANSFER_FILE_UNAVAILABLE` (accept only).

### `POST /api/me/transfers/:file_id/report`
Reports the file of a transfer you received. Recipient only, and only after accepting (`404 TRANSFER_NOT_FOUND` otherwise, `409 TRANSFER_NOT_ACCEPTED` while pending or declined). Behaves like [`POST /api/file/:id/report`](#post-apifileidreport) (strict limiter, dedupe, auto-delete) and records the transfer on the report.

## Quick share tunnels

A tunnel is a short-lived room (10 minutes to 24 hours) where a host and approved participants exchange files using a shared session password. Guests can host and join.

**Callers.** Every tunnel endpoint requires the caller to be a member:

- **Host:** the initiating signed-in user, or for a guest-started tunnel the `host_token` from `start`, sent as `X-Host-Token`.
- **Participant:** a signed-in user, or an anonymous joiner sending `X-Device-ID` plus the `participant_token` from `join` as `X-Participant-Token`.

Non-members get `403 TUNNEL_FORBIDDEN`; a joiner the host declined gets `403 PARTICIPANT_REJECTED`. Until approved, a joiner sees only the lobby (tunnel and participants); file lists, file access, uploads and key envelopes return `403 PARTICIPANT_NOT_APPROVED`.

**Keys.** The host's browser generates a session password and wraps it for each approved participant's throwaway public key (`/envelopes`); every file in the session is encrypted with it. A signed-in uploader also stores their own copy wrapped with their identity key; a guest uploader's copy is wrapped for their throwaway key.

**Lifecycle.** `pending` → `joined` → `active` → `ended`, or `expired`.

### `POST /api/me/tunnels/start`
```json
{ "duration": "30m", "device_id": "optional-device-id" }
```
`duration` is a Go-style duration between `10m` and `24h` (`400 INVALID_DURATION`). Response: `{ tunnel, qr_payload, participants, host_token }`; `host_token` is only present for guest hosts and returned once.

### `POST /api/me/tunnels/join`
Strict-limited.
```json
{
  "code": "1234",
  "device_id": "required for anonymous joiners",
  "public_key_jwk": { "kty": "RSA", "n": "...", "e": "AQAB" },
  "key_algorithm": "RSA-OAEP-2048",
  "key_version": 1
}
```
Response like `start`; anonymous joiners get `participant_token` once. Errors: `409 PARTICIPANT_CONFLICT` (device ID owned by another participant; the owner may re-join to replace its key), `403 PARTICIPANT_REJECTED`, `410 TUNNEL_EXPIRED`, `410 TUNNEL_ALREADY_ACTIVE`, `403 TUNNEL_NOT_AVAILABLE`.

### `GET /api/me/tunnels/:id`
Tunnel metadata, participants (signed-in ones carry `username` and `avatar_url`) and the file list.

### `GET /api/me/tunnels/:id/participants`
`{ "items": [...] }`.

### `GET /api/me/tunnels/:id/files`
Only the tunnel's files.

### `GET /api/tunnels/:id/files/:file_id/access`
A file's metadata plus its key envelope, for approved members of a pending or active tunnel (same caller rules; note the path has no `/me`). Response: `{ "file": {...}, "file_key_envelope": { wrapped_dek_b64, dek_wrap_alg, dek_wrap_nonce_b64, dek_wrap_version } }`.

### `POST /api/me/tunnels/:id/participants/:participant_id/approve`
Host only. Admits a joiner so the host can wrap the session key for them.

### `POST /api/me/tunnels/:id/participants/:participant_id/reject`
Host only. Removes the joiner and their envelope and blocks that user or device from re-joining this tunnel.

### `GET /api/me/tunnels/:id/participant-keys`
Host only. Participants with public keys, including `approved` and `has_envelope`.

### `POST /api/me/tunnels/:id/envelopes`
Host only. Stores the session key wrapped for one approved participant device. `403 PARTICIPANT_NOT_APPROVED`; `409 ENVELOPE_EXISTS` (existing envelopes are never overwritten).

### `GET /api/me/tunnels/:id/envelopes/:device_id`
The participant's own envelope; `:device_id` must be the caller's.

### `POST /api/me/tunnels/:id/confirm`
Host only. Starts the session: the tunnel becomes `active` and approved participants move into it. Body: `{ "device_id": "optional" }`.

### `DELETE /api/me/tunnels/:id`
Leave the tunnel (removes only you). When the last participant leaves, the tunnel and its file blobs are deleted.

## Devices and enrollment

Each signed-in browser is a **device**. A device is *trusted* once it holds a copy of the account's identity private key. All **auth**; sensitive routes are strict-limited. Background in [ARCHITECTURE.md](ARCHITECTURE.md#device-trust-and-identity-keys).

### `POST /api/me/devices/register`
Registers this device and reports whether it already holds the identity key. A `device_id` registered to another account returns `409 DEVICE_ID_CONFLICT`.

Request: `device_id`, `device_label`, `public_key_jwk`, `key_algorithm`, `key_version`; only when creating an identity key also `identity_public_key_jwk`, `identity_key_algorithm`, `wrapped_identity_private_key_b64` (private key wrapped for this device), `identity_key_wrap_alg`, `identity_key_wrap_meta`.

Response contains `device_id` and exactly one of:
- `identity_key_envelope` (`{ wrapped_private_key_b64, wrap_alg, wrap_meta, identity_key_version }`) with `identity_public_key` (`{ key_version, key_algorithm, public_key_jwk }`): the device is trusted.
- `needs_enrollment: true`: another device must approve this one.
- `needs_identity_setup: true`: brand-new account; register again with a new identity keypair (becomes version 1).
- `needs_identity_migration: true`: pre-identity-key account; see [migration](#identity-migration-temporary).

### `POST /api/me/devices/recover`
For an account with no device left to approve. Same request as `register`, with a mandatory new identity keypair (`400 IDENTITY_KEY_REQUIRED`). It becomes the next identity key version, the old one is retired, every other device is revoked and pending enrollments expire. Files wrapped for older versions stay locked until [rescued](#identity-key-rescue).

### `GET /api/me/devices/ws`
WebSocket for live account events. Payload: `{ "type", "enrollment", "request_device", "approver_device_id", "pending_count" }`. `type` is one of `device_enrollment_created`, `device_enrollment_approved`, `device_enrollment_rejected`, `transfers_updated` (`{ pending_count }`) or `sent_transfers_updated` (`{ file_id, status }`).

### `POST /api/me/devices/enrollments`
`{ "request_device_id": "..." }` → `{ "enrollment_id", "verification_code": "123456", "expires_at" }`. The code is shown on the new device and typed on the approving device.

### `GET /api/me/devices/enrollments/pending`
Pending enrollments with the requesting device's metadata.

### `POST /api/me/devices/enrollments/:id/approve`
The trusted approver hands over the identity key, wrapped for the requesting device's public key.
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
`409 IDENTITY_KEY_STALE` for a non-active version; `403 APPROVER_NOT_TRUSTED` if the approver does not hold the key.

### `POST /api/me/devices/enrollments/:id/reject`
`{ "approver_device_id": "..." }`.

## Identity key rescue

After recovery, file keys wrapped for an older identity key version are locked. A browser that still holds that version (typically a device revoked by the recovery) can re-wrap them. **auth**, standard-limited; see the trust note in [SECURITY.md](SECURITY.md#recovery-and-rescue).

### `GET /api/me/identity-rescue/locked?version=<n>&after=<file_id>`
Your file keys wrapped for the older `version`, 100 per page ordered by file id: `{ "items": [{ file_id, wrapped_dek_b64, dek_wrap_alg }], "remaining": n }`.

### `POST /api/me/identity-rescue`
```json
{ "from_version": 1, "to_version": 2, "items": [{ "file_id": "...", "identity_wrapped_dek_b64": "...", "identity_dek_wrap_alg": "..." }] }
```
Only replaces keys still locked to `from_version`, and only when `to_version` is active. Returns `{ "rescued": n }`.

## Identity migration (temporary)

Moves accounts from before identity keys (legacy AES user key) onto them. All crypto happens in the browser. The routes disappear once every account is migrated ([OPERATIONS.md](OPERATIONS.md#retiring-the-identity-migration)). **auth**.

| Route | Description |
|---|---|
| `GET /api/me/identity-migration/legacy-key?device_id=` | This device's copy of the legacy user key; `404 LEGACY_USER_KEY_NOT_FOUND` if none. |
| `POST /api/me/identity-migration/start` | From a device with the legacy key, for an account with no identity key: creates version 1 with the device's copy and an escrow (identity private key encrypted with the user key). `409 IDENTITY_KEY_EXISTS` if another device was first. |
| `GET /api/me/identity-migration/escrow?device_id=` | The escrow, for a device holding the legacy key, if it belongs to the active identity key. |
| `POST /api/me/identity-migration/adopt` | A legacy device stores its own copy of the identity key decrypted from the escrow; no approval needed. |
| `GET /api/me/identity-migration/files?device_id=&after=` | Trusted device only: your uploads whose key is still wrapped only with the legacy user key. |
| `POST /api/me/identity-migration/files` | Trusted device only: those keys re-wrapped for the active identity key; files that already have one are left alone. |

## Health, pages and static routes

| Route | Description |
|---|---|
| `GET /health` | Probes PostgreSQL, Redis and storage (2 s timeout each, cached 5 s): `200 {"status":"healthy","checks":{...}}` or `503 {"status":"unhealthy",...}` with failing checks marked `error`. Details are logged, never returned. |
| `GET /livez` | Process liveness only. |
| `GET /static/*` | Static assets, including `wordlist.txt` (cached for a year). |
| `GET /robots.txt`, `/sitemap.xml`, `/.well-known/security.txt` | SEO and security contact. |
| `GET /`, `/quickshare`, `/link`, `/shared/:id`, `/uploaded`, `/transfers`, `/limits`, `/help`, `/data-encryption`, `/tos`, `/privacy`, `/legal-notice` | HTML pages. Each sets the `csrf_token` cookie and honors the locale (`en`, `de`). `/transfers` shows a sign-up teaser to guests. |

## Error codes

Frequently seen codes. `details` may carry validation text.

| Status | Code | Meaning |
|---|---|---|
| 400 | `INVALID_REQUEST` | Body failed validation. |
| 400 | `INVALID_PAGINATION` | Bad `page` / `per_page`. |
| 400 | `INVALID_FILE_ID` / `MISSING_SESSION_ID` | Malformed identifier. |
| 400 | `FILE_TOO_LARGE` | Over the tier's size limit. |
| 400 | `INVALID_DURATION` | Duration not allowed for tier or tunnel. |
| 400 | `IDENTITY_ENVELOPE_REQUIRED` / `IDENTITY_KEY_STALE` / `IDENTITY_KEY_REQUIRED` | Identity key envelope missing or for the wrong version. |
| 401 | `AUTH_REQUIRED` | Sign in required. |
| 403 | `CSRF_FORBIDDEN` | Missing or mismatched CSRF token. |
| 403 | `TUNNEL_FORBIDDEN` / `PARTICIPANT_NOT_APPROVED` / `PARTICIPANT_REJECTED` | Tunnel membership problem. |
| 403 | `FILE_OWNERSHIP_REQUIRED` / `APPROVER_NOT_TRUSTED` | Not the owner / not a trusted device. |
| 404 | `FILE_NOT_FOUND` / `SESSION_NOT_FOUND` / `TRANSFER_NOT_FOUND` / `ACCESS_DENIED` / `RECIPIENT_NOT_READY` | Resource missing or not yours. |
| 409 | `ALREADY_REPORTED` / `DEVICE_ID_CONFLICT` / `PARTICIPANT_CONFLICT` / `ENVELOPE_EXISTS` / `TRANSFER_EXISTS` / `TRANSFER_ALREADY_ANSWERED` / `RECIPIENT_KEY_VERSION_MISMATCH` | State conflict. |
| 410 | `TUNNEL_EXPIRED` / `TUNNEL_ALREADY_ACTIVE` / `TRANSFER_FILE_UNAVAILABLE` | Gone. Expired or deleted files also return 410. |
| 429 | `RATE_LIMITED` / `DOWNLOAD_RATE_LIMITED` | Over a limiter. |
