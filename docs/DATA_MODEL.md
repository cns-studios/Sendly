# ShareIt Backend Data Model

This document summarizes key persistence entities.

## Files

Main file record includes:

- `id`
- `numeric_code`
- `original_name`
- `size_bytes`
- uploader and optional owner metadata
- optional tunnel linkage
- expiration and creation timestamps
- report and deletion flags

Related:

- `file_access_key_envelopes`: a signed-in user's copy of a file key, wrapped with their identity public key (`recipient_key_version`); `access_kind` is `owner` (their upload) or `share` (a transfer, handed out once accepted).
- `file_key_envelopes`: a quick share guest upload's key, wrapped for the guest's throwaway participant key.
- `file_recipient_key_envelopes`: legacy, no longer written.

## Device Trust

### User devices

`user_devices` tracks registered devices:

- device ID
- owning CNS user
- label
- public key JWK
- key algorithm/version
- active/revoked state

### Identity keys

`user_identity_keys` holds each account's identity public keys by `key_version`, one `active` at a time; recovery retires it and adds the next version.

`user_identity_key_device_envelopes` stores each trusted device's copy of an identity private key, wrapped for that device's public key. A device is trusted when it has a copy of the active version.

### Legacy user key envelopes

`user_key_envelopes` stores the per-device copies of the pre-identity AES user key. Only read to migrate accounts from before identity keys.

## Device Enrollment

`device_enrollments` stores pending trust requests:

- request device ID
- verification code
- status (`pending`, `approved`, `rejected`, `expired`)
- approver metadata
- expiration timestamps

## Reports

`reports` records abuse reports by file and reporter IP, plus the reporter's CNS user ID when signed in (`reporter_cns_user_id`, unique per file) and the transfer a report was filed from (`transfer_id`, NULL for link-share reports).

Report count on file drives auto-delete threshold logic.

## Tunnels

`tunnels` represent temporary transfer sessions:

- short code
- initiator/peer identity and device IDs
- duration and lifecycle status
- confirmation and ending metadata

Tunnel-linked files can be queried by tunnel ID.

## Desktop API Ownership Mapping

Desktop API keys and file association mapping determine key-scoped file visibility.

## Redis Operational State

Redis is used for non-persistent operational data:

- upload session metadata
- chunk upload tracking
- pending file flags
- assembly status
- per-route-class rate-limit counters

## Filesystem Layout (Conceptual)

- Final encrypted file blobs keyed by file ID
- Temporary chunk directories keyed by session ID
