# ShareIt

## Overview

ShareIt is an end-to-end encrypted file sharing application that allows users to securely upload, share, and download files with automatic expiration. The platform offers both a web interface and a desktop application API for seamless file management.

### Key Features

- **End-to-End Encryption**: Files are encrypted on the client side before transmission
- **Automatic Expiration**: Files automatically expire after a configurable duration (24 hours, 7 days, 30 days, or 90 days)
- **Desktop Application Support**: Native desktop integration with API key authentication
- **File Sharing**: Generate shareable links with numeric codes for easy file distribution
- **Reporting System**: Users can report inappropriate files with automatic deletion after threshold
- **Real-time Updates**: WebSocket support for desktop clients to receive notifications about new uploads
- **Rate Limiting**: Request throttling to prevent abuse
- **CSRF Protection**: Security middleware for web API endpoints

## Getting Started

### Prerequisites

- Go 1.18+
- PostgreSQL 12+
- Redis 6+
- Docker & Docker Compose (for containerized deployment)

### Installation & Running

```bash
# Clone the repository
git clone https://github.com/cns-studios/ShareIt.git
cd ShareIt

# Install dependencies
go mod download

# Set up environment variables
cp .env.example .env

# Run with Docker Compose
docker compose up

# Or run locally
go run cmd/server/main.go
```

### Configuration

Configure via environment variables:

```env
PORT=8085
BASE_URL=http://localhost:8085

# PostgreSQL
POSTGRES_HOST=localhost
POSTGRES_PORT=5432
POSTGRES_USER=shareit
POSTGRES_PASSWORD=changeme
POSTGRES_DB=shareit

# Redis
REDIS_HOST=localhost
REDIS_PORT=6379

# Rate limiting
RATE_LIMIT_MAX_PER_MINUTE=2
RATE_LIMIT_WINDOW_SECONDS=60
RATE_LIMIT_STRICT_MAX_PER_MINUTE=1
RATE_LIMIT_STRICT_WINDOW_SECONDS=60
RATE_LIMIT_DOWNLOAD_MAX_PER_MINUTE=10
RATE_LIMIT_DOWNLOAD_WINDOW_SECONDS=60

# Storage
DATA_DIR=./data
CHUNK_DIR=             # Optional: separate chunk storage directory

# Security
BEHIND_CLOUDFLARE=false
MAX_FILE_SIZE=786432000  # 750MB in bytes
AUTO_DELETE_REPORT_COUNT=3

# Notifications
DISCORD_WEBHOOK_URL=   # Optional: for file report notifications

# Migrations
MIGRATIONS_DIR=db/migrations
```

### Database Migrations

ShareIt now applies SQL migrations automatically at server startup.

- Migration files are loaded from `MIGRATIONS_DIR` (default: `db/migrations`).
- Applied migrations are tracked in `schema_migrations`.
- If a previously-applied migration file changes, startup fails with a checksum mismatch.

To run migrations manually:

```bash
make migrate
```

---

# API Documentation

## Base URLs

- **Web API**: `http://localhost:8085/api`
- **Android API**: `http://localhost:8085/android`
- **Desktop API**: `http://localhost:8085/desktop`
- **Web Pages**: `http://localhost:8085`

### Android API

The Android surface uses bearer-token authentication and does not require CSRF cookies. It is mounted directly under `/android` and reuses the same storage and upload/download pipeline as the web and desktop APIs.

## Authentication

### Web API (Browser)

The web API uses **CSRF token** authentication. Tokens are set as cookies automatically on page loads.

- CSRF tokens are required for all `/api/*` endpoints
- Token is transmitted in the `X-CSRF-Token` header
- Obtained automatically when accessing any page (`/`, `/shared`, etc.)

### Desktop API

The desktop API uses **API Key** authentication.

- Pass API key via `X-API-KEY` header
- Verify key validity first with `/desktop/auth/verify`
- CORS enabled for cross-origin requests

## Error Response Format

All error responses follow this format:

```json
{
  "error": "Human-readable error message",
  "code": "ERROR_CODE",
  "details": "Additional context (optional)"
}
```

Common error codes:
- `INVALID_REQUEST` - Request body validation failed
- `INVALID_FILE_ID` - File ID format is invalid
- `FILE_NOT_FOUND` - File does not exist
- `FILE_EXPIRED` - File has expired
- `FILE_DELETED` - File was deleted due to reports
- `FILE_TOO_LARGE` - File exceeds max size
- `SESSION_NOT_FOUND` - Upload session doesn't exist
- `API_KEY_NOT_FOUND` - Desktop API key invalid or inactive
- `FILE_NOT_OWNED` - Desktop user doesn't own this file

---

## Web API Routes

### Page Routes (Browser)

#### `GET /` - Home Page
Returns the main upload interface with CSRF token.

**Response**: HTML page with form and base configuration

**Query Parameters**: None

---

#### `GET /tos` - Terms of Service
Returns the Terms of Service page.

**Response**: HTML page

---

#### `GET /privacy` - Privacy Policy
Returns the Privacy Policy page.

**Response**: HTML page

---

#### `GET /shared` - File Lookup
Returns the file lookup interface for retrieving files by numeric code.

**Response**: HTML page with lookup form

---

#### `GET /shared/:id` - Download File Page
Returns the download interface for a specific file.

**Parameters**:
- `id` (path, required) - File ID (UUID format)

**Response**: HTML page with download interface

---

#### `GET /health` - Health Check
Check if the server is running and healthy.

**Response**:
```json
{
  "status": "healthy"
}
```

---

### Upload API Routes

#### `POST /api/upload/init` - Initialize Upload
Initiates a new file upload session. Client specifies file details and chunk configuration.

**Headers**:
- `Content-Type: application/json`
- `X-CSRF-Token: <token>` (required)

**Request Body**:
```json
{
  "file_name": "document.pdf",
  "file_size": 5242880,
  "total_chunks": 10,
  "chunk_size": 524288
}
```

**Parameters**:
- `file_name` (string, required) - Original filename
- `file_size` (integer, required) - Total file size in bytes
- `total_chunks` (integer, required) - Number of chunks file is split into
- `chunk_size` (integer, required) - Size of each chunk in bytes

**Rate Limited**: Yes (one request per IP every 60 seconds)

**Response** (200 OK):
```json
{
  "session_id": "550e8400-e29b-41d4-a716-446655440000",
  "file_id": "f8f8f8f8-f8f8-f8f8-f8f8-f8f8f8f8f8f8",
  "chunk_size": 524288,
  "total_chunks": 10
}
```

---

#### `POST /api/upload/chunk` - Upload Chunk
Uploads a single chunk of the file.

**Headers**:
- `Content-Type: multipart/form-data`
- `X-CSRF-Token: <token>` (required)

**Form Parameters**:
- `session_id` (string, required) - Session ID from init response
- `chunk_index` (integer, required) - Zero-based chunk index
- `file` (file, required) - Binary chunk data

**Response** (200 OK):
```json
{
  "chunk_index": 0,
  "received": true
}
```

---

#### `GET /api/upload/status/:session_id` - Check Assembly Status
Check if all chunks have been received and file assembly progress.

**Parameters**:
- `session_id` (path, required) - Session ID from init response

**Response** (200 OK):
```json
{
  "session_id": "550e8400-e29b-41d4-a716-446655440000",
  "received_chunks": 8,
  "total_chunks": 10,
  "status": "assembling"
}
```

---

#### `POST /api/upload/complete` - Complete Upload
Completes the upload after all chunks are sent. File is assembled and marked as pending.

**Headers**:
- `Content-Type: application/json`
- `X-CSRF-Token: <token>` (required)

**Request Body**:
```json
{
  "session_id": "550e8400-e29b-41d4-a716-446655440000",
  "confirmed": true
}
```

**Parameters**:
- `session_id` (string, required) - Session ID
- `confirmed` (boolean, required) - Confirmation flag

**Response** (200 OK):
```json
{
  "session_id": "550e8400-e29b-41d4-a716-446655440000",
  "file_id": "f8f8f8f8-f8f8-f8f8-f8f8-f8f8f8f8f8f8",
  "pending_expires_at": "2026-04-07T12:34:56Z"
}
```

---

#### `POST /api/upload/finalize` - Finalize Upload
Finalizes the upload and sets the expiration time. Generates a shareable link.

**Headers**:
- `Content-Type: application/json`
- `X-CSRF-Token: <token>` (required)

**Request Body**:
```json
{
  "session_id": "550e8400-e29b-41d4-a716-446655440000",
  "duration": "7d"
}
```

**Parameters**:
- `session_id` (string, required) - Session ID
- `duration` (string, required) - Expiration duration: `24h`, `7d`, `30d`, or `90d`

**Response** (200 OK):
```json
{
  "file_id": "f8f8f8f8-f8f8-f8f8-f8f8-f8f8f8f8f8f8",
  "numeric_code": "123-456-789",
  "share_url": "http://localhost:8085/shared/f8f8f8f8-f8f8-f8f8-f8f8-f8f8f8f8f8f8"
}
```

---

#### `DELETE /api/upload/cancel` - Cancel Upload
Cancels an ongoing upload session and cleans up temporary data.

**Headers**:
- `Content-Type: application/json`
- `X-CSRF-Token: <token>` (required)

**Request Body**:
```json
{
  "session_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

**Response** (200 OK):
```json
{
  "success": true,
  "message": "Upload session cancelled"
}
```

---

### File API Routes

#### `GET /api/file/:id` - Get File Metadata
Retrieves metadata for a file without downloading it.

**Parameters**:
- `id` (path, required) - File ID (UUID format)

**Response** (200 OK):
```json
{
  "id": "f8f8f8f8-f8f8-f8f8-f8f8-f8f8f8f8f8f8",
  "numeric_code": "123-456-789",
  "original_name": "document.pdf",
  "size_bytes": 5242880,
  "expires_at": "2026-04-13T12:34:56Z",
  "created_at": "2026-04-06T12:34:56Z"
}
```

---

#### `GET /api/file/:id/download` - Download File
Downloads the file binary data.

**Parameters**:
- `id` (path, required) - File ID (UUID format)

**Response** (200 OK):
- Binary file content
- `Content-Disposition` header with filename
- `Content-Type` set to application/octet-stream

**Status Codes**:
- `200` - File downloaded successfully
- `404` - File not found
- `410` - File expired or deleted

---

#### `GET /api/file/code/:code` - Get File by Numeric Code
Retrieves file metadata using the shareable numeric code instead of UUID.

**Parameters**:
- `code` (path, required) - Numeric code (format: `123-456-789`)

**Response** (200 OK):
```json
{
  "id": "f8f8f8f8-f8f8-f8f8-f8f8-f8f8f8f8f8f8",
  "numeric_code": "123-456-789",
  "original_name": "document.pdf",
  "size_bytes": 5242880,
  "expires_at": "2026-04-13T12:34:56Z",
  "created_at": "2026-04-06T12:34:56Z"
}
```

---

#### `POST /api/file/:id/report` - Report File
Reports a file for inappropriate content. File is automatically deleted after reaching report threshold.

**Parameters**:
- `id` (path, required) - File ID (UUID format)

**Headers**:
- `Content-Type: application/json`
- `X-CSRF-Token: <token>` (required)

**Response** (200 OK):
```json
{
  "success": true,
  "message": "File reported successfully"
}
```

**Status Codes**:
- `200` - Report submitted
- `409` - User has already reported this file
- `404` - File not found
- `410` - File already expired or deleted

---

### Authenticated User API Routes

All `/api/me/*` routes require a valid CNS-authenticated browser session.

#### `GET /api/me/recent-uploads` - List Recent Owned Uploads
Returns recent uploads that belong to the authenticated CNS user.

**Headers**:
- `X-CSRF-Token: <token>` (required)

**Response** (200 OK):
```json
{
  "items": [
    {
      "file_id": "f8f8f8f8f8f8f8f8f",
      "filename": "document.pdf",
      "size_bytes": 5242880,
      "created_at": "2026-04-06T12:34:56Z",
      "expires_at": "2026-04-13T12:34:56Z",
      "share_url": "http://localhost:8085/shared/f8f8f8f8f8f8f8f8f"
    }
  ]
}
```

---

#### `GET /api/me/files/:id/access` - Get Wrapped Key Access Data
Returns file metadata and key envelopes for an owned file and requesting device.

**Parameters**:
- `id` (path, required) - File ID

**Query Parameters**:
- `device_id` (string, required) - Registered device ID requesting access

**Headers**:
- `X-CSRF-Token: <token>` (required)

**Response** (200 OK):
```json
{
  "file": {
    "id": "f8f8f8f8f8f8f8f8f",
    "numeric_code": "123456789012",
    "original_name": "document.pdf",
    "size_bytes": 5242880,
    "expires_at": "2026-04-13T12:34:56Z",
    "created_at": "2026-04-06T12:34:56Z"
  },
  "file_key_envelope": {
    "wrapped_dek_b64": "...",
    "dek_wrap_alg": "AES-GCM-UK-v1",
    "dek_wrap_nonce_b64": "...",
    "dek_wrap_version": 1
  },
  "user_key_envelope": {
    "wrapped_uk_b64": "...",
    "uk_wrap_alg": "RSA-OAEP-2048-v1",
    "uk_wrap_meta": {
      "type": "self-wrap",
      "device_id": "11111111-2222-3333-4444-555555555555"
    },
    "key_version": 1
  }
}
```

**Status Codes**:
- `200` - Access data returned
- `400` - Missing or invalid request parameters
- `403` - Device not authorized
- `404` - File not owned/not found/expired

---

#### `POST /api/me/devices/register` - Register or Refresh Device
Registers device key material for the authenticated user. If a wrapped user key is provided,
the device is immediately trusted.

**Headers**:
- `Content-Type: application/json`
- `X-CSRF-Token: <token>` (required)

**Request Body**:
```json
{
  "device_id": "11111111-2222-3333-4444-555555555555",
  "device_label": "CNS Laptop",
  "public_key_jwk": {
    "kty": "RSA",
    "n": "...",
    "e": "AQAB"
  },
  "key_algorithm": "RSA-OAEP-2048",
  "key_version": 1,
  "wrapped_user_key_b64": "...",
  "uk_wrap_alg": "RSA-OAEP-2048-v1",
  "uk_wrap_meta": {
    "type": "self-wrap",
    "device_id": "11111111-2222-3333-4444-555555555555"
  }
}
```

**Response** (200 OK):
```json
{
  "device_id": "11111111-2222-3333-4444-555555555555",
  "needs_enrollment": false
}
```

---

#### `POST /api/me/devices/enrollments` - Create Enrollment Request
Creates a pending trusted-device enrollment request for the authenticated user.

**Headers**:
- `Content-Type: application/json`
- `X-CSRF-Token: <token>` (required)

**Request Body**:
```json
{
  "request_device_id": "11111111-2222-3333-4444-555555555555"
}
```

**Response** (200 OK):
```json
{
  "enrollment_id": "aaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
  "verification_code": "123456",
  "expires_at": "2026-04-11T10:25:00Z"
}
```

---

#### `GET /api/me/devices/enrollments/pending` - List Pending Enrollments
Lists non-expired pending enrollments for the authenticated user.

**Headers**:
- `X-CSRF-Token: <token>` (required)

**Response** (200 OK):
```json
{
  "items": [
    {
      "id": "aaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
      "cns_user_id": 123,
      "request_device_id": "11111111-2222-3333-4444-555555555555",
      "verification_code": "123456",
      "status": "pending",
      "expires_at": "2026-04-11T10:25:00Z",
      "created_at": "2026-04-11T10:15:00Z"
    }
  ]
}
```

#### `GET /api/me/devices/ws` - Device Enrollment WebSocket
Streams real-time enrollment events for the authenticated user so trusted sessions can react immediately when a new browser requests access.

**Authentication**:
- CNS auth cookie required

**Incoming Messages**:
```json
{
  "type": "device_enrollment_created",
  "enrollment": {
    "id": "aaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
    "request_device_id": "11111111-2222-3333-4444-555555555555",
    "status": "pending",
    "verification_code": "123456"
  },
  "request_device": {
    "id": "11111111-2222-3333-4444-555555555555",
    "device_label": "CNS Laptop"
  },
  "pending_count": 1
}
```

The same stream is also used for `device_enrollment_approved` and `device_enrollment_rejected` updates.

#### `POST /api/me/devices/recover` - Recover Lost Trusted Device
Rotates trusted-device state and makes the current browser the new trusted device. Use this when the old trusted device is lost and you want to keep using this browser.

**Headers**:
- `Content-Type: application/json`
- `X-CSRF-Token: <token>` (required)

**Request Body**:
```json
{
  "device_id": "11111111-2222-3333-4444-555555555555",
  "device_label": "CNS Laptop",
  "public_key_jwk": {
    "kty": "RSA",
    "n": "...",
    "e": "AQAB"
  },
  "key_algorithm": "RSA-OAEP-2048",
  "key_version": 1,
  "wrapped_user_key_b64": "...",
  "uk_wrap_alg": "RSA-OAEP-2048-v1",
  "uk_wrap_meta": {
    "type": "recovery-reset"
  }
}
```

**Response** (200 OK):
```json
{
  "device_id": "11111111-2222-3333-4444-555555555555",
  "needs_enrollment": false
}
```

This is a key rotation, not a silent restore. Files encrypted with the previous user key will no longer be readable from the recovered browser unless they are re-shared or re-uploaded.

---

#### `POST /api/me/devices/enrollments/:id/approve` - Approve Enrollment
Approves a pending enrollment from a trusted device and stores wrapped user key for the request device.

**Parameters**:
- `id` (path, required) - Enrollment ID

**Headers**:
- `Content-Type: application/json`
- `X-CSRF-Token: <token>` (required)

**Request Body**:
```json
{
  "approver_device_id": "99999999-2222-3333-4444-555555555555",
  "verification_code": "123456",
  "wrapped_user_key_b64": "...",
  "uk_wrap_alg": "RSA-OAEP-2048-v1",
  "uk_wrap_meta": {
    "approved_from": "99999999-2222-3333-4444-555555555555"
  }
}
```

**Response** (200 OK):
```json
{
  "success": true
}
```

#### Device Recovery Note
ShareIt keeps the user key in browser storage on trusted devices. If the only trusted device is lost, a new browser cannot decrypt previously protected data unless the user key is recovered from another trusted session first. The approval flow now makes device B wait for approval cleanly and notifies device A instantly, but it does not invent a new recovery secret.

---

## Desktop API Routes

All desktop routes require:
- **CORS Headers** enabled for cross-origin requests
- **API Key Authentication** via `X-API-KEY` header

### Desktop Authentication

#### `GET /desktop/auth/verify` - Verify API Key
Validates an API key without requiring authentication. Used for initial key verification.

**Query Parameters**:
- `key` (string, required) - API key to verify

**Response** (200 OK):
```json
{
  "status": "valid",
  "owner": "My Desktop"
}
```

**Status Codes**:
- `200` - Key is valid
- `401` - Key is invalid or inactive

---

#### `GET /desktop/ws` - WebSocket Connection
Establishes a WebSocket connection for real-time notifications about new file uploads.

**Headers**:
- `X-API-KEY: <api_key>` (required)

**Authentication**: Desktop API Key authentication required

**Message Format** (incoming):
```json
{
  "type": "new_file",
  "file": {
    "id": "f8f8f8f8-f8f8-f8f8-f8f8-f8f8f8f8f8f8",
    "numeric_code": "123-456-789",
    "file_name": "document.pdf",
    "file_size": 5242880,
    "expires_at": "2026-04-13T12:34:56Z",
    "uploaded_at": "2026-04-06T12:34:56Z"
  }
}
```

---

### Desktop Upload Routes

#### `POST /desktop/upload/init` - Initialize Desktop Upload
Initiates an upload session from a desktop client.

**Headers**:
- `Content-Type: application/json`
- `X-API-KEY: <api_key>` (required)

**Request Body**:
```json
{
  "file_name": "document.pdf",
  "file_size": 5242880,
  "total_chunks": 10,
  "chunk_size": 524288
}
```

**Response** (200 OK):
```json
{
  "session_id": "550e8400-e29b-41d4-a716-446655440000",
  "file_id": "f8f8f8f8-f8f8-f8f8-f8f8-f8f8f8f8f8f8",
  "chunk_size": 524288,
  "total_chunks": 10,
  "api_key_id": "key-id-123"
}
```

---

#### `POST /desktop/upload/chunk` - Upload Desktop Chunk
Uploads a file chunk from desktop client.

**Headers**:
- `Content-Type: multipart/form-data`
- `X-API-KEY: <api_key>` (required)

**Form Parameters**:
- `session_id` (string, required) - Session ID
- `chunk_index` (integer, required) - Zero-based index
- `file` (file, required) - Binary chunk data

**Response** (200 OK):
```json
{
  "chunk_index": 0,
  "received": true
}
```

---

#### `POST /desktop/upload/complete` - Complete Desktop Upload
Marks all chunks as received.

**Headers**:
- `Content-Type: application/json`
- `X-API-KEY: <api_key>` (required)

**Request Body**:
```json
{
  "session_id": "550e8400-e29b-41d4-a716-446655440000",
  "confirmed": true
}
```

**Response** (200 OK):
```json
{
  "session_id": "550e8400-e29b-41d4-a716-446655440000",
  "file_id": "f8f8f8f8-f8f8-f8f8-f8f8-f8f8f8f8f8f8",
  "pending_expires_at": "2026-04-07T12:34:56Z"
}
```

---

#### `POST /desktop/upload/finalize` - Finalize Desktop Upload
Sets expiration and generates share link for desktop uploads.

**Headers**:
- `Content-Type: application/json`
- `X-API-KEY: <api_key>` (required)

**Request Body**:
```json
{
  "session_id": "550e8400-e29b-41d4-a716-446655440000",
  "duration": "7d"
}
```

**Response** (200 OK):
```json
{
  "file_id": "f8f8f8f8-f8f8-f8f8-f8f8-f8f8f8f8f8f8",
  "numeric_code": "123-456-789",
  "file_name": "document.pdf",
  "file_size": 5242880,
  "expires_at": "2026-04-13T12:34:56Z",
  "share_url": "http://localhost:8085/shared/f8f8f8f8-f8f8-f8f8-f8f8-f8f8f8f8f8f8"
}
```

---

#### `GET /desktop/upload/status/:session_id` - Check Upload Status
Checks the assembly progress of desktop uploads.

**Parameters**:
- `session_id` (path, required) - Session ID

**Headers**:
- `X-API-KEY: <api_key>` (required)

**Response** (200 OK):
```json
{
  "session_id": "550e8400-e29b-41d4-a716-446655440000",
  "received_chunks": 8,
  "total_chunks": 10,
  "status": "assembling"
}
```

---

### Desktop File Management Routes

#### `GET /desktop/files` - List Files for API Key
Lists all files uploaded by this API key.

**Headers**:
- `X-API-KEY: <api_key>` (required)

**Query Parameters** (optional):
- `limit` (integer) - Max results (default: 50)
- `offset` (integer) - Pagination offset (default: 0)

**Response** (200 OK):
```json
{
  "files": [
    {
      "id": "f8f8f8f8-f8f8-f8f8-f8f8-f8f8f8f8f8f8",
      "numeric_code": "123-456-789",
      "file_name": "document.pdf",
      "file_size": 5242880,
      "expires_at": "2026-04-13T12:34:56Z",
      "uploaded_at": "2026-04-06T12:34:56Z"
    }
  ],
  "total": 5
}
```

---

#### `GET /desktop/files/:id` - Get File Details
Retrieves metadata for a specific file owned by this API key.

**Parameters**:
- `id` (path, required) - File ID

**Headers**:
- `X-API-KEY: <api_key>` (required)

**Response** (200 OK):
```json
{
  "id": "f8f8f8f8-f8f8-f8f8-f8f8-f8f8f8f8f8f8",
  "numeric_code": "123-456-789",
  "file_name": "document.pdf",
  "file_size": 5242880,
  "expires_at": "2026-04-13T12:34:56Z",
  "uploaded_at": "2026-04-06T12:34:56Z"
}
```

**Status Codes**:
- `200` - File found
- `404` - File not found or doesn't belong to API key
- `410` - File expired or deleted

---

#### `GET /desktop/files/:id/download` - Download File
Downloads a file owned by this API key.

**Parameters**:
- `id` (path, required) - File ID

**Headers**:
- `X-API-KEY: <api_key>` (required)

**Response** (200 OK):
- Binary file content
- `Content-Disposition` header with filename

**Status Codes**:
- `200` - File downloaded
- `404` - File not found
- `410` - File expired or deleted

---

## Rate Limiting

- **Web Upload Init**: Limited to 1 request per IP per 60 seconds
- **Desktop API**: No per-endpoint rate limiting (but IP-based overall limits may apply)
- Rate limit info returned in response headers (if applicable)

## Security Considerations

1. **CSRF Protection**: All web API endpoints require CSRF tokens
2. **Encryption**: Files are encrypted client-side before upload
3. **API Keys**: Desktop API keys should be treated as secrets
4. **IP Tracking**: All uploads are tracked by IP for security and reporting
5. **File Expiration**: Files are automatically deleted after expiration
6. **Report System**: Files can be reported and auto-deleted after threshold

## Pagination & Filtering

The `/desktop/files` endpoint supports pagination:

```
GET /desktop/files?limit=20&offset=40
```

- `limit`: 1-100 (default 50)
- `offset`: 0+ (default 0)

---

## Example Usage

### Web Upload Flow

```bash
# 1. Initialize upload
curl -X POST http://localhost:8085/api/upload/init \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: <token>" \
  -d '{"file_name":"test.pdf","file_size":1000,"total_chunks":1,"chunk_size":1000}'

# 2. Upload chunk
curl -X POST http://localhost:8085/api/upload/chunk \
  -H "X-CSRF-Token: <token>" \
  -F "session_id=<session_id>" \
  -F "chunk_index=0" \
  -F "file=@test.pdf"

# 3. Complete upload
curl -X POST http://localhost:8085/api/upload/complete \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: <token>" \
  -d '{"session_id":"<session_id>","confirmed":true}'

# 4. Finalize with expiration
curl -X POST http://localhost:8085/api/upload/finalize \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: <token>" \
  -d '{"session_id":"<session_id>","duration":"7d"}'
```

### Desktop Upload Flow

```bash
# 1. Verify API key
curl -X GET "http://localhost:8085/desktop/auth/verify?key=<api_key>"

# 2. Initialize upload
curl -X POST http://localhost:8085/desktop/upload/init \
  -H "Content-Type: application/json" \
  -H "X-API-KEY: <api_key>" \
  -d '{"file_name":"test.pdf","file_size":1000,"total_chunks":1,"chunk_size":1000}'

# 3. Upload chunk
curl -X POST http://localhost:8085/desktop/upload/chunk \
  -H "X-API-KEY: <api_key>" \
  -F "session_id=<session_id>" \
  -F "chunk_index=0" \
  -F "file=@test.pdf"

# 4. Complete and finalize (same as web)
```

---