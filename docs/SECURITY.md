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
- Quick share (tunnel) membership: every tunnel endpoint requires the caller to be the host (initiating CNS user, or `X-Host-Token` for a guest host) or a participant (CNS user, or `X-Device-ID` + `X-Participant-Token` for an anonymous joiner). Device IDs alone never authorize anything.
- Quick share host approval: joiners start unapproved. Session key envelopes, peer file-key wrapping, file lists, file access and uploads are only available to participants the host approved. The host compares a key fingerprint shown next to each joiner with the one on the joiner's screen before approving.
- A participant's public key can only be replaced by that participant, and existing session key envelopes are never overwritten.
- Device IDs stay bound to the account that registered them (`DEVICE_ID_CONFLICT` otherwise).

## Data Protection Model

- File blobs are stored as encrypted payloads.
- File key envelopes (`wrapped_dek`) are persisted separately.
- User key envelopes (`wrapped_user_key`) tie trust material to specific devices.

## Abuse Prevention

- Multi-tier route rate limiting:
  - Standard
  - Strict
  - Download
- Duplicate report prevention per signed-in user + file, or per client IP + file for anonymous reports.
- Auto-delete threshold for highly reported files; only distinct signed-in reporters count towards it.
- Transfer recipients can report a file only after accepting it; transfer reports count like any signed-in report.
- Formal notices of illegal content go to `abuse@cns-studios.com` (the ToS moderation section, DSA Art. 14).
- Client IPs come from forwarding headers only when the direct peer is a configured trusted proxy (`TRUSTED_PROXIES`, see CONFIGURATION.md).
- Quick share joins are rate-limited with the strict limiter.

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
