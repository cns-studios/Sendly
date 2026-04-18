# ShareIt Backend Security Notes

This document summarizes practical security controls in ShareIt backend.

## Authentication and Identity

### Browser

- Cookie-based auth token (`auth_token`) from CNS OAuth callback.
- CSRF token cookie (`csrf_token`) issued by page handlers.
- `/api` routes enforce CSRF middleware.

### Desktop

- API key auth (`X-API-KEY` or query `key`).
- Optional bearer token auth for CNS users.

### Mobile

- Bearer token auth on `/android` surface.

## Authorization

Access checks include:

- File ownership checks for metadata and download.
- Device ownership checks for enrollment actions.
- Trusted device checks for envelope-sensitive operations.
- Tunnel ownership and active-state checks.

## Data Protection Model

- File blobs are stored as encrypted payloads.
- File key envelopes (`wrapped_dek`) are persisted separately.
- User key envelopes (`wrapped_user_key`) tie trust material to specific devices.

## Abuse Prevention

- Multi-tier route rate limiting:
  - Standard
  - Strict
  - Download
- Duplicate report prevention per reporter IP + file.
- Auto-delete threshold for highly reported files.

## Input and Transport Controls

- Request binding/validation on JSON and multipart fields.
- File ID and numeric code format validation for lookup/download endpoints.
- Websocket upgrade paths gated by auth checks.

## Operational Hardening Recommendations

- Serve behind HTTPS only in production.
- Use secure cookie behavior and strict domain policy.
- Restrict network access to postgres and redis.
- Rotate CNS service credentials periodically.
- Centralize logs and monitor repeated auth failures and rate-limit hits.

## Known Trust Assumptions

- `CheckOrigin` is permissive in websocket upgrader; deployment should enforce network and origin controls at ingress/reverse proxy where possible.
- Desktop CORS is permissive by design for local app interoperability.

## Incident Response Pointers

When suspicious activity is detected:

1. Increase strict limiter aggressiveness.
2. Revoke/rotate desktop API keys.
3. Force trusted-device reset for affected accounts.
4. Review reports and delete/expire sensitive blobs.
5. Rotate CNS service credentials if compromise suspected.
