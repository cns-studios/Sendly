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
- Quick share host approval: joiners start unapproved. Session key envelopes, file lists, file access and uploads are only available to participants the host approved. The host compares a key fingerprint shown next to each joiner with the one on the joiner's screen before approving.
- A participant's public key can only be replaced by that participant, and existing session key envelopes are never overwritten.
- Device IDs stay bound to the account that registered them (`DEVICE_ID_CONFLICT` otherwise).

## Data Protection Model

End-to-end encrypted: the server stores only encrypted file blobs and wrapped keys, never a file key, session password or private key in the clear.

- File blobs are encrypted in the browser with a per-upload key (or, in quick share, the session password).
- Each signed-in account has one identity keypair (RSA-OAEP-2048), versioned. Its private key exists only in the browser; the server keeps one copy per trusted device, wrapped for that device's own key (`user_identity_key_device_envelopes`).
- A device is trusted when it holds a copy of the account's active identity key. Copies are only created by the account's first device, by a trusted device approving a new one (verification code), or by recovery.
- A signed-in user's access to a file is their copy of its key wrapped with their identity public key (`file_access_key_envelopes`): `owner` for their uploads, `share` for accepted transfers. Senders wrap for the recipient's identity public key.
- Recovery creates a new identity key version and revokes every device; anything wrapped for an older version stays unreadable to the new key until a device still holding that version re-wraps it (rescue).
- Rescue trust point: the rescuing device was revoked, so it can't verify the active identity public key and wraps for the one the server reports. The server only accepts rescue envelopes for keys that are still locked, so a bad upload can't break a readable key, but a compromised server could collect the keys of locked files from a returning old device. Accepted as a trade-off, like transfers, where the sender wraps for the recipient public key the server reports.
- Identity migration (temporary): the identity key of an account from before identity keys is stored encrypted with its legacy user key (`legacy_identity_escrow`), so only devices already holding that user key can open it.
- Guest share links carry the file key in the URL fragment, which never reaches the server. Quick share guests use throwaway RSA keys held in the page.

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

## Content Security Policy and Self-Hosted Assets

Every response carries a `Content-Security-Policy` header (`internal/middleware/security_headers.go`):
`default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data: blob: https:; connect-src 'self' blob: <ws(s) origin of BASE_URL>; manifest-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'`.

- No third-party script, stylesheet or font is loaded. Lucide icons are vendored at `web/static/js/vendor/lucide-1.53.0.min.js` (ISC, license alongside) and the TASA Orbiter / Arimo fonts at `web/static/fonts/` (declared in `web/static/css/fonts.css`). To upgrade lucide, replace the file, bump the version in the filename and templates, and verify all `data-lucide` icon names still exist.
- Inline scripts are not allowed. Page bootstrap lives in `web/static/js/page-init.js`; `window.CONFIG` is passed through a `<script type="application/json" id="page-config">` data block.
- `style-src` keeps `'unsafe-inline'` because templates use inline `style` attributes; scripts are unaffected.
- `img-src https:` exists because user avatars are served by the CNS identity provider.
- `TestFrontendHasNoThirdPartyOrInlineCode` fails if templates or CSS reintroduce external resources, inline scripts or inline event handlers.

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
