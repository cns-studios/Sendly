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

- File key envelope (`file_key_envelopes`) stores wrapped DEK and wrap metadata.

## Device Trust

### User devices

`user_devices` tracks registered devices:

- device ID
- owning CNS user
- label
- public key JWK
- key algorithm/version
- active/revoked state

### User key envelopes

`user_key_envelopes` stores wrapped user keys per `(user, device)` pair.

## Device Enrollment

`device_enrollments` stores pending trust requests:

- request device ID
- verification code
- status (`pending`, `approved`, `rejected`, `expired`)
- approver metadata
- expiration timestamps

## Reports

`reports` records abuse reports by file and reporter IP.

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
