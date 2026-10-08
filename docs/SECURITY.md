# Sendly Security

What protects your files, who can do what, and what Sendly deliberately trusts. See [ARCHITECTURE.md](ARCHITECTURE.md) for the building blocks and [API.md](API.md) for the endpoints.

## Threat model in short

- The server, its database and its disk are treated as **untrusted for confidentiality**: they hold only ciphertext and wrapped keys. A full database and storage leak does not expose file contents.
- A malicious or compromised server could still serve modified JavaScript. Mitigations are a strict CSP, self-hosted assets and tests; users who need stronger guarantees should verify the deployed code.
- Availability and abuse (spam, illegal content, scraping) are handled with rate limits, reports and expiry.

## Encryption

End-to-end encrypted: the server stores only encrypted file blobs and wrapped keys, never a file key, session password or private key in the clear.

- **Files** are encrypted in the browser (WebCrypto) with a per-upload key, or in quick share with the session password, before any byte is uploaded.
- **Link shares:** the key travels in the URL fragment, which never reaches the server. Anyone with the full link can decrypt; anyone without the fragment cannot.
- **Accounts:** each account has a versioned identity keypair (RSA-OAEP-2048). Its private key exists only in browsers; the server keeps one copy per trusted device, wrapped for that device's own key (`user_identity_key_device_envelopes`). A user's access to a file is their copy of its key wrapped with their identity public key.
- **Transfers:** the sender wraps the file key for the recipient's identity public key. The recipient's copy is only handed out once they accept.
- **Quick share:** the host wraps a session password for each approved participant's throwaway RSA key. Guests' keys live only in the page.
- **New devices** receive the identity key only from a trusted device, after the user compares a verification code, or through recovery.

### Recovery and rescue

Recovery creates a new identity key version and revokes every device; anything wrapped for an older version stays unreadable until a device still holding that version re-wraps it (rescue).

Rescue trust point: the rescuing device was revoked, so it cannot verify the active identity public key and wraps for the one the server reports. The server only accepts rescue envelopes for keys that are still locked, so a bad upload cannot break a readable key, but a compromised server could collect the keys of locked files from a returning old device. This is accepted as a trade-off, like transfers, where the sender wraps for the recipient public key the server reports.

### Identity migration (temporary)

The identity key of an account from before identity keys is stored encrypted with its legacy user key (`legacy_identity_escrow`), so only devices already holding that user key can open it.

## Authentication

- Sign-in uses CNS Auth OAuth2 with PKCE (`S256`); tokens are stored in HttpOnly cookies (`Secure` on HTTPS), and the refresh token is revoked at CNS on logout.
- `/api` requires a CSRF token (double-submit cookie `csrf_token` + `X-CSRF-Token`, constant-time compared).
- Guests are anonymous; limits and rate limits apply per client IP.

## Authorization

- File metadata, access envelopes and "my files" are scoped to the owner or the accepted recipient.
- Devices stay bound to the account that registered them (`DEVICE_ID_CONFLICT` otherwise). Enrollment actions need a trusted device of the same account.
- **Quick share:** every tunnel endpoint requires the caller to be the host (initiating user, or `X-Host-Token` for a guest host) or a participant (signed-in user, or `X-Device-ID` + `X-Participant-Token`). Device IDs alone never authorize anything.
- **Host approval:** joiners start unapproved. Session key envelopes, file lists, file access and uploads are only available to participants the host approved, after comparing the key fingerprint shown next to each joiner with the one on the joiner's screen.
- A participant's public key can only be replaced by that participant, and existing session key envelopes are never overwritten.
- Declined joiners are blocked from rejoining that tunnel (`tunnel_rejections`).

## Abuse prevention

- **Rate limiting:** standard, strict and download limiters, per user or per IP, backed by Redis ([OPERATIONS.md](OPERATIONS.md#rate-limiting)). Joining quick share, reporting and device enrollment use the strict limiter.
- **Reports:** deduplicated per signed-in user, or per IP for anonymous reports. Only distinct signed-in reporters count toward auto-delete (`AUTO_DELETE_REPORT_COUNT`). Transfer recipients can report only after accepting.
- **Notices:** formal notices of illegal content go to `abuse@cns-studios.com` (ToS moderation section, DSA Art. 14).
- **Client IPs** are read from forwarding headers only when the direct peer is a configured trusted proxy (`TRUSTED_PROXIES`), so clients cannot spoof their IP.
- **Tiers** cap file size and lifetime; expired files and their blobs are deleted automatically.

## Input and transport

- JSON and multipart bodies are bound and validated; file IDs and numeric codes are format-checked.
- WebSocket upgrades are gated by authentication.
- Errors never include internal details; health checks log failures but return only status.
- Serve production behind HTTPS only.

## Content Security Policy and self-hosted assets

Every response carries a `Content-Security-Policy` header (`internal/middleware/security_headers.go`):

`default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data: blob: https:; connect-src 'self' blob: <ws(s) origin of BASE_URL>; manifest-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'`

- No third-party script, stylesheet or font is loaded. Lucide icons are vendored at `web/static/js/vendor/` (ISC) and fonts at `web/static/fonts/`. To upgrade lucide, replace the file, bump the version in the filename and templates, and check all `data-lucide` icon names still exist.
- Inline scripts are not allowed. Page bootstrap lives in `web/static/js/page-init.js`; `window.CONFIG` is passed through a `<script type="application/json" id="page-config">` block.
- `style-src` keeps `'unsafe-inline'` for template `style` attributes; scripts are unaffected.
- `img-src https:` exists because avatars are served by the CNS identity provider.
- `TestFrontendHasNoThirdPartyOrInlineCode` fails if templates or CSS reintroduce external resources, inline scripts or inline event handlers.

## Privacy and retention

Sendly keeps as little as it can. Soft-deleted file records, per-IP upload counters, old reports and tunnel rejections are purged on a schedule ([OPERATIONS.md](OPERATIONS.md#data-retention)). Legal texts live in `legal/`.

## Known trust assumptions

- The WebSocket upgrader's `CheckOrigin` is permissive; enforce origin and network controls at the ingress or reverse proxy where possible.
- The server delivers the JavaScript that performs encryption (see threat model).
- Public keys of other users and of rescue targets are as reported by the server (see rescue above).

## Hardening checklist

- HTTPS only; `BASE_URL` set to the public origin; `GIN_MODE=release`.
- Restrict network access to PostgreSQL and Redis; use strong credentials.
- Set `TRUSTED_PROXIES` correctly behind a proxy or Cloudflare.
- Rotate `CNS_AUTH_SERVICE_KEY` periodically.
- Centralize logs; watch repeated auth failures and rate-limit hits.

## Incident response

1. Tighten the strict limiter.
2. Force a trusted-device reset for affected accounts (recovery).
3. Review reports; delete or expire sensitive blobs (`admin delete <file_id>`).
4. Rotate CNS service credentials if compromise is suspected.
