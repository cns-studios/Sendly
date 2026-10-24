# The CNS Accounts app as a trusted Sendly device

A user who connected Sendly to their CNS account can "Set Up" Sendly in the CNS
Accounts Android app. The app then becomes a trusted Sendly device whose only
job is to approve or deny new Sendly devices: when another device signs in to
Sendly and asks for enrollment, the request shows up in the app (Services card →
Sendly → login request, with device and verification code), and Allow hands the
account's identity key to the new device exactly like approving from a browser.

## Decisions (agreed 2026-10-24)

- **Token:** the app signs in to Sendly properly: it asks CNS auth for an
  authorization code for the `Sendly` client (`/api/auth/authorize` with its own
  session + PKCE) and exchanges it, so it holds a Sendly-scoped token. This needs
  one redirect URI for the app on the `Sendly` OAuth client in CNS auth (admin
  panel); no Sendly code change.
- **Identity key on the phone:** accepted. The app never shows files. The key is
  stored encrypted with an Android Keystore key; using it asks for the
  fingerprint when the app's biometric setting is on.
- **Client id:** `Sendly`.

## What already existed (reused unchanged)

- Device registration, enrollment creation, the pending list, approve/reject
  and the WebSockets under `/android/me/devices/...`.
- The envelope format of `crypto.js`: `RSA-OAEP-2048+AES-GCM-256-v1`
  (RSA-OAEP/SHA-256 wraps a random AES-256 key; AES-GCM with a 12-byte IV
  encrypts the identity private JWK as JSON; layout
  `wrappedKey(256) | iv(12) | ciphertext`).

## Added on this branch

1. **Device kind** (`user_devices.kind`, migration 022): `standard` | `approver`,
   set once at the first registration. An approver can never create or recover
   the identity key (`403 APPROVER_DEVICE_RESTRICTED`), also not by
   re-registering without the kind.
2. **Cancel own enrollment**: `POST /android/me/devices/enrollments/:id/cancel`
   by the requesting device (the app's Abort).
3. **Remove own device**: `DELETE /android/me/devices/:id`, approver devices
   only: revokes it and deletes its identity key copy (Disconnect in the app).
4. **Push**: creating an enrollment (web or Android) asks CNS auth to wake the
   user's CNS Accounts app (`POST /api/service/push`, event
   `sendly_enrollment`, id = enrollment id). Needs the `push:send` scope on
   Sendly's service key in CNS auth.

Tests: `internal/services/device_identity_test.go` (`TestApproverDevice`) and
`internal/integration/live_approver_device_test.go` (real schema).

## Flow in the app

1. Set Up → "Setting up Sendly" → Continue: Sendly-scoped sign-in (above), a
   fresh RSA-OAEP-2048 device key pair, registration as "CNS Accounts on
   <model>" with `device_kind: approver`.
2. `needs_enrollment` → create an enrollment → "Approve this app from a trusted
   Sendly device" with the verification code and Abort; the app waits on
   `enrollments/:id/ws`.
3. Approved → register again → unwrap the identity-key envelope → keep it
   encrypted under a Keystore key.
4. New enrollments of the account arrive by push (and over the WebSocket while
   the app is open). Allow → (fingerprint) → unwrap the identity key → wrap it
   for the requesting device's public key → approve with the verification code.
   Deny → reject.

The device RSA key is generated in software and its private part stored
AES-GCM-encrypted under a Keystore key: Keystore's RSA-OAEP only supports
MGF1-SHA1 before Android 14, which can't open WebCrypto's MGF1-SHA256 envelopes.

## Not changed

Sendly still accepts any CNS client's access token (the open audit item). With
the app on a Sendly-scoped token, Sendly could start requiring `client_id ==
Sendly`; that also affects the Sendly Android app and desktop clients, so it is
left for a separate change.
