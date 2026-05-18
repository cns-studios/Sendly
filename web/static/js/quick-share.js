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
    const errorBanner = document.getElementById('error-banner');
    const errorBannerText = document.getElementById('error-banner-text');
    const errorBannerClose = document.getElementById('error-banner-close');
    const tosOverlay = document.getElementById('tos-overlay');
    const tosAcceptBtn = document.getElementById('tos-accept-btn');
    const tosDeclineBtn = document.getElementById('tos-decline-btn');

    let joinCodeInput = '';
    let guestDeviceId = '';

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
        if (hasAcceptedCurrentTOS()) { tosOverlay.classList.add('hidden'); return; }
        tosOverlay.classList.remove('hidden');
        tosAcceptBtn?.addEventListener('click', () => { setCookie(TOS_COOKIE_NAME, TOS_VERSION, 31536000); tosOverlay.classList.add('hidden'); });
        tosDeclineBtn?.addEventListener('click', () => { window.location.href = 'https://cns-studios.com'; });
    }

    function getOrCreateGuestDeviceId() {
        if (guestDeviceId) return guestDeviceId;
        guestDeviceId = localStorage.getItem('shareit_guest_device_id');
        if (!guestDeviceId) {
            guestDeviceId = crypto.randomUUID();
            localStorage.setItem('shareit_guest_device_id', guestDeviceId);
        }
        return guestDeviceId;
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
                headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCookieValue('csrf_token') },
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
                authUserKeyRaw = await SecureCrypto.unwrapUserKeyForDevice(wrappedUK, authDeviceIdentity.privateKeyJWK);
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
        if (startBtn) startBtn.style.display = (view === 'create' && isHost && !hasStarted) ? '' : 'none';
    }

    function setCodeDisplay(squares, code) {
        squares.forEach((sq, i) => {
            sq.textContent = code[i] || '\u2013';
            sq.classList.toggle('filled', !!code[i]);
        });
    }

    function renderParticipants(items) {
        const container = isHost ? peopleRow : queuePeopleRow;
        if (!container) return;
        container.innerHTML = '';
        const otherPeers = items.filter(p => {
            if (AUTHENTICATED) return p.cns_user_id && p.cns_user_id !== CNS_USER_ID;
            return p.device_id && p.device_id !== getOrCreateGuestDeviceId();
        });
        if (otherPeers.length === 0) {
            const empty = document.createElement('div');
            empty.className = 'recent-state';
            empty.textContent = 'Noone joined yet';
            empty.style.color = '#888';
            empty.style.fontSize = '0.85rem';
            empty.style.textAlign = 'center';
            empty.style.padding = '1rem 0';
            container.appendChild(empty);
        } else {
            otherPeers.forEach(p => {
                const person = document.createElement('div');
                person.className = 'person';
                person.innerHTML = `
                    <div class="person-circle">
                        <svg xmlns="http://www.w3.org/2000/svg" width="30" height="30" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                            <path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>
                        </svg>
                    </div>
                    <span class="person-name">${p.cns_user_id ? `User ${p.cns_user_id}` : 'Guest'}</span>
                `;
                container.appendChild(person);
            });
        }
        if (connectedText) {
            const total = 1 + otherPeers.length;
            connectedText.textContent = `${total} connected`;
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
                    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="file-download-btn" title="Download"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
                </div>
            `;
            el.querySelector('.file-download-btn').addEventListener('click', () => downloadTunnelFile(item.file_id, item.filename));
            fileList.appendChild(el);
        });
    }

    function escapeHtml(str) {
        const div = document.createElement('div');
        div.textContent = str;
        return div.innerHTML;
    }

    async function downloadTunnelFile(fileId, fileName) {
        try {
            let password = SecureCrypto.getCachedFileKey(fileId);

            if (!password && AUTHENTICATED) {
                const accessRes = await fetch(`/api/tunnels/${activeTunnel.id}/files/${fileId}/access`);
                if (!accessRes.ok) throw new Error('Failed to access file');
                const accessData = await accessRes.json();
                const envelope = accessData.file_key_envelope;
                if (envelope?.wrapped_dek_b64 && authUserKeyRaw) {
                    const wrappedDEK = SecureCrypto.fromBase64(envelope.wrapped_dek_b64);
                    const nonce = envelope.dek_wrap_nonce_b64 ? SecureCrypto.fromBase64(envelope.dek_wrap_nonce_b64) : null;
                    const rawDEK = await SecureCrypto.unwrapSecretWithUserKey(wrappedDEK, nonce, authUserKeyRaw);
                    password = new TextDecoder().decode(rawDEK);
                    SecureCrypto.cacheFileKey(fileId, password);
                }
            }

            if (!password) {
                const storedPw = localStorage.getItem(SESSION_PASSWORD_PREFIX + activeTunnel.id);
                if (storedPw) password = storedPw;
            }

            if (!password) {
                showErrorBanner('No decryption key for this file.');
                return;
            }

            const chunkRes = await fetch(`/api/file/${fileId}/download`);
            if (!chunkRes.ok) throw new Error('Download failed');
            const blob = await chunkRes.blob();

            const decrypted = await SecureCrypto.decryptBlob(blob, password);
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

    async function handleCreateTunnel() {
        if (AUTHENTICATED) {
            const ready = await ensureDeviceReady();
            if (!ready) return;
        }

        const deviceId = AUTHENTICATED ? authDeviceIdentity.deviceId : getOrCreateGuestDeviceId();

        try {
            const response = await fetch('/api/me/tunnels/start', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCookieValue('csrf_token') },
                body: JSON.stringify({ duration: '24h', device_id: deviceId })
            });
            if (!response.ok) { const error = await response.json(); throw new Error(error.error || 'Failed to create tunnel'); }
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
        }

        const deviceId = AUTHENTICATED ? authDeviceIdentity.deviceId : getOrCreateGuestDeviceId();

        try {
            const response = await fetch('/api/me/tunnels/join', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCookieValue('csrf_token') },
                body: JSON.stringify({ code: joinCodeInput, device_id: deviceId })
            });
            if (!response.ok) { const error = await response.json(); throw new Error(error.error || 'Failed to join tunnel'); }
            const payload = await response.json();
            activeTunnel = payload.tunnel;
            participants = payload.participants || [];
            sessionPassword = null;
            isHost = false;
            hasStarted = activeTunnel.status === 'active';
            setCodeDisplay(queueCodeSquares, activeTunnel.code);
            renderParticipants(participants);
            if (!hasStarted) {
                setView('queue');
            } else {
                setView('session');
            }
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
        const deviceId = AUTHENTICATED ? authDeviceIdentity.deviceId : getOrCreateGuestDeviceId();

        try {
            const response = await fetch(`/api/me/tunnels/${activeTunnel.id}/confirm`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCookieValue('csrf_token') },
                body: JSON.stringify({ device_id: deviceId })
            });
            if (!response.ok) { const error = await response.json(); throw new Error(error.error || 'Failed to start'); }
            const payload = await response.json();
            if (payload.tunnel) activeTunnel = payload.tunnel;
            hasStarted = true;
            setView('session');
            if (!sessionPassword) {
                sessionPassword = await SecureCrypto.generatePassword();
                localStorage.setItem(SESSION_PASSWORD_PREFIX + activeTunnel.id, sessionPassword);
            }
            await refreshTunnelState();
        } catch (error) {
            console.error('Start tunnel failed:', error);
            showErrorBanner('Failed to start session: ' + error.message);
        }
    }

    async function handleLeaveTunnel() {
        if (!activeTunnel?.id) return;
        const deviceId = AUTHENTICATED ? authDeviceIdentity.deviceId : getOrCreateGuestDeviceId();
        try {
            const response = await fetch(`/api/me/tunnels/${activeTunnel.id}`, {
                method: 'DELETE',
                headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCookieValue('csrf_token') },
                body: JSON.stringify({ device_id: deviceId })
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
            const response = await fetch(`/api/me/tunnels/${activeTunnel.id}`, {
                headers: { 'X-CSRF-Token': getCookieValue('csrf_token') }
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
        if (tunnelPollTimer) { clearInterval(tunnelPollTimer); tunnelPollTimer = null; }
    }

    function clearTunnelState() {
        stopTunnelPolling();
        activeTunnel = null;
        sessionPassword = null;
        participants = [];
        isHost = false;
        hasStarted = false;
        if (fileList) fileList.innerHTML = '';
        if (fileListEmpty) fileListEmpty.classList.remove('hidden');
        if (fileList) fileList.classList.add('hidden');
    }

    async function processFileForTunnel(file) {
        if (isUploading || !activeTunnel?.id) return;
        if (file.size > TUNNEL_MAX_FILE_SIZE) { showErrorBanner(`File too large. Maximum: ${SecureCrypto.formatFileSize(TUNNEL_MAX_FILE_SIZE)}`); return; }
        if (file.size === 0) { showErrorBanner('Cannot upload empty file.'); return; }

        isUploading = true;
        dropMainText.textContent = 'Uploading...';
        dropSubText.textContent = file.name;

        try {
            let password = sessionPassword;
            if (!password) {
                password = await SecureCrypto.generatePassword();
                sessionPassword = password;
                localStorage.setItem(SESSION_PASSWORD_PREFIX + activeTunnel.id, password);
            }

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

            const encryptedBlob = await SecureCrypto.encryptFile(file, password, () => {});
            const totalChunks = Math.ceil(encryptedBlob.size / CHUNK_SIZE);

            const initRes = await fetch('/api/upload/init', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCookieValue('csrf_token') },
                body: JSON.stringify({ file_name: file.name, file_size: encryptedBlob.size, total_chunks: totalChunks, chunk_size: CHUNK_SIZE, tunnel_id: activeTunnel.id })
            });
            if (!initRes.ok) { const error = await initRes.json(); throw new Error(error.error || 'Init failed'); }
            const initData = await initRes.json();
            const sessionId = initData.session_id;

            let uploadedChunks = 0;
            const concurrency = Math.max(1, Math.min(PARALLEL_CHUNK_UPLOADS, totalChunks));
            let nextChunkIndex = 0;
            const worker = async () => {
                while (true) {
                    const chunkIndex = nextChunkIndex++;
                    if (chunkIndex >= totalChunks) return;
                    const start = chunkIndex * CHUNK_SIZE;
                    const end = Math.min(start + CHUNK_SIZE, encryptedBlob.size);
                    const chunk = encryptedBlob.slice(start, end);
                    let lastError;
                    for (let attempt = 0; attempt < MAX_CHUNK_UPLOAD_RETRIES; attempt++) {
                        if (attempt > 0) await new Promise(r => setTimeout(r, 2000 * attempt));
                        try {
                            const formData = new FormData();
                            formData.append('session_id', sessionId);
                            formData.append('chunk_index', chunkIndex.toString());
                            formData.append('chunk', chunk);
                            const res = await fetch('/api/upload/chunk', { method: 'POST', headers: { 'X-CSRF-Token': getCookieValue('csrf_token') }, body: formData });
                            if (!res.ok) { const error = await res.json(); throw new Error(error.error || `Chunk ${chunkIndex + 1} failed`); }
                            break;
                        } catch (error) { lastError = error; }
                    }
                    if (lastError) throw lastError;
                    uploadedChunks++;
                }
            };
            await Promise.all(Array.from({ length: concurrency }, () => worker()));

            await fetch('/api/upload/complete', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCookieValue('csrf_token') },
                body: JSON.stringify({ session_id: sessionId, confirmed: true })
            });

            const deadline = Date.now() + 600000;
            while (Date.now() < deadline) {
                const statusRes = await fetch(`/api/upload/status/${sessionId}`);
                if (!statusRes.ok) throw new Error('Assembly status check failed');
                const { status } = await statusRes.json();
                if (status === 'done') break;
                if (status.startsWith('error:')) throw new Error(status.slice(6));
                await new Promise(r => setTimeout(r, 1500));
            }

            const finalizePayload = {
                session_id: sessionId,
                tunnel_id: activeTunnel.id,
                ...envelopePayload
            };
            const finalizeRes = await fetch('/api/upload/finalize', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCookieValue('csrf_token') },
                body: JSON.stringify(finalizePayload)
            });
            if (!finalizeRes.ok) { const error = await finalizeRes.json(); throw new Error(error.error || 'Finalize failed'); }
            const finalizeData = await finalizeRes.json();
            if (finalizeData.file_id) SecureCrypto.cacheFileKey(finalizeData.file_id, password);

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
        createBtn.addEventListener('click', handleCreateTunnel);
        joinBtn.addEventListener('click', () => { setView('join'); joinCodeInput = ''; setCodeDisplay(joinCodeSquares, ''); joinCodeSquares[0]?.focus(); });
        startBtn.addEventListener('click', handleStartTunnel);
        joinSubmitBtn.addEventListener('click', handleJoinTunnel);
        leaveBtn.addEventListener('click', handleLeaveTunnel);

        dropZone.addEventListener('click', () => fileInput.click());
        dropZone.addEventListener('dragover', (e) => { e.preventDefault(); e.stopPropagation(); dropZone.classList.add('active'); });
        dropZone.addEventListener('dragleave', (e) => { e.preventDefault(); e.stopPropagation(); dropZone.classList.remove('active'); });
        dropZone.addEventListener('drop', (e) => {
            e.preventDefault(); e.stopPropagation(); dropZone.classList.remove('active');
            if (e.dataTransfer.files.length > 0) processFileForTunnel(e.dataTransfer.files[0]);
        });
        fileInput.addEventListener('change', (e) => { if (e.target.files.length > 0) processFileForTunnel(e.target.files[0]); });

        joinCodeSquares.forEach((sq, index) => {
            sq.addEventListener('click', () => { joinCodeSquares.forEach(s => s.classList.remove('focused')); sq.classList.add('focused'); });
            sq.addEventListener('keydown', (e) => {
                if (e.key >= '0' && e.key <= '9') {
                    e.preventDefault();
                    joinCodeInput = joinCodeInput.substring(0, index) + e.key + joinCodeInput.substring(index + 1);
                    setCodeDisplay(joinCodeSquares, joinCodeInput);
                    if (index < 3) joinCodeSquares[index + 1].focus();
                    joinCodeInput = joinCodeInput.substring(0, 4);
                    joinSubmitBtn.disabled = joinCodeInput.length !== 4;
                    joinSubmitBtn.classList.toggle('disabled', joinCodeInput.length !== 4);
                } else if (e.key === 'Backspace') {
                    e.preventDefault();
                    if (joinCodeInput[index]) {
                        joinCodeInput = joinCodeInput.substring(0, index) + joinCodeInput.substring(index + 1);
                    } else if (index > 0) {
                        joinCodeInput = joinCodeInput.substring(0, index - 1) + joinCodeInput.substring(index);
                        joinCodeSquares[index - 1].focus();
                    }
                    setCodeDisplay(joinCodeSquares, joinCodeInput);
                    joinSubmitBtn.disabled = joinCodeInput.length !== 4;
                    joinSubmitBtn.classList.toggle('disabled', joinCodeInput.length !== 4);
                } else if (e.key === 'ArrowLeft' && index > 0) {
                    e.preventDefault(); joinCodeSquares[index - 1].focus();
                } else if (e.key === 'ArrowRight' && index < 3) {
                    e.preventDefault(); joinCodeSquares[index + 1].focus();
                }
            });
            sq.addEventListener('input', (e) => {
                const val = e.target.textContent.replace(/[^a-zA-Z0-9]/g, '').toUpperCase();
                if (val) {
                    joinCodeInput = joinCodeInput.substring(0, index) + val + joinCodeInput.substring(index + 1);
                    joinCodeInput = joinCodeInput.substring(0, 4);
                    setCodeDisplay(joinCodeSquares, joinCodeInput);
                    if (index < 3) joinCodeSquares[index + 1].focus();
                    joinSubmitBtn.disabled = joinCodeInput.length !== 4;
                    joinSubmitBtn.classList.toggle('disabled', joinCodeInput.length !== 4);
                }
            });
        });

        if (errorBannerClose) errorBannerClose.addEventListener('click', hideErrorBanner);
    }

    let errorBannerHideTimer = null;
    let errorBannerCloseTimer = null;

    function showErrorBanner(message) {
        if (!errorBanner) return;
        if (errorBannerText) errorBannerText.textContent = message;
        if (errorBannerHideTimer) clearTimeout(errorBannerHideTimer);
        if (errorBannerCloseTimer) clearTimeout(errorBannerCloseTimer);
        errorBanner.classList.remove('hidden');
        requestAnimationFrame(() => errorBanner.classList.add('visible'));
        errorBannerHideTimer = setTimeout(() => hideErrorBanner(), 4500);
    }

    function hideErrorBanner() {
        if (!errorBanner) return;
        if (errorBannerHideTimer) clearTimeout(errorBannerHideTimer);
        if (errorBannerCloseTimer) clearTimeout(errorBannerCloseTimer);
        if (errorBanner.classList.contains('hidden')) return;
        errorBanner.classList.remove('visible');
        errorBannerCloseTimer = setTimeout(() => { if (!errorBanner.classList.contains('visible')) errorBanner.classList.add('hidden'); }, 320);
    }

    async function init() {
        setupTOSGate();
        try { await SecureCrypto.loadWordList(); } catch (error) { console.error('Word list failed:', error); }
        setupEventListeners();
        setView('initial');
    }

    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
    else init();
})();
