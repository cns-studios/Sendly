(function() {
    'use strict';

    const t = (k, d) => window.CONFIG?.t?.[k] || d || k;
    const tpl = (k, vars) => { let s = t(k); if (vars) for (const [key, val] of Object.entries(vars)) s = s.replace(`{${key}}`, val); return s; };

    const CHUNK_SIZE = 5 * 1024 * 1024;
    const AUTHENTICATED = window.CONFIG?.authenticated || false;
    const CNS_USER_ID = window.CONFIG?.cnsUserId || 0;
    const CNS_USERNAME = window.CONFIG?.cnsUsername || '';
    const TOS_VERSION = window.CONFIG?.tosVersion || '2026-04-05';
    const TOS_COOKIE_NAME = 'sendly_tos_accepted';
    const MAX_FILE_SIZE = AUTHENTICATED ? (1.5 * 1024 * 1024 * 1024) : 786432000;
    const RETENTION = AUTHENTICATED ? '90d' : '7d';
    const RETENTION_LABEL = AUTHENTICATED ? '90 Days' : '7 Days';
    const PARALLEL_CHUNK_UPLOADS = window.CONFIG?.parallelChunkUploads || 6;
    const MAX_CHUNK_UPLOAD_RETRIES = 5;

    let totalChunks = 0;
    let uploadedChunks = 0;
    let selectedFile = null;
    let generatedPassword = null;
    let uploadSessionId = null;
    let pendingExpiresAt = null;
    let pendingCountdownTimer = null;
    let isUploading = false;
    let isFinalizing = false;
    let uploadComplete = false;
    let uploadError = null;
    let uploadStartedAt = 0;
    let finalizeEnvelopePayload = null;
    let authDeviceIdentity = null;
    let authUserKeyRaw = null;
    let lastShareUrl = '';
    let uploadedFileID = '';
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
    const uploadedFileMeta = document.getElementById('uploaded-file-meta');
    const uploadFileName = document.getElementById('upload-file-name');
    const uploadFileSize = document.getElementById('upload-file-size');
    const uploadFileType = document.getElementById('upload-file-type');
    const uploadExpiryPill = document.getElementById('upload-expiry-pill');
    const uploadSubhead = document.getElementById('upload-subhead');
    const shareRecipientInput = document.getElementById('share-recipient-input');
    const shareRecipientSend = document.getElementById('share-recipient-send');
    const shareRecipientStatus = document.getElementById('share-recipient-status');
    const shareSuggestList = document.getElementById('share-suggest-list');
    const shareSentChips = document.getElementById('share-sent-chips');
    const shareLinkInput = document.getElementById('share-link-input');
    const shareLinkCopy = document.getElementById('share-link-copy');
    const shareAnotherFile = document.getElementById('share-another-file');
    const shareUrlModal = document.getElementById('share-url-modal');
    const shareUrlText = document.getElementById('shareUrlText');
    const shareUrlCopyBtn = document.getElementById('shareUrlCopyBtn');
    const shareUrlDiscardBtn = document.getElementById('shareUrlDiscardBtn');
    let notificationTimer = null;
    const tosOverlay = document.getElementById('tos-overlay');
    const tosAcceptBtn = document.getElementById('tos-accept-btn');
    const tosDeclineBtn = document.getElementById('tos-decline-btn');
    let recipientLookupTimer = null;
    let recipientMatches = [];
    let shareInFlight = false;
    let selectedRecipient = null;

    function escapeHtml(value) {
        return String(value ?? '').replace(/[&<>"']/g, (char) => ({
            '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
        }[char]));
    }

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
            const result = await SecureCrypto.registerAuthenticatedDevice({
                userId: CNS_USER_ID,
                username: CNS_USERNAME,
                csrfToken: getCookieValue('csrf_token'),
                includeBootstrapEnvelope: true
            });
            authDeviceIdentity = result.identity;
            authUserKeyRaw = result.userKeyRaw;
            const payload = result.payload;
            if (payload.needs_enrollment) {
                showErrorBanner(t('toast_device_approve'));
                return false;
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
        shareRecipientInput?.addEventListener('input', handleRecipientInput);
        shareRecipientSend?.addEventListener('click', () => {
            if (selectedRecipient) shareFileWithUser(selectedRecipient);
        });
        shareLinkCopy?.addEventListener('click', copyUploadedLink);
        shareAnotherFile?.addEventListener('click', resetUpload);

        // Close suggestions on click outside
        document.addEventListener('click', (e) => {
            if (shareSuggestList && !e.target.closest('.user-input-wrap')) {
                shareSuggestList.classList.remove('open');
            }
        });

        shareUrlModal?.addEventListener('click', (e) => {
            if (e.target === shareUrlModal) hideShareUrlModal();
        });

        shareUrlCopyBtn?.addEventListener('click', async () => {
            const url = shareUrlText?.textContent;
            if (!url) return;
            const ok = await copyToClipboard(url, true);
            hideShareUrlModal();
            if (ok) showToast(t('toast_link_copied'));
        });

        shareUrlDiscardBtn?.addEventListener('click', hideShareUrlModal);
    }

    function setRecipientStatus(message, type = '') {
        if (!shareRecipientStatus) return;
        shareRecipientStatus.textContent = message;
        shareRecipientStatus.className = `share-recipient-status${type ? ` is-${type}` : ''}`;
    }

    // Render suggestion list from recent recipients + search results
    function renderSuggestions(query, recentUsers) {
        const q = query.trim().toLowerCase();
        if (!q) {
            shareSuggestList.classList.remove('open');
            shareSuggestList.innerHTML = '';
            return;
        }

        // First try recent users
        let matches = recentUsers.filter(u =>
            u.username.toLowerCase().includes(q)
        ).slice(0, 4);

        // If not enough, also include server lookup results
        if (matches.length < 4) {
            const serverMatches = recipientMatches.filter(u =>
                u.username.toLowerCase().includes(q)
            );
            for (const u of serverMatches) {
                if (!matches.find(m => m.user_id === u.user_id)) {
                    matches.push(u);
                }
                if (matches.length >= 4) break;
            }
        }

        if (matches.length === 0) {
            shareSuggestList.classList.remove('open');
            shareSuggestList.innerHTML = '';
            return;
        }

        shareSuggestList.innerHTML = matches.map(u => `
            <li class="suggest-item" role="option" data-user-id="${u.user_id}" data-username="${escapeHtml(u.username)}">
                <div class="suggest-avatar">${escapeHtml((u.username || '?').slice(0, 2).toUpperCase())}</div>
                <div>
                    <div class="suggest-name">${escapeHtml(u.username)}</div>
                    <div class="suggest-handle">@${escapeHtml(u.username)}</div>
                </div>
            </li>
        `).join('');
        shareSuggestList.classList.add('open');

        shareSuggestList.querySelectorAll('.suggest-item').forEach(item => {
            item.addEventListener('click', () => {
                const userId = item.dataset.userId;
                const username = item.dataset.username;
                selectRecipient({ user_id: userId, username: username });
            });
        });
    }

    function selectRecipient(u) {
        selectedRecipient = u;
        shareRecipientInput.value = u.username;
        shareSuggestList.classList.remove('open');
        shareRecipientSend.disabled = false;
        setRecipientStatus('');
    }

    async function handleRecipientInput() {
        const query = shareRecipientInput.value.trim();
        recipientMatches = [];
        selectedRecipient = null;
        shareRecipientSend.disabled = true;
        const inputWrap = shareRecipientInput?.closest('.user-input-wrap');
        inputWrap?.classList.remove('not-found');
        if (query.length < 3) {
            inputWrap?.classList.remove('searching');
            renderSuggestions(query, []);
            return;
        }
        // Show spinner
        inputWrap?.classList.add('searching');
        if (recipientLookupTimer) clearTimeout(recipientLookupTimer);
        recipientLookupTimer = setTimeout(async () => {
            try {
                const response = await fetch(`/api/users/lookup?q=${encodeURIComponent(query)}`, {
                    headers: { 'X-CSRF-Token': getCookieValue('csrf_token') }
                });
                const payload = await response.json().catch(() => ({}));
                if (!response.ok) throw new Error(payload.error || t('share_lookup_failed'));
                recipientMatches = payload.items || [];
                renderSuggestions(query, []);

                // An exact username match unlocks sending without requiring a
                // click on the suggestion list; a query with no matches at all
                // shows the not-found icon instead of inline status text.
                const exactMatch = recipientMatches.find(u => u.username.toLowerCase() === query.toLowerCase());
                if (exactMatch) {
                    selectRecipient(exactMatch);
                } else if (recipientMatches.length === 0) {
                    inputWrap?.classList.add('not-found');
                }
            } catch (error) {
                console.error('User lookup failed:', error);
            } finally {
                inputWrap?.classList.remove('searching');
            }
        }, 300);
    }

    async function shareFileWithUser(recipient) {
        if (shareInFlight || !recipient || !generatedPassword) return;
        shareInFlight = true;
        shareRecipientSend.disabled = true;
        shareRecipientSend.classList.add('is-loading');
        setRecipientStatus(t('share_sending'));
        try {
            let keyPayload;
            for (let attempt = 0; attempt < 2; attempt++) {
                const keyResponse = await fetch(`/api/users/${encodeURIComponent(recipient.user_id)}/identity-key`, {
                    headers: { 'X-CSRF-Token': getCookieValue('csrf_token') }
                });
                keyPayload = await keyResponse.json().catch(() => ({}));
                if (!keyResponse.ok) {
                    if (keyPayload.code === 'RECIPIENT_NOT_READY') throw new Error(t('share_recipient_not_ready'));
                    throw new Error(keyPayload.error || t('share_failed'));
                }
                const wrapped = await SecureCrypto.wrapFileDEKForIdentity(
                    new TextEncoder().encode(generatedPassword), keyPayload.public_key_jwk
                );
                const response = await fetch(`/api/file/${encodeURIComponent(uploadedFileID)}/share-to-user`, {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCookieValue('csrf_token') },
                    body: JSON.stringify({
                        recipient_user_id: Number(recipient.user_id),
                        wrapped_dek: SecureCrypto.toBase64(wrapped),
                        dek_wrap_alg: 'RSA-OAEP-2048-v1',
                        recipient_key_version: keyPayload.key_version
                    })
                });
                if (response.ok) {
                    setRecipientStatus(t('share_success'), 'valid');
                    // Add sent chip
                    addSentChip(recipient);
                    // Clear input
                    shareRecipientInput.value = '';
                    selectedRecipient = null;
                    shareRecipientSend.disabled = true;
                    shareRecipientSend.classList.remove('is-loading');
                    return;
                }
                const errorPayload = await response.json().catch(() => ({}));
                if (errorPayload.code === 'RECIPIENT_KEY_VERSION_MISMATCH' && attempt === 0) continue;
                if (errorPayload.code === 'RECIPIENT_NOT_READY') throw new Error(t('share_recipient_not_ready'));
                if (errorPayload.code === 'RECIPIENT_KEY_VERSION_MISMATCH') throw new Error(t('share_key_changed'));
                throw new Error(errorPayload.error || t('share_failed'));
            }
        } catch (error) {
            setRecipientStatus(error.message, 'error');
            showErrorBanner(tpl('toast_action_failed', {msg: error.message}));
        } finally {
            shareInFlight = false;
            shareRecipientSend.classList.remove('is-loading');
            shareRecipientSend.disabled = true;
        }
    }

    function addSentChip(recipient) {
        if (!shareSentChips) return;
        const chip = document.createElement('div');
        chip.className = 'sent-chip';
        chip.innerHTML = `
            <span class="chip-avatar">${escapeHtml((recipient.username || '?').slice(0, 2).toUpperCase())}</span>
            Sent to @${escapeHtml(recipient.username)}
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6L9 17l-5-5"/></svg>
        `;
        shareSentChips.appendChild(chip);
    }

    async function copyUploadedLink() {
        if (!shareLinkInput?.value) return;
        const ok = await copyToClipboard(shareLinkInput.value, true);
        if (ok) {
            shareLinkCopy.setAttribute('aria-label', t('share_copied'));
            shareLinkCopy.classList.add('copied');
            // Flash the link field
            shareLinkInput.classList.add('flash');
            setTimeout(() => {
                shareLinkCopy.setAttribute('aria-label', t('share_copy'));
                shareLinkCopy.classList.remove('copied');
                shareLinkInput.classList.remove('flash');
            }, 1800);
        }
    }

    // Load recent share recipients for the suggestion dropdown
    async function loadRecentShareRecipients() {
        if (!AUTHENTICATED) return;
        try {
            const response = await fetch('/api/me/recent-share-recipients', { headers: { 'X-CSRF-Token': getCookieValue('csrf_token') } });
            const payload = await response.json();
            return payload.items || [];
        } catch (error) {
            return [];
        }
    }

    function handleZoneClick(e) {
        if (dropZone.classList.contains('success')) {
            if (lastShareUrl) {
                showShareUrlModal(lastShareUrl);
            }
            return;
        }
        if (dropZone.classList.contains('uploading')) return;
        fileInput.click();
    }

    function handleDragOver(e) { e.preventDefault(); e.stopPropagation(); e.dataTransfer.dropEffect = 'copy'; dropZone.classList.add('active', 'dragging'); }
    function handleDragLeave(e) { e.preventDefault(); e.stopPropagation(); if (e.target === dropZone) dropZone.classList.remove('active', 'dragging'); }
    function handleDrop(e) { e.preventDefault(); e.stopPropagation(); dropZone.classList.remove('active', 'dragging'); if (dropZone.classList.contains('uploading') || dropZone.classList.contains('success')) return; if (e.dataTransfer.files.length > 0) processFile(e.dataTransfer.files[0]); }
    function handleFileSelect(e) { if (dropZone.classList.contains('uploading') || dropZone.classList.contains('success')) return; if (e.target.files.length > 0) processFile(e.target.files[0]); }

    async function processFile(file) {
        if (isUploading || isFinalizing) return;
        if (file.size > MAX_FILE_SIZE) {
            showFileSizeWarning();
            return;
        }
        if (file.size === 0) {
            setDropZoneState('error', t('link_cannot_upload_empty'));
            showErrorBanner(t('link_cannot_upload_empty'));
            return;
        }

        selectedFile = file;

        const zoneHeading = dropZone.querySelector('h3');
        const zoneSubtext = dropZone.querySelector('p');
        setDropZoneState('uploading', selectedFile.name);
        zoneHeading.textContent = selectedFile.name;
        zoneSubtext.textContent = t('status_processing');

        runProtocolInBackground();
    }

    function showFileSizeWarning() {
        const sub = dropZone.querySelector('p');
        if (!sub) return;
        setDropZoneState('warning');
        sub.textContent = tpl('link_file_too_large', {size: SecureCrypto.formatFileSize(MAX_FILE_SIZE)});
        setTimeout(() => { if (dropZone.classList.contains('warning')) resetDropZoneState(); }, 3000);
    }

    function setDropZoneState(state, filename = selectedFile?.name) {
        const icon = dropZone.querySelector('.drop-zone-icon');
        const heading = dropZone.querySelector('h3');
        const subtext = dropZone.querySelector('p');
        const badge = dropZone.querySelector('.drop-zone-badge');
        const progressFill = dropZone.querySelector('.drop-zone-progress-fill');
        const eta = dropZone.querySelector('#drop-zone-eta');
        dropZone.classList.remove('uploading', 'success', 'error', 'warning');
        if (state !== 'idle') dropZone.classList.add(state);
        if (progressFill && (state === 'idle' || state === 'uploading')) progressFill.style.width = '0';
        if (eta && state !== 'uploading') eta.textContent = '';

        if (state === 'uploading') {
            icon?.setAttribute('data-lucide', 'loader-2');
            heading.textContent = filename || '';
            subtext.textContent = t('status_uploading');
            if (eta) eta.textContent = t('upload_eta_calculating');
        } else if (state === 'success') {
            icon?.setAttribute('data-lucide', 'circle-check');
            heading.textContent = t('status_complete');
            subtext.textContent = filename || '';
            if (badge) badge.textContent = t('status_complete');
        } else if (state === 'error') {
            icon?.setAttribute('data-lucide', 'frown');
            heading.textContent = t('status_upload_failed');
            subtext.textContent = filename || '';
            if (badge) badge.textContent = t('status_upload_failed');
        } else if (state === 'warning') {
            icon?.setAttribute('data-lucide', 'triangle-alert');
            heading.textContent = tpl('link_file_too_large', {size: SecureCrypto.formatFileSize(MAX_FILE_SIZE)});
            subtext.textContent = filename || t('drop_subtext');
            if (badge) badge.textContent = tpl('link_file_too_large', {size: SecureCrypto.formatFileSize(MAX_FILE_SIZE)});
        } else {
            icon?.setAttribute('data-lucide', 'cloud-upload');
            heading.textContent = t('drop_heading');
            subtext.textContent = t('drop_subtext');
            if (badge) badge.textContent = '';
        }
        if (window.lucide?.createIcons) lucide.createIcons();
    }

    function resetDropZoneState() {
        setDropZoneState('idle');
    }

    function handleFinalize() {
        if (isFinalizing) return;
        isFinalizing = true;
        updateFinalizeButtonState();
        stagePending.classList.add('hidden');
        stageProcessing.classList.remove('hidden');
        processMain.textContent = t('status_uploading');
        processSub.textContent = t('app_0_pct');

        if (uploadComplete) { finalizeUpload(); }
        else if (uploadError) {
            isFinalizing = false; updateFinalizeButtonState();
            stageProcessing.classList.add('hidden'); stagePending.classList.remove('hidden');
            showErrorBanner(tpl('link_upload_failed', {msg: uploadError}));
        } else {
            const poll = setInterval(() => {
                if (uploadComplete) { clearInterval(poll); finalizeUpload(); }
                else if (uploadError) {
                    clearInterval(poll); isFinalizing = false; updateFinalizeButtonState();
                    stageProcessing.classList.add('hidden'); stagePending.classList.remove('hidden');
                    showErrorBanner(tpl('link_upload_failed', {msg: uploadError}));
                }
            }, 500);
        }
    }

    function updateFinalizeButtonState() { finalizeBtn.disabled = isFinalizing; }

    function updateUploadProgress() {
        if (totalChunks === 0) return;
        const pct = Math.floor((uploadedChunks / totalChunks) * 100);
        const progressFill = dropZone.querySelector('.drop-zone-progress-fill');
        const eta = dropZone.querySelector('#drop-zone-eta');
        if (progressFill) progressFill.style.width = `${pct}%`;
        if (eta && uploadStartedAt && uploadedChunks > 0 && pct < 100) {
            const elapsed = (Date.now() - uploadStartedAt) / 1000;
            const progress = uploadedChunks / totalChunks;
            const remaining = Math.ceil(elapsed * (1 - progress) / progress);
            eta.textContent = tpl('status_eta', {time: formatUploadDuration(remaining)});
        } else if (eta && pct >= 100) {
            eta.textContent = '';
        }

        processMain.textContent = t('status_uploading');
        processSub.textContent = `${pct}%`;
        if (progressVal) progressVal.textContent = `${pct}%`;
        const zoneSubtext = dropZone.querySelector('p');
        if (zoneSubtext) zoneSubtext.textContent = t('status_uploading');
    }

    function formatUploadDuration(seconds) {
        if (seconds < 60) return `${Math.max(1, seconds)}s`;
        return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
    }

    async function runProtocolInBackground() {
        isUploading = true;
        uploadStartedAt = Date.now();
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

            zoneSubtext.textContent = t('status_uploading');
            totalChunks = Math.ceil(selectedFile.size / CHUNK_SIZE);
            const initResponse = await initUpload(selectedFile.size, totalChunks);
            uploadSessionId = initResponse.session_id;
            uploadedChunks = 0;

            await SecureCrypto.encryptFileChunked(
                selectedFile,
                generatedPassword,
                CHUNK_SIZE,
                async (chunkIndex, chunkData) => {
                    await uploadOneChunk(uploadSessionId, chunkIndex, chunkData);
                    uploadedChunks++;
                    updateUploadProgress();
                },
                { concurrency: PARALLEL_CHUNK_UPLOADS }
            );

            const completeResponse = await completeUpload();
            await waitForAssembly(uploadSessionId);
            pendingExpiresAt = completeResponse.pending_expires_at
                ? new Date(completeResponse.pending_expires_at).getTime()
                : null;
            startPendingCountdown();

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
            setDropZoneState('error', error.message);
            showErrorBanner(tpl('link_upload_failed', {msg: error.message}));
        }
    }

    async function uploadOneChunk(sessionId, chunkIndex, chunkData) {
        let lastError;
        for (let attempt = 0; attempt < MAX_CHUNK_UPLOAD_RETRIES; attempt++) {
            if (attempt > 0) await new Promise(r => setTimeout(r, 2000 * attempt));
            try {
                const formData = new FormData();
                formData.append('session_id', sessionId);
                formData.append('chunk_index', chunkIndex.toString());
                formData.append('chunk', new Blob([chunkData]));
                const response = await fetch('/api/upload/chunk', { method: 'POST', headers: { 'X-CSRF-Token': getCookieValue('csrf_token') }, body: formData });
                if (!response.ok) { const error = await response.json(); throw new Error(error.error || `Chunk ${chunkIndex + 1} failed`); }
                return;
            } catch (error) { lastError = error; }
        }
        throw lastError;
    }

    async function initUpload(fileSize, totalChunks) {
        const response = await fetch('/api/upload/init', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCookieValue('csrf_token') },
            body: JSON.stringify({ file_name: selectedFile.name, file_size: fileSize, total_chunks: totalChunks, chunk_size: CHUNK_SIZE })
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
            setDropZoneState('error', error.message);
            showErrorBanner(tpl('toast_finalize_failed', {msg: error.message}));
        }
    }

    function showSuccess(response) {
        clearPendingCountdown();
        isFinalizing = false;
        if (response.file_id && generatedPassword) SecureCrypto.cacheFileKey(response.file_id, generatedPassword);
        const fullShareUrl = `${response.share_url}#${generatedPassword}`;
        lastShareUrl = fullShareUrl;
        uploadedFileID = response.file_id || '';

        // Populate file metadata
        if (uploadFileName) uploadFileName.textContent = selectedFile?.name || '';
        if (uploadFileSize) uploadFileSize.textContent = SecureCrypto.formatFileSize(selectedFile?.size || 0);
        if (uploadFileType) {
            const ext = selectedFile?.name?.split('.').pop()?.toUpperCase() || 'FILE';
            uploadFileType.textContent = ext + ' document';
        }
        if (uploadExpiryPill) uploadExpiryPill.textContent = tpl('link_expires_in', {retention: RETENTION_LABEL});
        if (uploadSubhead) uploadSubhead.textContent = t('link_upload_subhead');

        // Populate share link
        if (shareLinkInput) shareLinkInput.value = fullShareUrl;

        // Load recent recipients for suggestions (only for authenticated users)
        if (AUTHENTICATED) {
            loadRecentShareRecipients().catch(() => {});
        }

        uploadSessionId = null;
        stageEntry.classList.add('hidden');
        stageProcessing.classList.add('hidden');
        stagePending.classList.add('hidden');
        stageOutput.classList.remove('hidden');

        // Trigger animations on stage output elements
        requestAnimationFrame(() => {
            stageOutput.querySelectorAll('.card').forEach((card, i) => {
                card.style.animationDelay = `${0.30 + i * 0.08}s`;
            });
        });
    }

    function setupIdleCopy(text) {
        idleCopyDone = false;
        idleCopyBannerShown = false;

        const tryCopy = () => {
            if (idleCopyDone) return;
            copyToClipboard(text, true).then(ok => {
                if (ok) {
                    idleCopyDone = true;
                    const zoneSubtext = dropZone.querySelector('p');
                    if (zoneSubtext) zoneSubtext.textContent = t('toast_link_copied_idle');
                    showShareBanner();
                    setTimeout(() => {
                        idleCopyDone = false;
                        document.addEventListener('pointerup', tryCopy, { once: true });
                        document.addEventListener('keydown', tryCopy, { once: true });
                    }, 4000);
                } else {
                    document.addEventListener('pointerup', tryCopy, { once: true });
                    document.addEventListener('keydown', tryCopy, { once: true });
                }
            });
        };

        document.addEventListener('pointerup', tryCopy, { once: true });
        document.addEventListener('keydown', tryCopy, { once: true });
    }

    async function copyToClipboard(text, silent = false) {
        try {
            if (navigator.clipboard?.writeText) {
                await navigator.clipboard.writeText(text);
                if (!silent) showToast(t('toast_copied'));
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
            if (!silent) showToast(t('toast_copied'));
            return true;
        }
        return false;
    }

    function showShareUrlModal(url) {
        if (!shareUrlModal || !shareUrlText) return;
        shareUrlText.textContent = url;
        shareUrlCopyBtn.textContent = t('link_share_copy');
        shareUrlCopyBtn.classList.remove('copied');
        shareUrlModal.classList.remove('hidden');
        shareUrlModal.offsetHeight;
        shareUrlModal.classList.add('visible');
        shareUrlModal.setAttribute('aria-hidden', 'false');
        document.body.classList.add('tos-gate-open');
        shareUrlCopyBtn.focus();
    }

    function hideShareUrlModal() {
        if (!shareUrlModal) return;
        shareUrlModal.classList.remove('visible');
        setTimeout(() => shareUrlModal.classList.add('hidden'), 250);
        shareUrlModal.setAttribute('aria-hidden', 'true');
        document.body.classList.remove('tos-gate-open');
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
        showNotification(t('toast_link_copied_notification'), 'info');
    }

    function showToast(message) {
        showNotification(message, 'info');
    }

    function resetUpload() {
        clearPendingCountdown();
        selectedFile = null; generatedPassword = null; uploadedFileID = '';
        const sessionToCancel = uploadSessionId;
        uploadSessionId = null; pendingExpiresAt = null; finalizeEnvelopePayload = null;
        isFinalizing = false; isUploading = false; uploadComplete = false; uploadError = null;
        idleCopyDone = false;
        if (sessionToCancel) fetch('/api/upload/cancel', { method: 'DELETE', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCookieValue('csrf_token') }, body: JSON.stringify({ session_id: sessionToCancel }) }).catch(() => {});
        fileInput.value = '';

        resetDropZoneState();

        stageEntry.classList.remove('hidden');
        stageProcessing.classList.add('hidden');
        stagePending.classList.add('hidden');
        stageOutput.classList.add('hidden');
        const msg = document.getElementById('idle-copy-msg');
        if (msg) msg.remove();
    }

    function startPendingCountdown() {
        clearPendingCountdown();
        if (!pendingExpiresAt) { pendingCountdown.textContent = t('state_upload_pending'); return; }
        const tick = () => {
            const remaining = pendingExpiresAt - Date.now();
            if (remaining <= 0) { clearPendingCountdown(); pendingCountdown.textContent = t('state_upload_expired'); resetUpload(); return; }
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
