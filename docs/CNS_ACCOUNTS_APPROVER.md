# Proposal: the CNS Accounts app as a trusted Sendly device

Status: proposal, nothing implemented. Needs sign-off before any backend change.

## Goal

A user who connected Sendly to their CNS account can "Set Up" Sendly in the CNS Accounts
Android app. The app then becomes a trusted Sendly device whose only job is to approve or deny
new Sendly devices: when another device signs in to Sendly and asks for enrollment, the request
shows up in the app (Services card → Sendly → "Login request", device + verification code), and
Allow hands the account's identity key to the new device exactly like approving from a browser.

## What already exists (and is reused unchanged)

- `/android/me/devices/register`: registers a device (RSA-OAEP-2048 public JWK) and returns
  `needs_enrollment` or the identity-key envelope.
- `/android/me/devices/enrollments` (create), `/enrollments/pending`, `/enrollments/:id/approve`,
  `/enrollments/:id/reject`, and the WebSockets `ws/pending-approvals` and `enrollments/:id/ws`.
- The envelope format of `crypto.js`: `RSA-OAEP-2048+AES-GCM-256-v1` (RSA-OAEP/SHA-256 wraps a
  random AES-256 key; AES-GCM with a 12-byte IV encrypts the identity private JWK as JSON;
  layout `wrappedKey(256) | iv(12) | ciphertext`). The approve body is the same as the web's
  (`approver_device_id`, `verification_code`, `wrapped_identity_private_key_b64`,
  `identity_key_wrap_alg`, `identity_key_wrap_meta`, `identity_key_version`).

## Flow in the app

1. Set Up → "Setting up Sendly" modal → Continue (button shows a spinner).
2. The app gets a token Sendly accepts (open question 1), generates its device key pair and
   registers as "CNS Accounts on <model>".
3. `needs_enrollment` → create an enrollment → the modal changes to "Approve this app from a
   trusted Sendly device" with the verification code and an Abort button; the app waits on
   `enrollments/:id/ws`.
4. Approved → register again → unwrap the identity-key envelope → keep the identity private key
   encrypted with an Android Keystore key that needs biometric / device-credential auth to use.
5. From then on, pending enrollments for the account appear as login requests. Allow →
   biometric → unwrap the identity key → wrap it for the requesting device's public key →
   approve. Deny → reject.

Device key: Android Keystore RSA-OAEP only supports MGF1-SHA1 before Android 14, which can't
open the WebCrypto (MGF1-SHA256) envelopes, so the device RSA key is generated in software and
its private part stored AES-GCM-encrypted under a Keystore key.

## Proposed Sendly changes

1. **Cancel own enrollment**: `DELETE /android/me/devices/enrollments/:id` by the requesting
   device (the modal's Abort). Today only a trusted device can reject. Small, no migration.
2. **Remove own device**: `DELETE /android/me/devices/:id` by that device (Disconnect / sign-out
   in the CNS app). Revokes its identity-key envelopes.
3. **Device kind**: `user_devices.kind` (`browser` | `desktop` | `android` | `approver`), so the
   web device list shows "CNS Accounts" and the approver is never offered for file access. Needs
   a migration (`db/migrations/022-add-device-kind.sql`).
4. **Push for new enrollments**, so a request reaches the phone when the app isn't open. Depends
   on the push channel chosen for CNS notifications (FCM recommended; see the CNS Accounts repo,
   `docs/backend_plans.md` §2).

Tests: the existing `internal/services/device_identity_test.go` and the integration scenarios
(`internal/integration/scenarios_test.go`) get cases for cancel, self-removal and the
approver kind (cannot list/download files, can approve).

## Open questions

1. Token for Sendly: the CNS app's own access token works today because Sendly accepts any CNS
   client's token (flagged in the last audit). Alternatively a real Sendly login: the app calls
   auth's `/api/auth/authorize` with its bearer token + PKCE for the `sendly` client and
   exchanges the code, which needs an app redirect URI (or public-client exchange) for that
   client in auth. The second would let Sendly reject other clients' tokens afterwards.
2. The approver holds the identity key, so it could decrypt the account's files (it shows none).
   Acceptable with encryption at rest + biometric per use?
3. Sendly's production `client_id` in auth (the app matches `sendly` or the display name).
