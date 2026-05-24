(function() {
    'use strict';

    const CHUNK_SIZE = 5 * 1024 * 1024;
    const AUTHENTICATED = window.CONFIG?.authenticated || false;
    const CNS_USER_ID = window.CONFIG?.cnsUserId || 0;
    const CNS_USERNAME = window.CONFIG?.cnsUsername || '';
    const TOS_VERSION = window.CONFIG?.tosVersion || '2026-04-05';
    const TOS_COOKIE_NAME = 'shareit_tos_accepted';
    const MAX_FILE_SIZE = AUTHENTICATED ? (1.5 * 1024 * 1024 * 1024) : 786432000;
    const RETENTION = AUTHENTICATED ? '90d' : '7d';
    const RETENTION_LABEL = AUTHENTICATED ? '90 Days' : '7 Days';
    const PARALLEL_CHUNK_UPLOADS = window.CONFIG?.parallelChunkUploads || 6;
    const MAX_CHUNK_UPLOAD_RETRIES = 5;

    let totalChunks = 0;
    let uploadedChunks = 0;
    let selectedFile = null;
    let encryptedBlob = null;
    let generatedPassword = null;
    let uploadSessionId = null;
    let pendingExpiresAt = null;
    let pendingCountdownTimer = null;
    let isUploading = false;
    let isFinalizing = false;
    let uploadComplete = false;
    let uploadError = null;
    let finalizeEnvelopePayload = null;
    let authDeviceIdentity = null;
    let authUserKeyRaw = null;
    let lastShareUrl = '';
    let idleCopyDone = false;
    let idleCopyBannerShown = false;

    const dropZone = document.getElementById('drop-zone');
    const fileInput = document.getElementById('file-input');
    const finalizeBtn = document.getElementById('finalize-btn');
    const stageEntry = document.getElementById('stage-entry');
    const stageProcessing = document.getElementById('stage-processing');
    const stagePending = document.getElementById('stage-pending');
    const stageOutput = document.getElementById('stage-output');
    const pendingCountdown = document.getElementById('pending-countdown');
    const progressVal = document.getElementById('progress-val');
    const processMain = document.getElementById('process-main');
    const processSub = document.getElementById('process-sub');
    const outExpiryLabel = document.getElementById('out-expiry-label');
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
        if (!tosOverlay) return true;
        if (hasAcceptedCurrentTOS()) { hideTOSGate(); return true; }
        showTOSGate();
        tosAcceptBtn?.addEventListener('click', () => { setCookie(TOS_COOKIE_NAME, TOS_VERSION, 31536000); hideTOSGate(); });
        tosDeclineBtn?.addEventListener('click', () => { window.location.href = 'https://cns-studios.com'; });
        return false;
    }

    function showTOSGate() {
        if (!tosOverlay) return;
        tosOverlay.classList.remove('hidden');
        tosOverlay.setAttribute('aria-hidden', 'false');
        document.body.classList.add('tos-gate-open');
    }

    function hideTOSGate() {
        if (!tosOverlay) return;
        tosOverlay.classList.add('hidden');
        tosOverlay.setAttribute('aria-hidden', 'true');
        document.body.classList.remove('tos-gate-open');
    }

    async function ensureDeviceReady() {
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
                showErrorBanner('Approve this device from a trusted device before uploading.');
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

    function setupEventListeners() {
        dropZone.addEventListener('click', handleZoneClick);
        dropZone.addEventListener('dragover', handleDragOver);
        dropZone.addEventListener('dragleave', handleDragLeave);
        dropZone.addEventListener('drop', handleDrop);
        fileInput.addEventListener('change', handleFileSelect);
        finalizeBtn.addEventListener('click', handleFinalize);
    }

    function handleZoneClick(e) {
        if (dropZone.classList.contains('success')) {
            if (lastShareUrl) {
                copyToClipboard(lastShareUrl, true);
                showShareBanner();
                const sub = dropZone.querySelector('p');
                if (sub) sub.textContent = 'Link copied to clipboard';
            }
            return;
        }
        if (dropZone.classList.contains('uploading')) return;
        fileInput.click();
    }

    function handleDragOver(e) { e.preventDefault(); e.stopPropagation(); e.dataTransfer.dropEffect = 'copy'; dropZone.classList.add('active'); }
    function handleDragLeave(e) { e.preventDefault(); e.stopPropagation(); if (e.target === dropZone) dropZone.classList.remove('active'); }
    function handleDrop(e) { e.preventDefault(); e.stopPropagation(); dropZone.classList.remove('active'); if (dropZone.classList.contains('uploading') || dropZone.classList.contains('success')) return; if (e.dataTransfer.files.length > 0) processFile(e.dataTransfer.files[0]); }
    function handleFileSelect(e) { if (dropZone.classList.contains('uploading') || dropZone.classList.contains('success')) return; if (e.target.files.length > 0) processFile(e.target.files[0]); }

    async function processFile(file) {
        if (isUploading || isFinalizing) return;
        if (file.size > MAX_FILE_SIZE) {
            showFileSizeWarning();
            return;
        }
        if (file.size === 0) { showErrorBanner('Cannot upload empty file.'); return; }

        selectedFile = file;

        const zoneHeading = dropZone.querySelector('h3');
        const zoneSubtext = dropZone.querySelector('p');
        dropZone.classList.add('uploading');
        zoneHeading.textContent = selectedFile.name;
        zoneSubtext.textContent = 'Processing...';

        runProtocolInBackground();
    }

    function showFileSizeWarning() {
        const sub = dropZone.querySelector('p');
        if (sub) {
            const original = sub.textContent;
            sub.textContent = `File too large. Maximum: ${SecureCrypto.formatFileSize(MAX_FILE_SIZE)}`;
            sub.style.color = '#ff4444';
            setTimeout(() => { sub.textContent = original; sub.style.color = ''; }, 3000);
        }
    }

    function handleFinalize() {
        if (isFinalizing) return;
        isFinalizing = true;
        updateFinalizeButtonState();
        stagePending.classList.add('hidden');
        stageProcessing.classList.remove('hidden');
        processMain.textContent = 'Uploading...';
        processSub.textContent = '0%';

        if (uploadComplete) { finalizeUpload(); }
        else if (uploadError) {
            isFinalizing = false; updateFinalizeButtonState();
            stageProcessing.classList.add('hidden'); stagePending.classList.remove('hidden');
            showErrorBanner('Upload failed: ' + uploadError);
        } else {
            const poll = setInterval(() => {
                if (uploadComplete) { clearInterval(poll); finalizeUpload(); }
                else if (uploadError) {
                    clearInterval(poll); isFinalizing = false; updateFinalizeButtonState();
                    stageProcessing.classList.add('hidden'); stagePending.classList.remove('hidden');
                    showErrorBanner('Upload failed: ' + uploadError);
                }
            }, 500);
        }
    }

    function updateFinalizeButtonState() { finalizeBtn.disabled = isFinalizing; }

    function updateUploadProgress() {
        if (totalChunks === 0) return;
        const pct = Math.floor((uploadedChunks / totalChunks) * 100);
        processMain.textContent = 'Uploading...';
        processSub.textContent = `${pct}%`;
        if (progressVal) progressVal.textContent = `${pct}%`;
        const zoneSubtext = dropZone.querySelector('p');
        if (zoneSubtext) zoneSubtext.textContent = `Uploading... ${pct}%`;
    }

    async function runProtocolInBackground() {
        isUploading = true;
        uploadComplete = false;
        uploadError = null;
        finalizeEnvelopePayload = null;

        const zoneSubtext = dropZone.querySelector('p');

        try {
            generatedPassword = await SecureCrypto.generatePassword();
            const dekBytes = new TextEncoder().encode(generatedPassword);

            if (AUTHENTICATED) {
                if (!authUserKeyRaw) await ensureDeviceReady();
                if (authUserKeyRaw) {
                    const wrapped = await SecureCrypto.wrapSecretWithUserKey(dekBytes, authUserKeyRaw);
                    finalizeEnvelopePayload = {
                        wrapped_dek_b64: SecureCrypto.toBase64(wrapped.wrapped),
                        dek_wrap_alg: 'AES-GCM-UK-v1',
                        dek_wrap_nonce_b64: SecureCrypto.toBase64(wrapped.nonce),
                        dek_wrap_version: 1
                    };
                }
            }

            zoneSubtext.textContent = 'Uploading...';
            encryptedBlob = await SecureCrypto.encryptFile(selectedFile, generatedPassword, () => {});
            await startUploadInBackground();
            uploadComplete = true;
            isUploading = false;
            updateFinalizeButtonState();

            isFinalizing = true;
            await finalizeUpload();
        } catch (error) {
            console.error('Upload pipeline failed:', error);
            uploadError = error.message;
            isUploading = false; uploadComplete = false; isFinalizing = false;
            updateFinalizeButtonState();
            const zoneHeading = dropZone.querySelector('h3');
            zoneHeading.textContent = 'Upload failed';
            zoneSubtext.textContent = error.message;
            showErrorBanner('Upload failed: ' + error.message);
        }
    }

    async function startUploadInBackground() {
        if (!encryptedBlob) return;
        const initResponse = await initUpload();
        uploadSessionId = initResponse.session_id;
        totalChunks = initResponse.total_chunks;
        uploadedChunks = 0;
        await uploadChunksInBackground(initResponse);
        const completeResponse = await completeUpload();
        await waitForAssembly(uploadSessionId);
        pendingExpiresAt = completeResponse.pending_expires_at ? new Date(completeResponse.pending_expires_at).getTime() : null;
        startPendingCountdown();
    }

    async function uploadChunksInBackground(initResponse) {
        uploadedChunks = 0;
        await uploadChunksParallel(initResponse, () => { uploadedChunks++; updateUploadProgress(); });
    }

    function getChunkBlob(chunkIndex) {
        const start = chunkIndex * CHUNK_SIZE;
        const end = Math.min(start + CHUNK_SIZE, encryptedBlob.size);
        return encryptedBlob.slice(start, end);
    }

    async function uploadChunkWithRetry(sessionId, chunkIndex) {
        const chunk = getChunkBlob(chunkIndex);
        let lastError;
        for (let attempt = 0; attempt < MAX_CHUNK_UPLOAD_RETRIES; attempt++) {
            if (attempt > 0) await new Promise(r => setTimeout(r, 2000 * attempt));
            try {
                const formData = new FormData();
                formData.append('session_id', sessionId);
                formData.append('chunk_index', chunkIndex.toString());
                formData.append('chunk', chunk);
                const response = await fetch('/api/upload/chunk', { method: 'POST', headers: { 'X-CSRF-Token': getCookieValue('csrf_token') }, body: formData });
                if (!response.ok) { const error = await response.json(); throw new Error(error.error || `Chunk ${chunkIndex + 1} failed`); }
                return;
            } catch (error) { lastError = error; }
        }
        throw lastError;
    }

    async function uploadChunksParallel(initResponse, onChunkUploaded) {
        const totalChunks = initResponse.total_chunks;
        const concurrency = Math.max(1, Math.min(PARALLEL_CHUNK_UPLOADS, totalChunks));
        let nextChunkIndex = 0;
        const worker = async () => {
            while (true) {
                const chunkIndex = nextChunkIndex++;
                if (chunkIndex >= totalChunks) return;
                await uploadChunkWithRetry(initResponse.session_id, chunkIndex);
                if (onChunkUploaded) onChunkUploaded(chunkIndex, totalChunks);
            }
        };
        await Promise.all(Array.from({ length: concurrency }, () => worker()));
    }

    async function initUpload() {
        const totalChunks = Math.ceil(encryptedBlob.size / CHUNK_SIZE);
        const response = await fetch('/api/upload/init', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCookieValue('csrf_token') },
            body: JSON.stringify({ file_name: selectedFile.name, file_size: encryptedBlob.size, total_chunks: totalChunks, chunk_size: CHUNK_SIZE })
        });
        if (!response.ok) { const error = await response.json(); throw new Error(error.error || 'Failed to initialize'); }
        return response.json();
    }

    async function completeUpload() {
        const response = await fetch('/api/upload/complete', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCookieValue('csrf_token') },
            body: JSON.stringify({ session_id: uploadSessionId, confirmed: true })
        });
        if (!response.ok) { const error = await response.json(); throw new Error(error.error || 'Failed to complete'); }
        return response.json();
    }

    async function waitForAssembly(sessionId, intervalMs = 1500, timeoutMs = 600000) {
        const deadline = Date.now() + timeoutMs;
        while (Date.now() < deadline) {
            const res = await fetch(`/api/upload/status/${sessionId}`);
            if (!res.ok) throw new Error('Assembly status check failed');
            const { status } = await res.json();
            if (status === 'done') return;
            if (status.startsWith('error:')) throw new Error(status.slice(6));
            await new Promise(r => setTimeout(r, intervalMs));
        }
        throw new Error('Assembly timed out');
    }

    async function finalizeUpload() {
        try {
            const finalizePayload = { session_id: uploadSessionId, ...(finalizeEnvelopePayload || {}), duration: RETENTION };
            const response = await fetch('/api/upload/finalize', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCookieValue('csrf_token') },
                body: JSON.stringify(finalizePayload)
            });
            if (!response.ok) { const error = await response.json(); throw new Error(error.error || 'Failed to finalize'); }
            const payload = await response.json();
            showSuccess(payload);
        } catch (error) {
            console.error('Finalize failed:', error);
            isFinalizing = false; updateFinalizeButtonState();
            dropZone.classList.remove('uploading');
            const zoneHeading = dropZone.querySelector('h3');
            const zoneSubtext = dropZone.querySelector('p');
            zoneHeading.textContent = 'Upload failed';
            zoneSubtext.textContent = error.message;
            showErrorBanner('Finalize failed: ' + error.message);
        }
    }

    function showSuccess(response) {
        clearPendingCountdown();
        isFinalizing = false;
        if (response.file_id && generatedPassword) SecureCrypto.cacheFileKey(response.file_id, generatedPassword);
        const fullShareUrl = `${response.share_url}#${generatedPassword}`;
        lastShareUrl = fullShareUrl;

        dropZone.classList.remove('uploading');
        dropZone.classList.add('success');
        const zoneIcon = dropZone.querySelector('.drop-zone-icon');
        const zoneHeading = dropZone.querySelector('h3');
        const zoneSubtext = dropZone.querySelector('p');
        zoneIcon.setAttribute('data-lucide', 'circle-check-big');
        zoneHeading.textContent = 'Uploaded';
        zoneSubtext.textContent = 'Link copied to clipboard';
        if (window.lucide && lucide.createIcons) lucide.createIcons();

        copyToClipboard(fullShareUrl, true);
        showShareBanner();

        setupIdleCopy(fullShareUrl);

        uploadSessionId = null;
        stageProcessing.classList.add('hidden');
        stagePending.classList.add('hidden');
        stageOutput.classList.add('hidden');
    }

    function setupIdleCopy(text) {
        idleCopyDone = false;
        idleCopyBannerShown = false;

        const onMove = () => {
            if (idleCopyDone) return;
            copyToClipboard(text, true).then(ok => {
                if (ok) {
                    idleCopyDone = true;
                    const zoneSubtext = dropZone.querySelector('p');
                    if (zoneSubtext) zoneSubtext.textContent = 'Link copied to clipboard';
                    showShareBanner();
                    setTimeout(() => {
                        idleCopyDone = false;
                        if (zoneSubtext) zoneSubtext.textContent = 'Link copied to clipboard';
                        document.addEventListener('mousemove', onMove, { once: true });
                        document.addEventListener('touchstart', onMove, { once: true });
                        document.addEventListener('keydown', onMove, { once: true });
                    }, 4000);
                }
            });
        };

        document.addEventListener('mousemove', onMove, { once: true });
        document.addEventListener('touchstart', onMove, { once: true });
        document.addEventListener('keydown', onMove, { once: true });
    }

    async function copyToClipboard(text, silent = false) {
        try {
            if (navigator.clipboard?.writeText) {
                await navigator.clipboard.writeText(text);
                if (!silent) showToast('Copied to clipboard!');
                return true;
            }
        } catch (error) {
        }

        const textarea = document.createElement('textarea');
        textarea.value = text;
        textarea.style.position = 'fixed';
        textarea.style.left = '0';
        textarea.style.top = '0';
        textarea.style.width = '1px';
        textarea.style.height = '1px';
        textarea.style.opacity = '0';
        textarea.setAttribute('readonly', '');
        document.body.appendChild(textarea);
        textarea.focus();
        textarea.select();
        let ok = false;
        try {
            ok = document.execCommand('copy');
        } catch (error) {
        }
        document.body.removeChild(textarea);
        if (ok) {
            if (!silent) showToast('Copied to clipboard!');
            return true;
        }
        return false;
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

    function showShareBanner() {
        showNotification('Link Copied!', 'info');
    }

    function showToast(message) {
        showNotification(message, 'info');
    }

    function resetUpload() {
        clearPendingCountdown();
        selectedFile = null; encryptedBlob = null; generatedPassword = null;
        const sessionToCancel = uploadSessionId;
        uploadSessionId = null; pendingExpiresAt = null; finalizeEnvelopePayload = null;
        isFinalizing = false; isUploading = false; uploadComplete = false; uploadError = null;
        idleCopyDone = false;
        if (sessionToCancel) fetch('/api/upload/cancel', { method: 'DELETE', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCookieValue('csrf_token') }, body: JSON.stringify({ session_id: sessionToCancel }) }).catch(() => {});
        fileInput.value = '';

        dropZone.classList.remove('uploading', 'success');
        const zoneIcon = dropZone.querySelector('.drop-zone-icon');
        const zoneHeading = dropZone.querySelector('h3');
        const zoneSubtext = dropZone.querySelector('p');
        zoneIcon.setAttribute('data-lucide', 'circle-fading-arrow-up');
        zoneHeading.textContent = 'Upload File';
        zoneSubtext.textContent = 'Drag and Drop a file here or click to browse.';
        if (window.lucide && lucide.createIcons) lucide.createIcons();

        stageEntry.classList.remove('hidden');
        stageProcessing.classList.add('hidden');
        stagePending.classList.add('hidden');
        stageOutput.classList.add('hidden');
        const msg = document.getElementById('idle-copy-msg');
        if (msg) msg.remove();
    }

    function startPendingCountdown() {
        clearPendingCountdown();
        if (!pendingExpiresAt) { pendingCountdown.textContent = '10:00'; return; }
        const tick = () => {
            const remaining = pendingExpiresAt - Date.now();
            if (remaining <= 0) { clearPendingCountdown(); pendingCountdown.textContent = '00:00'; resetUpload(); return; }
            const s = Math.floor(remaining / 1000);
            pendingCountdown.textContent = `${String(Math.floor(s / 60)).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`;
        };
        tick();
        pendingCountdownTimer = setInterval(tick, 1000);
    }

    function clearPendingCountdown() {
        if (pendingCountdownTimer) { clearInterval(pendingCountdownTimer); pendingCountdownTimer = null; }
    }

    function showErrorBanner(message) {
        showNotification(message, 'error');
    }

    function hideErrorBanner() {}

    async function init() {
        setupTOSGate();
        try { await SecureCrypto.loadWordList(); } catch (error) { console.error('Word list failed:', error); }
        setupEventListeners();
    }

    const style = document.createElement('style');
    style.textContent = '@keyframes fadeIn{from{opacity:0;transform:translateX(-50%) translateY(20px);}to{opacity:1;transform:translateX(-50%) translateY(0);}}@keyframes fadeOut{from{opacity:1;transform:translateX(-50%) translateY(0);}to{opacity:0;transform:translateX(-50%) translateY(20px);}}';
    document.head.appendChild(style);

    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
    else init();
})();
