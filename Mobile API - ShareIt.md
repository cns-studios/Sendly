# Mobile API - ShareIt

Base path: `/android/`

**Required Api functionallitys**

- List files
- Upload Endpoints (Chunk based)
- Download Endpoints (Stream file)
- Notification WebSocket
- New Pending Approvals WebSocket
- Waiting for Approval - Status WebSocket
- this.device Recovery Endpoints
- Connected Devices (to account) list Endpoint > FETCH, JSON List
- Rename device name

Implementation now starts on the ShareIt backend with bearer-token auth and no CSRF on the `/android` surface.