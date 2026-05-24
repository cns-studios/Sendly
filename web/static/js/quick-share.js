(function() {
    'use strict';

    const CHUNK_SIZE = 5 * 1024 * 1024;
    const AUTHENTICATED = window.CONFIG?.authenticated || false;
    const CNS_USER_ID = window.CONFIG?.cnsUserId || 0;
    const CNS_USERNAME = window.CONFIG?.cnsUsername || '';
    const TOS_VERSION = window.CONFIG?.tosVersion || '2026-04-05';
    const TOS_COOKIE_NAME = 'shareit_tos_accepted';
    const TUNNEL_MAX_FILE_SIZE = 3 * 1024 * 1024 * 1024;
    const PARALLEL_CHUNK_UPLOADS = window.CONFIG?.parallelChunkUploads || 6;
    const MAX_CHUNK_UPLOAD_RETRIES = 5;
    const TUNNEL_POLL_INTERVAL = 2000;
    const SESSION_PASSWORD_PREFIX = 'shareit_tunnel_pw_';

    let activeTunnel = null;
    let tunnelPollTimer = null;
    let authDeviceIdentity = null;
    let authUserKeyRaw = null;
    let isUploading = false;
    let sessionPassword = null;
    let participants = [];
    let isHost = false;
    let hasStarted = false;
    let guestNameMap = new Map();
    let guestCounter = 0;
    let ephemeralKeyPair = null;
    let myDeviceId = '';
    let joinCodeInput = '';

    const initialView = document.getElementById('initialView');
    const createView = document.getElementById('createView');
    const joinView = document.getElementById('joinView');
    const sessionView = document.getElementById('sessionView');
    const queueView = document.getElementById('queueView');
    const bottomBar = document.getElementById('bottomBar');
    const joinBottomBar = document.getElementById('joinBottomBar');
    const createBtn = document.getElementById('createBtn');
    const joinBtn = document.getElementById('joinBtn');
    const startBtn = document.getElementById('startBtn');
    const joinSubmitBtn = document.getElementById('joinSubmitBtn');
    const leaveBtn = document.getElementById('leaveBtn');
    const fileList = document.getElementById('fileList');
    const fileListEmpty = document.getElementById('fileListEmpty');
    const dropZone = document.getElementById('dropZone');
    const fileInput = document.getElementById('fileInput');
    const dropMainText = document.getElementById('dropMainText');
    const dropSubText = document.getElementById('dropSubText');
    const connectedText = document.getElementById('connectedText');
    const codeSquares = document.querySelectorAll('#codeSquares .code-square');
    const joinCodeSquares = document.querySelectorAll('#joinCodeSquares .join-code-square');
    const queueCodeSquares = document.querySelectorAll('#queueCodeSquares .code-square');
    const peopleRow = document.getElementById('peopleRow');
    const queuePeopleRow = document.getElementById('queuePeopleRow');
    let notificationTimer = null;
    const tosOverlay = document.getElementById('tos-overlay');
    const tosAcceptBtn = document.getElementById('tos-accept-btn');
    const tosDeclineBtn = document.getElementById('tos-decline-btn');

    function getCookieValue(name) {
        const value = `; ${document.cookie}`;
        const parts = value.split(`; ${name}=`);
        if (parts.length === 2) return parts.pop().split(';').shift();
        return '';
    }

    function setCookie(name, value, maxAgeSeconds) {
        document.cookie = `${name}=${encodeURIComponent(value)}; path=/; max-age=${maxAgeSeconds}; SameSite=Lax`;
    }

    function hasAcceptedCurrentTOS() {
        return getCookieValue(TOS_COOKIE_NAME) === TOS_VERSION;
    }

    function setupTOSGate() {
        if (!tosOverlay) return;
        if (hasAcceptedCurrentTOS()) {
            tosOverlay.classList.add('hidden');
            return;
        }
        tosOverlay.classList.remove('hidden');
        tosAcceptBtn?.addEventListener('click', () => {
            setCookie(TOS_COOKIE_NAME, TOS_VERSION, 31536000);
            tosOverlay.classList.add('hidden');
        });
        tosDeclineBtn?.addEventListener('click', () => {
            window.location.href = 'https://cns-studios.com';
        });
    }

    function getOrCreateGuestDeviceId() {
        if (myDeviceId && !AUTHENTICATED) return myDeviceId;
        const stored = localStorage.getItem('shareit_guest_device_id');
        if (stored) {
            if (!AUTHENTICATED) myDeviceId = stored;
            return stored;
        }
        const created = crypto.randomUUID();
        localStorage.setItem('shareit_guest_device_id', created);
        if (!AUTHENTICATED) myDeviceId = created;
        return created;
    }

    function extractDeviceID(participant) {
        if (!participant) return '';
        const deviceID = participant.device_id;
        if (!deviceID) return '';
        if (typeof deviceID === 'string') return deviceID;
        if (typeof deviceID === 'object') {
            if (deviceID.Valid === false) return '';
            return deviceID.String || '';
        }
        return '';
    }

    function extractUserID(participant) {
        if (!participant) return 0;
        const userID = participant.cns_user_id;
        if (!userID) return 0;
        if (typeof userID === 'number') return userID;
        if (typeof userID === 'object') {
            if (userID.Valid === false) return 0;
            return Number(userID.Int64 || 0);
        }
        return 0;
    }

    function buildHeaders(extra = {}) {
        const headers = { ...extra };
        const csrf = getCookieValue('csrf_token');
        if (csrf) headers['X-CSRF-Token'] = csrf;
        if (myDeviceId) headers['X-Device-ID'] = myDeviceId;
        return headers;
    }

    async function ensureEphemeralKeyPair() {
        if (ephemeralKeyPair) return ephemeralKeyPair;

        const cached = sessionStorage.getItem('shareit_ephemeral_kp');
        if (cached) {
            ephemeralKeyPair = JSON.parse(cached);
            return ephemeralKeyPair;
        }

        const keyPair = await crypto.subtle.generateKey(
            {
                name: 'RSA-OAEP',
                modulusLength: 2048,
                publicExponent: new Uint8Array([1, 0, 1]),
                hash: 'SHA-256'
            },
            true,
            ['encrypt', 'decrypt']
        );

        const publicKeyJWK = await crypto.subtle.exportKey('jwk', keyPair.publicKey);
        const privateKeyJWK = await crypto.subtle.exportKey('jwk', keyPair.privateKey);

        ephemeralKeyPair = { publicKeyJWK, privateKeyJWK };
        sessionStorage.setItem('shareit_ephemeral_kp', JSON.stringify(ephemeralKeyPair));
        return ephemeralKeyPair;
    }

    async function wrapWithPublicKey(plaintextBytes, publicKeyJWK) {
        const publicKey = await crypto.subtle.importKey(
            'jwk',
            publicKeyJWK,
            { name: 'RSA-OAEP', hash: 'SHA-256' },
            false,
            ['encrypt']
        );
        const wrapped = await crypto.subtle.encrypt({ name: 'RSA-OAEP' }, publicKey, plaintextBytes);
        return new Uint8Array(wrapped);
    }

    async function unwrapWithPrivateKey(wrappedBytes, privateKeyJWK) {
        const privateKey = await crypto.subtle.importKey(
            'jwk',
            privateKeyJWK,
            { name: 'RSA-OAEP', hash: 'SHA-256' },
            false,
            ['decrypt']
        );
        const raw = await crypto.subtle.decrypt({ name: 'RSA-OAEP' }, privateKey, wrappedBytes);
        return new Uint8Array(raw);
    }

    async function ensureDeviceReady() {
        if (!AUTHENTICATED) return true;
        try {
            authDeviceIdentity = await SecureCrypto.getOrCreateDeviceIdentity();
            authUserKeyRaw = SecureCrypto.getUserKeyRaw(CNS_USER_ID);
            if (!authUserKeyRaw) {
                authUserKeyRaw = SecureCrypto.generateUserKeyRaw();
                SecureCrypto.saveUserKeyRaw(CNS_USER_ID, authUserKeyRaw);
            }

            const response = await fetch('/api/me/devices/register', {
                method: 'POST',
                headers: buildHeaders({ 'Content-Type': 'application/json' }),
                body: JSON.stringify({
                    device_id: authDeviceIdentity.deviceId,
                    device_label: `${CNS_USERNAME || 'ShareIt User'} device`,
                    public_key_jwk: authDeviceIdentity.publicKeyJWK,
                    key_algorithm: authDeviceIdentity.keyAlgorithm,
                    key_version: authDeviceIdentity.keyVersion,
                })
            });

            if (!response.ok) throw new Error('Device registration failed');

            const payload = await response.json();
            if (payload.needs_enrollment) {
                showErrorBanner('Approve this device from a trusted device before using Quick Share.');
                return false;
            }

            if (payload.user_key_envelope?.wrapped_uk_b64 && !authUserKeyRaw) {
                const wrappedUK = SecureCrypto.fromBase64(payload.user_key_envelope.wrapped_uk_b64);
                authUserKeyRaw = await SecureCrypto.unwrapUserKeyForDevice(
                    wrappedUK,
                    authDeviceIdentity.privateKeyJWK
                );
                SecureCrypto.saveUserKeyRaw(CNS_USER_ID, authUserKeyRaw);
            }

            return true;
        } catch (error) {
            console.error('Device ready failed:', error);
            return false;
        }
    }

    function setView(view) {
        initialView.style.display = view === 'initial' ? '' : 'none';
        createView.classList.toggle('active', view === 'create');
        joinView.classList.toggle('active', view === 'join');
        sessionView.classList.toggle('active', view === 'session');
        queueView.classList.toggle('active', view === 'queue');
        bottomBar.style.display = view === 'create' ? '' : 'none';
        joinBottomBar.style.display = view === 'join' ? '' : 'none';

        if (startBtn) {
            startBtn.style.display = (view === 'create' && isHost && !hasStarted) ? '' : 'none';
        }
    }

    function setCodeDisplay(squares, code) {
        squares.forEach((sq, i) => {
            sq.textContent = code[i] || '\u2013';
            sq.classList.toggle('filled', !!code[i]);
        });
    }

    function getParticipantName(participant) {
        const userID = extractUserID(participant);
        if (userID) return 'User';

        const deviceID = extractDeviceID(participant);
        if (deviceID) {
            if (!guestNameMap.has(deviceID)) {
                guestCounter++;
                guestNameMap.set(deviceID, `Guest ${guestCounter}`);
            }
            return guestNameMap.get(deviceID);
        }

        return 'Guest';
    }

    function renderParticipants(items) {
        const container = isHost ? peopleRow : queuePeopleRow;
        if (!container) return;

        const allParticipants = Array.isArray(items) ? items : [];
        const myCurrentDeviceID = myDeviceId || (AUTHENTICATED ? authDeviceIdentity?.deviceId || '' : getOrCreateGuestDeviceId());

        container.innerHTML = '';

        if (allParticipants.length === 0) {
            const empty = document.createElement('div');
            empty.className = 'recent-state';
            empty.textContent = 'Nobody joined yet';
            empty.style.color = '#888';
            empty.style.fontSize = '0.85rem';
            empty.style.textAlign = 'center';
            empty.style.padding = '1rem 0';
            container.appendChild(empty);
        } else {
            allParticipants.forEach(p => {
                const person = document.createElement('div');
                person.className = 'person';
                const isSelf = extractDeviceID(p) === myCurrentDeviceID;
                person.innerHTML = `
                    <div class="person-circle">
                        <svg xmlns="http://www.w3.org/2000/svg" width="30" height="30" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                            <path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>
                        </svg>
                    </div>
                    <span class="person-name">${getParticipantName(p)}${isSelf ? ' (you)' : ''}</span>
                `;
                container.appendChild(person);
            });
        }

        if (connectedText) {
            connectedText.textContent = `${allParticipants.length} connected`;
        }
    }

    function renderTunnelFiles(files) {
        if (!fileList || !fileListEmpty) return;

        const items = Array.isArray(files) ? files : [];
        fileList.innerHTML = '';

        if (items.length === 0) {
            fileList.classList.add('hidden');
            fileListEmpty.classList.remove('hidden');
            return;
        }

        fileList.classList.remove('hidden');
        fileListEmpty.classList.add('hidden');

        items.forEach(item => {
            const el = document.createElement('div');
            el.className = 'file-entry';
            el.dataset.fileId = item.file_id;
            el.innerHTML = `
                <div class="file-entry-left">
                    <span class="file-name">${escapeHtml(item.filename)}</span>
                    <span class="file-info">${SecureCrypto.formatFileSize(item.size_bytes)}</span>
                </div>
                <div class="file-entry-right">
                    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="file-download-btn" title="Download">
                        <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/>
                    </svg>
                </div>
            `;
            el.querySelector('.file-download-btn').addEventListener('click', () => {
                downloadTunnelFile(item.file_id, item.filename);
            });
            fileList.appendChild(el);
        });
    }

    function escapeHtml(str) {
        const div = document.createElement('div');
        div.textContent = str;
        return div.innerHTML;
    }

    async function resolveSessionPassword(fileId) {
        if (sessionPassword) return sessionPassword;

        if (fileId) {
            const cached = SecureCrypto.getCachedFileKey(fileId);
            if (cached) {
                sessionPassword = cached;
                return sessionPassword;
            }
        }

        if (activeTunnel?.id) {
            const stored = localStorage.getItem(SESSION_PASSWORD_PREFIX + activeTunnel.id);
            if (stored) {
                sessionPassword = stored;
                return sessionPassword;
            }
        }

        if (activeTunnel?.id && ephemeralKeyPair && myDeviceId) {
            try {
                const res = await fetch(
                    `/api/me/tunnels/${activeTunnel.id}/envelopes/${encodeURIComponent(myDeviceId)}`,
                    { headers: buildHeaders() }
                );

                if (res.ok) {
                    const data = await res.json();
                    if (data.ready && data.envelope) {
                        const wrappedBytes = SecureCrypto.fromBase64(data.envelope.wrapped_dek_b64);
                        const rawBytes = await unwrapWithPrivateKey(wrappedBytes, ephemeralKeyPair.privateKeyJWK);
                        const pw = new TextDecoder().decode(rawBytes);

                        sessionPassword = pw;
                        localStorage.setItem(SESSION_PASSWORD_PREFIX + activeTunnel.id, pw);
                        if (fileId) SecureCrypto.cacheFileKey(fileId, pw);
                        return sessionPassword;
                    }
                }
            } catch (error) {
                console.warn('Failed to resolve session password from participant envelope:', error);
            }
        }

        if (AUTHENTICATED && authUserKeyRaw && activeTunnel?.id && fileId) {
            try {
                const accessRes = await fetch(
                    `/api/tunnels/${activeTunnel.id}/files/${fileId}/access`,
                    { headers: buildHeaders() }
                );

                if (accessRes.ok) {
                    const accessData = await accessRes.json();
                    const envelope = accessData.file_key_envelope;
                    if (envelope?.wrapped_dek_b64) {
                        const wrappedDEK = SecureCrypto.fromBase64(envelope.wrapped_dek_b64);
                        const nonce = envelope.dek_wrap_nonce_b64
                            ? SecureCrypto.fromBase64(envelope.dek_wrap_nonce_b64)
                            : null;

                        const rawDEK = await SecureCrypto.unwrapSecretWithUserKey(
                            wrappedDEK,
                            nonce,
                            authUserKeyRaw
                        );

                        const pw = new TextDecoder().decode(rawDEK);
                        sessionPassword = pw;
                        localStorage.setItem(SESSION_PASSWORD_PREFIX + activeTunnel.id, pw);
                        SecureCrypto.cacheFileKey(fileId, pw);
                        return sessionPassword;
                    }
                }
            } catch (error) {
                console.warn('Failed to resolve session password from file access envelope:', error);
            }
        }

        return null;
    }

    async function downloadTunnelFile(fileId, fileName) {
        try {
            const password = await resolveSessionPassword(fileId);
            if (!password) {
                showErrorBanner('No decryption key available yet. Try again in a moment.');
                return;
            }

            const response = await fetch(`/api/file/${fileId}/download`, {
                headers: buildHeaders()
            });

            if (!response.ok) {
                const contentType = response.headers.get('content-type') || '';
                if (contentType.includes('application/json')) {
                    const errorData = await response.json();
                    throw new Error(errorData.error || 'Download failed');
                }
                throw new Error(`Download failed (${response.status})`);
            }

            const encryptedBlob = await response.blob();
            const decrypted = await SecureCrypto.decryptBlob(encryptedBlob, password);

            const url = URL.createObjectURL(new Blob([decrypted]));
            const a = document.createElement('a');
            a.href = url;
            a.download = fileName;
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            URL.revokeObjectURL(url);
        } catch (error) {
            console.error('Tunnel file download failed:', error);
            showErrorBanner('Download failed: ' + error.message);
        }
    }

    async function pushEnvelopesToGuests() {
        if (!isHost || !sessionPassword || !activeTunnel?.id || !myDeviceId) return;

        try {
            const res = await fetch(`/api/me/tunnels/${activeTunnel.id}/participant-keys`, {
                headers: buildHeaders()
            });

            if (!res.ok) return;

            const data = await res.json();
            const guestParticipants = Array.isArray(data.participants) ? data.participants : [];
            const pending = guestParticipants.filter(p => !p.has_envelope);

            if (pending.length === 0) return;

            const passwordBytes = new TextEncoder().encode(sessionPassword);

            for (const participant of pending) {
                try {
                    const wrapped = await wrapWithPublicKey(passwordBytes, participant.public_key_jwk);
                    await fetch(`/api/me/tunnels/${activeTunnel.id}/envelopes`, {
                        method: 'POST',
                        headers: buildHeaders({ 'Content-Type': 'application/json' }),
                        body: JSON.stringify({
                            participant_device_id: participant.device_id,
                            wrapped_dek_b64: SecureCrypto.toBase64(wrapped),
                            dek_wrap_alg: 'RSA-OAEP-2048',
                            dek_wrap_version: 1
                        })
                    });
                } catch (error) {
                    console.warn('Failed to push envelope for participant:', participant.device_id, error);
                }
            }
        } catch (error) {
            console.warn('pushEnvelopesToGuests failed:', error);
        }
    }

    async function handleCreateTunnel() {
        if (AUTHENTICATED) {
            const ready = await ensureDeviceReady();
            if (!ready) return;
            myDeviceId = authDeviceIdentity.deviceId;
        } else {
            myDeviceId = getOrCreateGuestDeviceId();
        }

        await ensureEphemeralKeyPair();

        try {
            const response = await fetch('/api/me/tunnels/start', {
                method: 'POST',
                headers: buildHeaders({ 'Content-Type': 'application/json' }),
                body: JSON.stringify({
                    duration: '24h',
                    device_id: myDeviceId
                })
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.error || 'Failed to create tunnel');
            }

            const payload = await response.json();
            activeTunnel = payload.tunnel;
            participants = payload.participants || [];
            sessionPassword = null;
            isHost = true;
            hasStarted = false;

            setCodeDisplay(codeSquares, activeTunnel.code);
            setCodeDisplay(queueCodeSquares, activeTunnel.code);
            renderParticipants(participants);
            setView('create');
            startTunnelPolling();
        } catch (error) {
            console.error('Create tunnel failed:', error);
            showErrorBanner('Failed to create tunnel: ' + error.message);
        }
    }

    async function handleJoinTunnel() {
        if (joinCodeInput.length !== 4) return;

        if (AUTHENTICATED) {
            const ready = await ensureDeviceReady();
            if (!ready) return;
            myDeviceId = authDeviceIdentity.deviceId;
        } else {
            myDeviceId = getOrCreateGuestDeviceId();
        }

        await ensureEphemeralKeyPair();

        try {
            const response = await fetch('/api/me/tunnels/join', {
                method: 'POST',
                headers: buildHeaders({ 'Content-Type': 'application/json' }),
                body: JSON.stringify({
                    code: joinCodeInput,
                    device_id: myDeviceId,
                    public_key_jwk: ephemeralKeyPair.publicKeyJWK,
                    key_algorithm: 'RSA-OAEP-2048',
                    key_version: 1
                })
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.error || 'Failed to join tunnel');
            }

            const payload = await response.json();
            activeTunnel = payload.tunnel;
            participants = payload.participants || [];
            isHost = false;
            hasStarted = activeTunnel.status === 'active';

            const storedPw = localStorage.getItem(SESSION_PASSWORD_PREFIX + activeTunnel.id);
            sessionPassword = storedPw || null;

            setCodeDisplay(queueCodeSquares, activeTunnel.code);
            renderParticipants(participants);
            setView(hasStarted ? 'session' : 'queue');
            startTunnelPolling();
        } catch (error) {
            console.error('Join tunnel failed:', error);
            showErrorBanner('Failed to join tunnel: ' + error.message);
            joinCodeInput = '';
            setCodeDisplay(joinCodeSquares, '');
            joinSubmitBtn.disabled = true;
            joinSubmitBtn.classList.add('disabled');
        }
    }

    async function handleStartTunnel() {
        if (!activeTunnel?.id || !isHost) return;

        try {
            const response = await fetch(`/api/me/tunnels/${activeTunnel.id}/confirm`, {
                method: 'POST',
                headers: buildHeaders({ 'Content-Type': 'application/json' }),
                body: JSON.stringify({ device_id: myDeviceId })
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.error || 'Failed to start');
            }

            const payload = await response.json();
            if (payload.tunnel) activeTunnel = payload.tunnel;

            hasStarted = true;
            setView('session');

            if (!sessionPassword) {
                sessionPassword = await SecureCrypto.generatePassword();
                localStorage.setItem(SESSION_PASSWORD_PREFIX + activeTunnel.id, sessionPassword);
            }

            await pushEnvelopesToGuests();
            await refreshTunnelState();
        } catch (error) {
            console.error('Start tunnel failed:', error);
            showErrorBanner('Failed to start session: ' + error.message);
        }
    }

    async function handleLeaveTunnel() {
        if (!activeTunnel?.id) return;

        try {
            const response = await fetch(`/api/me/tunnels/${activeTunnel.id}`, {
                method: 'DELETE',
                headers: buildHeaders({ 'Content-Type': 'application/json' }),
                body: JSON.stringify({ device_id: myDeviceId })
            });

            if (response.ok) {
                const data = await response.json();
                if (data.tunnel_ended) {
                    showErrorBanner('Session ended — you were the last one here.');
                }
            }
        } catch (error) {
            console.error('Leave tunnel failed:', error);
        }

        clearTunnelState();
        setView('initial');
    }

    async function refreshTunnelState() {
        if (!activeTunnel?.id) return;

        try {
            const response = await fetch(`/api/me/tunnels/${activeTunnel.id}?t=${Date.now()}`, {
                headers: buildHeaders()
            });

            if (!response.ok) {
                if (response.status === 410 || response.status === 404 || response.status === 403) {
                    clearTunnelState();
                    showErrorBanner('Quick share has ended.');
                    setView('initial');
                    return;
                }
                throw new Error('Failed to refresh tunnel state');
            }

            const payload = await response.json();
            if (payload?.tunnel) {
                activeTunnel = payload.tunnel;
                participants = payload.participants || [];

                const tunnelStatus = activeTunnel.status || '';
                if (!isHost && !hasStarted && tunnelStatus === 'active') {
                    hasStarted = true;
                    setView('session');
                    if (!sessionPassword) {
                        const storedPw = localStorage.getItem(SESSION_PASSWORD_PREFIX + activeTunnel.id);
                        if (storedPw) sessionPassword = storedPw;
                    }
                }

                if (isHost && sessionPassword) {
                    await pushEnvelopesToGuests();
                }

                updateTunnelUI();
            }

            renderTunnelFiles(payload?.files || []);
        } catch (error) {
            console.error('Tunnel refresh failed:', error);
        }
    }

    function updateTunnelUI() {
        if (!activeTunnel) return;
        setCodeDisplay(codeSquares, activeTunnel.code);
        setCodeDisplay(queueCodeSquares, activeTunnel.code);
        renderParticipants(participants);
    }

    function startTunnelPolling() {
        if (tunnelPollTimer) clearInterval(tunnelPollTimer);
        tunnelPollTimer = setInterval(refreshTunnelState, TUNNEL_POLL_INTERVAL);
    }

    function stopTunnelPolling() {
        if (tunnelPollTimer) {
            clearInterval(tunnelPollTimer);
            tunnelPollTimer = null;
        }
    }

    function clearTunnelState() {
        stopTunnelPolling();
        activeTunnel = null;
        sessionPassword = null;
        participants = [];
        isHost = false;
        hasStarted = false;
        guestNameMap.clear();
        guestCounter = 0;

        if (fileList) fileList.innerHTML = '';
        if (fileListEmpty) fileListEmpty.classList.remove('hidden');
        if (fileList) fileList.classList.add('hidden');
        if (connectedText) connectedText.textContent = '';
    }

    async function processFileForTunnel(file) {
        if (isUploading || !activeTunnel?.id) return;

        if (file.size > TUNNEL_MAX_FILE_SIZE) {
            showErrorBanner(`File too large. Maximum: ${SecureCrypto.formatFileSize(TUNNEL_MAX_FILE_SIZE)}`);
            return;
        }

        if (file.size === 0) {
            showErrorBanner('Cannot upload empty file.');
            return;
        }

        isUploading = true;
        dropMainText.textContent = 'Uploading...';
        dropSubText.textContent = file.name;

        try {
            if (isHost && !sessionPassword) {
                sessionPassword = await SecureCrypto.generatePassword();
                localStorage.setItem(SESSION_PASSWORD_PREFIX + activeTunnel.id, sessionPassword);
                await pushEnvelopesToGuests();
            }

            if (!isHost && !sessionPassword) {
                dropSubText.textContent = 'Waiting for encryption key...';
                for (let attempt = 0; attempt < 10; attempt++) {
                    const resolved = await resolveSessionPassword(null);
                    if (resolved) break;
                    await new Promise(r => setTimeout(r, 1500));
                }
                if (!sessionPassword) {
                    throw new Error('Could not obtain encryption key from host yet. Please try again in a moment.');
                }
            }

            const password = sessionPassword;
            const dekBytes = new TextEncoder().encode(password);
            let envelopePayload = {};

            if (AUTHENTICATED && authUserKeyRaw) {
                const wrapped = await SecureCrypto.wrapSecretWithUserKey(dekBytes, authUserKeyRaw);
                envelopePayload = {
                    wrapped_dek_b64: SecureCrypto.toBase64(wrapped.wrapped),
                    dek_wrap_alg: 'AES-GCM-UK-v1',
                    dek_wrap_nonce_b64: SecureCrypto.toBase64(wrapped.nonce),
                    dek_wrap_version: 1
                };
            }

            dropSubText.textContent = file.name;

            const encryptedBlob = await SecureCrypto.encryptFile(file, password, () => {});
            const totalChunks = Math.ceil(encryptedBlob.size / CHUNK_SIZE);

            const initRes = await fetch('/api/upload/init', {
                method: 'POST',
                headers: buildHeaders({ 'Content-Type': 'application/json' }),
                body: JSON.stringify({
                    file_name: file.name,
                    file_size: encryptedBlob.size,
                    total_chunks: totalChunks,
                    chunk_size: CHUNK_SIZE,
                    tunnel_id: activeTunnel.id
                })
            });

            if (!initRes.ok) {
                const error = await initRes.json();
                throw new Error(error.error || 'Init failed');
            }

            const initData = await initRes.json();
            const sessionId = initData.session_id;

            const concurrency = Math.max(1, Math.min(PARALLEL_CHUNK_UPLOADS, totalChunks));
            let nextChunkIndex = 0;

            const worker = async () => {
                while (true) {
                    const chunkIndex = nextChunkIndex++;
                    if (chunkIndex >= totalChunks) return;

                    const start = chunkIndex * CHUNK_SIZE;
                    const end = Math.min(start + CHUNK_SIZE, encryptedBlob.size);
                    const chunk = encryptedBlob.slice(start, end);

                    let lastError = null;

                    for (let attempt = 0; attempt < MAX_CHUNK_UPLOAD_RETRIES; attempt++) {
                        if (attempt > 0) {
                            await new Promise(r => setTimeout(r, 2000 * attempt));
                        }

                        try {
                            const formData = new FormData();
                            formData.append('session_id', sessionId);
                            formData.append('chunk_index', chunkIndex.toString());
                            formData.append('chunk', chunk);

                            const res = await fetch('/api/upload/chunk', {
                                method: 'POST',
                                headers: buildHeaders(),
                                body: formData
                            });

                            if (!res.ok) {
                                const error = await res.json();
                                throw new Error(error.error || `Chunk ${chunkIndex + 1} failed`);
                            }

                            lastError = null;
                            break;
                        } catch (error) {
                            lastError = error;
                        }
                    }

                    if (lastError) throw lastError;
                }
            };

            await Promise.all(Array.from({ length: concurrency }, () => worker()));

            await fetch('/api/upload/complete', {
                method: 'POST',
                headers: buildHeaders({ 'Content-Type': 'application/json' }),
                body: JSON.stringify({ session_id: sessionId, confirmed: true })
            });

            const deadline = Date.now() + 600000;
            while (Date.now() < deadline) {
                const statusRes = await fetch(`/api/upload/status/${sessionId}`, {
                    headers: buildHeaders()
                });
                if (!statusRes.ok) throw new Error('Assembly status check failed');

                const { status } = await statusRes.json();
                if (status === 'done') break;
                if (status.startsWith('error:')) throw new Error(status.slice(6));

                await new Promise(r => setTimeout(r, 1500));
            }

            const finalizeRes = await fetch('/api/upload/finalize', {
                method: 'POST',
                headers: buildHeaders({ 'Content-Type': 'application/json' }),
                body: JSON.stringify({
                    session_id: sessionId,
                    tunnel_id: activeTunnel.id,
                    ...envelopePayload
                })
            });

            if (!finalizeRes.ok) {
                const error = await finalizeRes.json();
                throw new Error(error.error || 'Finalize failed');
            }

            const finalizeData = await finalizeRes.json();
            if (finalizeData.file_id) {
                SecureCrypto.cacheFileKey(finalizeData.file_id, password);
            }

            dropMainText.textContent = 'Place files here';
            dropSubText.textContent = '';
            isUploading = false;
            await refreshTunnelState();
        } catch (error) {
            console.error('Tunnel file upload failed:', error);
            showErrorBanner('Upload failed: ' + error.message);
            dropMainText.textContent = 'Place files here';
            dropSubText.textContent = '';
            isUploading = false;
        }
    }

    function setupEventListeners() {
        createBtn?.addEventListener('click', handleCreateTunnel);

        joinBtn?.addEventListener('click', () => {
            setView('join');
            joinCodeInput = '';
            setCodeDisplay(joinCodeSquares, '');
            joinCodeSquares[0]?.focus();
        });

        startBtn?.addEventListener('click', handleStartTunnel);
        joinSubmitBtn?.addEventListener('click', handleJoinTunnel);
        leaveBtn?.addEventListener('click', handleLeaveTunnel);

        dropZone?.addEventListener('click', () => fileInput?.click());
        dropZone?.addEventListener('dragover', (e) => {
            e.preventDefault();
            e.stopPropagation();
            dropZone.classList.add('active');
        });
        dropZone?.addEventListener('dragleave', (e) => {
            e.preventDefault();
            e.stopPropagation();
            dropZone.classList.remove('active');
        });
        dropZone?.addEventListener('drop', (e) => {
            e.preventDefault();
            e.stopPropagation();
            dropZone.classList.remove('active');
            if (e.dataTransfer.files.length > 0) {
                processFileForTunnel(e.dataTransfer.files[0]);
            }
        });

        fileInput?.addEventListener('change', (e) => {
            if (e.target.files.length > 0) {
                processFileForTunnel(e.target.files[0]);
            }
        });

        joinCodeSquares.forEach((sq, index) => {
            sq.addEventListener('click', () => {
                joinCodeSquares.forEach(s => s.classList.remove('focused'));
                sq.classList.add('focused');
            });

            sq.addEventListener('keydown', (e) => {
                if (e.key >= '0' && e.key <= '9') {
                    e.preventDefault();
                    const chars = joinCodeInput.split('');
                    chars[index] = e.key;
                    joinCodeInput = chars.join('').slice(0, 4);
                    setCodeDisplay(joinCodeSquares, joinCodeInput);
                    if (index < 3) joinCodeSquares[index + 1].focus();
                } else if (e.key === 'Backspace') {
                    e.preventDefault();
                    const chars = joinCodeInput.split('');
                    if (chars[index]) {
                        chars[index] = '';
                    } else if (index > 0) {
                        chars[index - 1] = '';
                        joinCodeSquares[index - 1].focus();
                    }
                    joinCodeInput = chars.join('').slice(0, 4);
                    setCodeDisplay(joinCodeSquares, joinCodeInput);
                } else if (e.key === 'ArrowLeft' && index > 0) {
                    e.preventDefault();
                    joinCodeSquares[index - 1].focus();
                } else if (e.key === 'ArrowRight' && index < 3) {
                    e.preventDefault();
                    joinCodeSquares[index + 1].focus();
                }

                joinCodeInput = joinCodeInput.replace(/[^0-9]/g, '').slice(0, 4);
                joinSubmitBtn.disabled = joinCodeInput.length !== 4;
                joinSubmitBtn.classList.toggle('disabled', joinCodeInput.length !== 4);
            });
        });

    }

    function showNotification(message, type) {
        const pill = document.getElementById('notification-pill');
        const icon = document.getElementById('notification-icon');
        const text = document.getElementById('notification-text');
        if (!pill || !text) return;

        if (notificationTimer) {
            clearTimeout(notificationTimer);
            notificationTimer = null;
        }

        pill.classList.remove('visible');
        pill.classList.add('hidden');

        text.textContent = message;

        if (icon) {
            if (type === 'error') {
                icon.setAttribute('data-lucide', 'circle-x');
                icon.style.color = '#FF3B30';
            } else {
                icon.setAttribute('data-lucide', 'info');
                icon.style.color = '#000';
            }
            if (window.lucide && lucide.createIcons) {
                lucide.createIcons();
            }
        }

        pill.classList.remove('hidden');
        pill.offsetHeight;
        pill.classList.add('visible');

        notificationTimer = setTimeout(() => {
            pill.classList.remove('visible');
            setTimeout(() => pill.classList.add('hidden'), 350);
        }, 3500);
    }

    function showErrorBanner(message) {
        showNotification(message, 'error');
    }

    function hideErrorBanner() {}

    async function init() {
        setupTOSGate();

        try {
            await ensureEphemeralKeyPair();
        } catch (error) {
            console.error('Ephemeral keypair init failed:', error);
        }

        try {
            await SecureCrypto.loadWordList();
        } catch (error) {
            console.error('Word list failed:', error);
        }

        setupEventListeners();
        setView('initial');
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
