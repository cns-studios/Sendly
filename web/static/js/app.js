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
    const RECENT_UPLOADS_PER_PAGE = 10;
    const RECENT_SEARCH_DEBOUNCE_MS = 180;

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
    let pendingAutoCopyText = null;
    let pendingAutoCopyBanner = false;
    let pendingAutoCopyBound = false;
    let notificationTimer = null;
    let finalizeEnvelopePayload = null;
    let ephemeralKeyPair = null;

    let authDeviceIdentity = null;
    let authUserKeyRaw = null;
    let recentSearchQuery = '';
    let recentCurrentPage = 1;
    let recentTotalPages = 0;
    let recentSearchOpen = false;
    let recentSearchDebounceTimer = null;
    let activeTunnel = null;
    let tunnelPollTimer = null;
    let lastShareUrl = '';
    let idleCopyDone = false;
    let idleCopyBannerShown = false;

    const stageEntry = document.getElementById('stage-entry');
    const stageProcessing = document.getElementById('stage-processing');
    const stagePending = document.getElementById('stage-pending');
    const stageOutput = document.getElementById('stage-output');
    const pendingCountdown = document.getElementById('pending-countdown');
    
    const progressVal = document.getElementById('progress-val');
    const processMain = document.getElementById('process-main');
    const processSub = document.getElementById('process-sub');

    const dropZone = document.getElementById('drop-zone');
    const fileInput = document.getElementById('file-input');
    const finalizeBtn = document.getElementById('finalize-btn');
    const fileDetails = document.getElementById('file-details');
    const statusText = document.getElementById('status-text');

    const outUrl = document.getElementById('out-url');
    const outPin = document.getElementById('out-pin');
    const outKey = document.getElementById('out-key');
    const outExpiryLabel = document.getElementById('out-expiry-label');
    const recentSection = document.getElementById('recent-uploads-section');
    const recentLoading = document.getElementById('recent-loading');
    const recentError = document.getElementById('recent-error');
    const recentEmpty = document.getElementById('recent-empty');
    const recentList = document.getElementById('recent-list');
    const recentCount = document.getElementById('recent-count');
    const recentSearchToggle = document.getElementById('recent-search-toggle');
    const recentSearchWrap = document.getElementById('recent-search-wrap');
    const recentSearchInput = document.getElementById('recent-search-input');
    const recentRecoverDevice = document.getElementById('recent-recover-device');
    const recentPagination = document.getElementById('recent-pagination');
    const recentPrev = document.getElementById('recent-prev');
    const recentNext = document.getElementById('recent-next');
    const recentPageLabel = document.getElementById('recent-page-label');
    const tunnelFilesSection = document.getElementById('tunnel-files-section');
    const tunnelList = document.getElementById('tunnel-list');
    const tunnelEmpty = document.getElementById('tunnel-empty');
    const tunnelCount = document.getElementById('tunnel-count');
    const tunnelControlsSection = document.getElementById('tunnel-controls-section');
    const tunnelDurationSelect = document.getElementById('tunnel-duration-select');
    const tunnelStartBtn = document.getElementById('tunnel-start-btn');
    const tunnelJoinCode = document.getElementById('tunnel-join-code');
    const tunnelJoinBtn = document.getElementById('tunnel-join-btn');
    const tunnelConfirmWrap = document.getElementById('tunnel-confirm-wrap');
    const tunnelConfirmBtn = document.getElementById('tunnel-confirm-btn');
    const tunnelActiveMeta = document.getElementById('tunnel-active-meta');
    const tunnelQRWrap = document.getElementById('tunnel-qr-wrap');
    const tunnelQRCode = document.getElementById('tunnel-qr-code');
    const tunnelEndBtn = document.getElementById('tunnel-end-btn');
    const deviceApprovalModal = document.getElementById('device-approval-modal');
    const deviceApprovalTitle = document.getElementById('device-approval-title');
    const deviceApprovalMessage = document.getElementById('device-approval-message');
    const deviceApprovalMeta = document.getElementById('device-approval-meta');
    const deviceApprovalCount = document.getElementById('device-approval-count');
    const deviceApprovalWaiting = document.getElementById('device-approval-waiting');
    const deviceApprovalDecline = document.getElementById('device-approval-decline');
    const deviceApprovalApprove = document.getElementById('device-approval-approve');
    const deviceApprovalRecover = document.getElementById('device-approval-recover');
    const tosOverlay = document.getElementById('tos-overlay');
    const tosAcceptBtn = document.getElementById('tos-accept-btn');
    const tosDeclineBtn = document.getElementById('tos-decline-btn');
    const downloadActivityOverlay = document.getElementById('download-activity-overlay');
    const actionModal = document.getElementById('action-modal');
    const actionModalTitle = document.getElementById('action-modal-title');
    const actionModalDescription = document.getElementById('action-modal-description');
    const actionModalCancel = document.getElementById('action-modal-cancel');
    const actionModalConfirm = document.getElementById('action-modal-confirm');

    let pendingEnrollmentItems = [];
    let activePendingEnrollment = null;
    let pendingEnrollmentBusy = false;
    let pendingEnrollmentMode = 'idle';
    let pendingEnrollmentSocket = null;
    let pendingEnrollmentSocketRetryTimer = null;
    let pendingEnrollmentRefreshTimer = null;
    let pendingEnrollmentSocketEverOpened = false;
    let isDeviceUntrusted = false;
    const recentFileStates = new Map();
    const activeDownloads = new Set();
    const LOCKED_FILE_INFO = t('device_connect_locked_info');
    let actionModalResolver = null;

    function getCookieValue(name) {
        const value = `; ${document.cookie}`;
        const parts = value.split(`; ${name}=`);
        if (parts.length === 2) {
            return parts.pop().split(';').shift();
        }
        return '';
    }

    function setCookie(name, value, maxAgeSeconds) {
        document.cookie = `${name}=${encodeURIComponent(value)}; path=/; max-age=${maxAgeSeconds}; SameSite=Lax`;
    }

    function hasAcceptedCurrentTOS() {
        return getCookieValue(TOS_COOKIE_NAME) === TOS_VERSION;
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

    function setupTOSGate() {
        if (!tosOverlay) return true;

        if (hasAcceptedCurrentTOS()) {
            hideTOSGate();
            return true;
        }

        showTOSGate();

        tosAcceptBtn?.addEventListener('click', () => {
            setCookie(TOS_COOKIE_NAME, TOS_VERSION, 31536000);
            hideTOSGate();
        });

        tosDeclineBtn?.addEventListener('click', () => {
            window.location.href = 'https://cns-studios.com';
        });

        return false;
    }

    async function refreshSessionIfNeeded() {
        const expiresAtRaw = getCookieValue('auth_expires_at');
        const refreshToken = getCookieValue('refresh_token');

        if (!refreshToken) return;

        const expiresAt = expiresAtRaw ? parseInt(expiresAtRaw, 10) : 0;
        const needsRefresh = !getCookieValue('auth_token') || (expiresAt > 0 && Date.now() / 1000 >= expiresAt - 300);

        if (!needsRefresh) return;

        try {
            await fetch('/auth/refresh', { method: 'POST' });
        } catch (e) {
        }
    }

    async function init() {
        setupTOSGate();

        if (AUTHENTICATED || getCookieValue('refresh_token')) {
            await refreshSessionIfNeeded();
        }

         
        try {
            await SecureCrypto.loadWordList();
        } catch (error) {
            console.error('Failed to preload word list:', error);
        }

        applyTierUI();
        setupEventListeners();

        if (AUTHENTICATED) {
            connectPendingEnrollmentSocket();
            startPendingEnrollmentRefreshTimer();
        }

        if (AUTHENTICATED) {
            ensureDeviceReady().catch(() => {});
            loadRecentUploads().catch(() => {});
            loadPendingEnrollments().catch(() => {});

            refreshRecentFilesCache();
            if (recentFilesCacheTimer) clearInterval(recentFilesCacheTimer);
            recentFilesCacheTimer = setInterval(refreshRecentFilesCache, 60000);
        }
    }

    function applyTierUI() {
        if (!AUTHENTICATED) {
            const nudge = document.getElementById('auth-nudge');
            if (nudge && !localStorage.getItem('sendly_auth_nudge_dismissed')) {
                nudge.classList.remove('hidden');
            }
            const closeBtn = document.getElementById('auth-nudge-close');
            if (closeBtn) {
                closeBtn.addEventListener('click', () => {
                    nudge.classList.add('hidden');
                    localStorage.setItem('sendly_auth_nudge_dismissed', '1');
                });
            }
        }
    }

    async function ensureDeviceReady() {
        try {
        const payload = await registerCurrentDevice(true);
        if (payload?.needs_enrollment) {
            isDeviceUntrusted = true;
            setRecoveryActionVisible(true);

            const currentDeviceId = authDeviceIdentity?.deviceId;
            const existingEnrollment = currentDeviceId
                ? pendingEnrollmentItems.find((item) => {
                    const requestDeviceId = item?.request_device?.id || item?.enrollment?.request_device_id || '';
                    return requestDeviceId === currentDeviceId;
                })
                : null;
            if (existingEnrollment?.enrollment?.id) {
                showWaitingEnrollment(existingEnrollment, pendingEnrollmentItems.length);
                return false;
            }

            const enrollment = await requestDeviceEnrollment(authDeviceIdentity.deviceId);
                if (enrollment?.enrollment_id) {
                    showWaitingEnrollment({
                        enrollment: {
                            id: enrollment.enrollment_id,
                            cns_user_id: CNS_USER_ID,
                            request_device_id: authDeviceIdentity.deviceId,
                            verification_code: enrollment.verification_code,
                            status: 'pending',
                            expires_at: enrollment.expires_at,
                            created_at: new Date().toISOString()
                        },
                        request_device: {
                            id: authDeviceIdentity.deviceId,
                            device_label: `${CNS_USERNAME || t('user_default')} device`,
                            public_key_jwk: authDeviceIdentity.publicKeyJWK,
                            key_algorithm: authDeviceIdentity.keyAlgorithm,
                            key_version: authDeviceIdentity.keyVersion
                        }
                    }, 1);
                    return false;
                }

                showRecoveryBanner(t('toast_approval_connect'));
                return false;
            }

            isDeviceUntrusted = false;
            setRecoveryActionVisible(false);
            return true;
        } catch (error) {
            console.error('Failed to initialize authenticated device state:', error);
            showErrorBanner(t('toast_approval_key_setup_failed'));
            return false;
        }
    }

    async function registerCurrentDevice(allowEnrollmentRequest = true, endpoint = '/api/me/devices/register') {
        authDeviceIdentity = await SecureCrypto.getOrCreateDeviceIdentity();
        authUserKeyRaw = SecureCrypto.getUserKeyRaw(CNS_USER_ID);

        let bootstrapUserKeyRaw = null;
        let wrappedUserKeyB64 = '';
        let ukWrapAlg = '';
        let ukWrapMeta = {};

        if (allowEnrollmentRequest) {
            if (!authUserKeyRaw) {
                bootstrapUserKeyRaw = SecureCrypto.generateUserKeyRaw();
                authUserKeyRaw = bootstrapUserKeyRaw;
            }

            const wrappedUserKey = await SecureCrypto.wrapUserKeyForDevice(authUserKeyRaw, authDeviceIdentity.publicKeyJWK);
            wrappedUserKeyB64 = SecureCrypto.toBase64(wrappedUserKey);
            ukWrapAlg = 'RSA-OAEP-2048-v1';
            ukWrapMeta = { type: 'self-wrap', device_id: authDeviceIdentity.deviceId };
        }

        const response = await fetch(endpoint, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'X-CSRF-Token': getCookieValue('csrf_token')
            },
            body: JSON.stringify({
                device_id: authDeviceIdentity.deviceId,
                device_label: `${CNS_USERNAME || t('user_default')} device`,
                public_key_jwk: authDeviceIdentity.publicKeyJWK,
                key_algorithm: authDeviceIdentity.keyAlgorithm,
                key_version: authDeviceIdentity.keyVersion,
                wrapped_user_key_b64: wrappedUserKeyB64,
                uk_wrap_alg: ukWrapAlg,
                uk_wrap_meta: ukWrapMeta
            })
        });

        if (!response.ok) {
            const errorPayload = await response.json().catch(() => ({}));
            throw new Error(errorPayload.error || 'Device registration failed');
        }

        const payload = await response.json().catch(() => ({}));
        isDeviceUntrusted = !!payload.needs_enrollment;
        setRecoveryActionVisible(isDeviceUntrusted);

        if (!payload.needs_enrollment) {
            if (!authUserKeyRaw && payload.user_key_envelope?.wrapped_uk_b64) {
                const wrappedUK = SecureCrypto.fromBase64(payload.user_key_envelope.wrapped_uk_b64);
                authUserKeyRaw = await SecureCrypto.unwrapUserKeyForDevice(wrappedUK, authDeviceIdentity.privateKeyJWK);
            }

            if (!authUserKeyRaw && bootstrapUserKeyRaw) {
                authUserKeyRaw = bootstrapUserKeyRaw;
            }

            if (authUserKeyRaw) {
                SecureCrypto.saveUserKeyRaw(CNS_USER_ID, authUserKeyRaw);
            }
        }

        return payload;
    }

    async function requestDeviceEnrollment(deviceId) {
        try {
            const response = await fetch('/api/me/devices/enrollments', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': getCookieValue('csrf_token')
                },
                body: JSON.stringify({ request_device_id: deviceId })
            });

            if (response.ok) {
                return response.json().catch(() => ({}));
            }

            const errorPayload = await response.json().catch(() => ({}));
            if (errorPayload.code === 'ENROLLMENT_CREATE_FAILED') {
                return null;
            }
            throw new Error(errorPayload.error || 'Failed to request device approval');
        } catch (error) {
            console.error('Failed to request enrollment:', error);
            return null;
        }
    }

    async function loadRecentUploads(page = 1) {
        if (!recentSection || !AUTHENTICATED) return;
        recentSection.classList.remove('hidden');
        setRecentState('loading');

        try {
            const params = new URLSearchParams({
                page: String(page),
                per_page: String(RECENT_UPLOADS_PER_PAGE),
            });
            if (recentSearchQuery) {
                params.set('q', recentSearchQuery);
            }

            const response = await fetch(`/api/me/recent-uploads?${params.toString()}`, {
                headers: { 'X-CSRF-Token': getCookieValue('csrf_token') }
            });
            if (!response.ok) {
                throw new Error('Failed to load recent uploads');
            }
            const payload = await response.json();
            renderRecentUploads(payload);
            prefetchRecentLockStates(payload?.items || []).catch(() => {});
        } catch (error) {
            console.error(error);
            setRecentState('error');
        }
    }

    async function loadPendingEnrollments() {
        if (!AUTHENTICATED || !deviceApprovalModal) return;

        try {
            const response = await fetch('/api/me/devices/enrollments/pending', {
                headers: { 'X-CSRF-Token': getCookieValue('csrf_token') }
            });

            if (!response.ok) {
                throw new Error('Failed to load pending device approvals');
            }

            const payload = await response.json();
            pendingEnrollmentItems = Array.isArray(payload.items) ? payload.items : [];

            const currentDeviceId = authDeviceIdentity?.deviceId || '';
            const currentDeviceEnrollment = currentDeviceId
                ? pendingEnrollmentItems.find((item) => {
                    const requestDeviceId = item?.request_device?.id || item?.enrollment?.request_device_id || '';
                    return requestDeviceId === currentDeviceId;
                })
                : null;

            if ((pendingEnrollmentMode === 'waiting' || isDeviceUntrusted) && currentDeviceEnrollment?.enrollment?.id) {
                showWaitingEnrollment(currentDeviceEnrollment, pendingEnrollmentItems.length);
                return;
            }

            if (pendingEnrollmentMode === 'waiting' && activePendingEnrollment?.enrollment?.id) {
                const stillPending = pendingEnrollmentItems.some((item) => item?.enrollment?.id === activePendingEnrollment.enrollment.id);
                if (stillPending) {
                    showWaitingEnrollment(activePendingEnrollment, pendingEnrollmentItems.length);
                    return;
                }

                await finalizeWaitingEnrollment();
                return;
            }

            if (isDeviceUntrusted) {
                hidePendingEnrollmentModal();
                return;
            }

            const approvalItems = currentDeviceId
                ? pendingEnrollmentItems.filter((item) => {
                    const requestDeviceId = item?.request_device?.id || item?.enrollment?.request_device_id || '';
                    return requestDeviceId !== currentDeviceId;
                })
                : pendingEnrollmentItems;

            if (approvalItems.length > 0) {
                showApprovalEnrollment(approvalItems[0], approvalItems.length);
            } else {
                hidePendingEnrollmentModal();
            }
        } catch (error) {
            console.error('Failed to load pending enrollments:', error);
            pendingEnrollmentItems = [];
            if (pendingEnrollmentMode !== 'waiting') {
                hidePendingEnrollmentModal();
            }
        }
    }

    function showApprovalEnrollment(item, count) {
        pendingEnrollmentMode = 'approval';
        activePendingEnrollment = item;
        if (!deviceApprovalTitle || !deviceApprovalMessage || !deviceApprovalMeta || !deviceApprovalModal) {
            return;
        }

        const device = item?.request_device || {};
        const enrollment = item?.enrollment || {};
        const deviceName = device.device_label || device.id || 'Unknown device';
        const deviceId = device.id || enrollment.request_device_id || 'unknown';
        const keyAlgorithm = device.key_algorithm || 'unknown';
        const requestedAt = enrollment.created_at ? formatUploadDate(enrollment.created_at) : 'just now';

        deviceApprovalTitle.textContent = t('device_connect_title');
        deviceApprovalMessage.textContent = t('device_connect_message');
        deviceApprovalMeta.innerHTML = [
            `<span>${t('device_connect_device')}${escapeHtml(deviceName)}</span>`,
            `<span>${t('device_connect_device_id')}${escapeHtml(deviceId)}</span>`,
            `<span>${t('device_connect_key')}${escapeHtml(keyAlgorithm)}</span>`,
            `<span>${t('device_connect_requested')}${escapeHtml(requestedAt)}</span>`
        ].join('');

        if (deviceApprovalCount) {
            deviceApprovalCount.textContent = count > 1 ? `${count}${t('device_connect_count')}` : t('device_connect_one');
        }

        if (deviceApprovalWaiting) {
            deviceApprovalWaiting.classList.add('hidden');
        }
        if (deviceApprovalApprove) {
            deviceApprovalApprove.classList.remove('hidden');
            deviceApprovalApprove.disabled = false;
        }
        if (deviceApprovalDecline) {
            deviceApprovalDecline.classList.remove('hidden');
            deviceApprovalDecline.disabled = false;
        }
        deviceApprovalRecover?.classList.add('hidden');

        deviceApprovalModal.classList.remove('hidden');
        deviceApprovalModal.setAttribute('aria-hidden', 'false');
    }

    function showWaitingEnrollment(item, count = 1) {
        pendingEnrollmentMode = 'waiting';
        activePendingEnrollment = item;
        if (!deviceApprovalTitle || !deviceApprovalMessage || !deviceApprovalMeta || !deviceApprovalModal) {
            return;
        }

        const device = item?.request_device || {};
        const enrollment = item?.enrollment || {};
        const deviceName = device.device_label || device.id || 'This device';
        const deviceId = device.id || enrollment.request_device_id || 'unknown';
        const requestedAt = enrollment.created_at ? formatUploadDate(enrollment.created_at) : 'just now';

        deviceApprovalTitle.textContent = t('device_pending_title');
        deviceApprovalMessage.textContent = t('device_pending_message');
        deviceApprovalMeta.innerHTML = [
            `<span>${t('device_connect_device')}${escapeHtml(deviceName)}</span>`,
            `<span>${t('device_connect_device_id')}${escapeHtml(deviceId)}</span>`,
            `<span>${t('device_connect_requested')}${escapeHtml(requestedAt)}</span>`
        ].join('');

        if (deviceApprovalCount) {
            deviceApprovalCount.textContent = count > 1 ? `${count}${t('device_connect_count')}` : t('device_pending_count');
        }

        deviceApprovalWaiting?.classList.remove('hidden');
        deviceApprovalApprove?.classList.add('hidden');
        deviceApprovalDecline?.classList.add('hidden');
        deviceApprovalRecover?.classList.remove('hidden');

        deviceApprovalModal.classList.remove('hidden');
        deviceApprovalModal.setAttribute('aria-hidden', 'false');
    }

    function hidePendingEnrollmentModal() {
        pendingEnrollmentMode = 'idle';
        activePendingEnrollment = null;
        if (!deviceApprovalModal) return;
        deviceApprovalModal.classList.add('hidden');
        deviceApprovalModal.setAttribute('aria-hidden', 'true');
        deviceApprovalWaiting?.classList.add('hidden');
        deviceApprovalApprove?.classList.remove('hidden');
        deviceApprovalDecline?.classList.remove('hidden');
        deviceApprovalRecover?.classList.add('hidden');
        if (deviceApprovalApprove) deviceApprovalApprove.disabled = false;
        if (deviceApprovalDecline) deviceApprovalDecline.disabled = false;
    }

    async function handleApprovePendingEnrollment() {
        if (!activePendingEnrollment || pendingEnrollmentBusy) return;

        pendingEnrollmentBusy = true;
        deviceApprovalModal?.classList.add('is-busy');
        if (deviceApprovalApprove) deviceApprovalApprove.disabled = true;
        if (deviceApprovalDecline) deviceApprovalDecline.disabled = true;

        try {
            if (!authDeviceIdentity) {
                await ensureDeviceReady();
            }
            if (!authUserKeyRaw) {
                authUserKeyRaw = SecureCrypto.getUserKeyRaw(CNS_USER_ID);
            }
            if (!authUserKeyRaw) {
                throw new Error('Trusted user key is not available on this device');
            }

            const requestDevice = activePendingEnrollment.request_device || {};
            const requestPublicKey = requestDevice.public_key_jwk;
            if (!requestPublicKey) {
                throw new Error('Request device public key is missing');
            }

            const wrappedUserKey = await SecureCrypto.wrapUserKeyForDevice(authUserKeyRaw, requestPublicKey);
            const response = await fetch(`/api/me/devices/enrollments/${encodeURIComponent(activePendingEnrollment.enrollment.id)}/approve`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': getCookieValue('csrf_token')
                },
                body: JSON.stringify({
                    approver_device_id: authDeviceIdentity.deviceId,
                    verification_code: activePendingEnrollment.enrollment.verification_code,
                    wrapped_user_key_b64: SecureCrypto.toBase64(wrappedUserKey),
                    uk_wrap_alg: 'RSA-OAEP-2048-v1',
                    uk_wrap_meta: {
                        type: 'enrollment-approval',
                        approver_device_id: authDeviceIdentity.deviceId,
                        request_device_id: requestDevice.id || activePendingEnrollment.enrollment.request_device_id
                    }
                })
            });

            if (!response.ok) {
                const errorPayload = await response.json().catch(() => ({}));
                throw new Error(errorPayload.error || 'Failed to approve device');
            }

            isDeviceUntrusted = false;
            setRecoveryActionVisible(false);
            await loadPendingEnrollments();
            await loadRecentUploads();
        } catch (error) {
            console.error('Approve enrollment failed:', error);
            showErrorBanner(tpl('toast_approval_failed', {msg: error.message}));
        } finally {
            pendingEnrollmentBusy = false;
            if (deviceApprovalApprove) deviceApprovalApprove.disabled = false;
            if (deviceApprovalDecline) deviceApprovalDecline.disabled = false;
            deviceApprovalModal?.classList.remove('is-busy');
        }
    }

    async function handleDeclinePendingEnrollment() {
        if (!activePendingEnrollment || pendingEnrollmentBusy) return;

        pendingEnrollmentBusy = true;
        deviceApprovalModal?.classList.add('is-busy');
        if (deviceApprovalApprove) deviceApprovalApprove.disabled = true;
        if (deviceApprovalDecline) deviceApprovalDecline.disabled = true;

        try {
            if (!authDeviceIdentity) {
                await ensureDeviceReady();
            }

            const response = await fetch(`/api/me/devices/enrollments/${encodeURIComponent(activePendingEnrollment.enrollment.id)}/reject`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': getCookieValue('csrf_token')
                },
                body: JSON.stringify({
                    approver_device_id: authDeviceIdentity.deviceId
                })
            });

            if (!response.ok) {
                const errorPayload = await response.json().catch(() => ({}));
                throw new Error(errorPayload.error || 'Failed to decline device');
            }

            await loadPendingEnrollments();
        } catch (error) {
            console.error('Reject enrollment failed:', error);
            showErrorBanner(tpl('toast_decline_failed', {msg: error.message}));
        } finally {
            pendingEnrollmentBusy = false;
            if (deviceApprovalApprove) deviceApprovalApprove.disabled = false;
            if (deviceApprovalDecline) deviceApprovalDecline.disabled = false;
            deviceApprovalModal?.classList.remove('is-busy');
        }
    }

    async function handleRecoverLostDevice() {
        if (pendingEnrollmentBusy) return;

        const confirmed = await openActionModal({
            title: t('toast_device_recover_title'),
            description: t('toast_device_recover_desc'),
            confirmText: t('toast_device_recover_confirm'),
            cancelText: t('toast_device_recover_cancel'),
            kicker: t('toast_device_recover_kicker')
        });
        if (!confirmed) {
            return;
        }

        pendingEnrollmentBusy = true;
        deviceApprovalModal?.classList.add('is-busy');
        if (deviceApprovalApprove) deviceApprovalApprove.disabled = true;
        if (deviceApprovalDecline) deviceApprovalDecline.disabled = true;
        if (deviceApprovalRecover) deviceApprovalRecover.disabled = true;

        try {
            const payload = await registerCurrentDevice(true, '/api/me/devices/recover');
            if (!payload?.device_id) {
                throw new Error('Recovery failed');
            }

            pendingEnrollmentItems = [];
            activePendingEnrollment = null;
            pendingEnrollmentMode = 'idle';
            isDeviceUntrusted = false;
            setRecoveryActionVisible(false);
            hidePendingEnrollmentModal();
            showInfoBanner(t('toast_device_recovered'));
            await loadRecentUploads();
        } catch (error) {
            console.error('Lost-device recovery failed:', error);
            showErrorBanner(tpl('toast_recovery_failed', {msg: error.message}));
        } finally {
            pendingEnrollmentBusy = false;
            if (deviceApprovalApprove) deviceApprovalApprove.disabled = false;
            if (deviceApprovalDecline) deviceApprovalDecline.disabled = false;
            if (deviceApprovalRecover) deviceApprovalRecover.disabled = false;
            deviceApprovalModal?.classList.remove('is-busy');
        }
    }

    async function finalizeWaitingEnrollment() {
        if (pendingEnrollmentMode !== 'waiting') {
            return;
        }

        try {
            const payload = await registerCurrentDevice(false);
            if (payload.needs_enrollment) {
                isDeviceUntrusted = true;
                setRecoveryActionVisible(true);
                hidePendingEnrollmentModal();
                showErrorBanner(t('toast_approval_expired'));
                return;
            }

            isDeviceUntrusted = false;
            setRecoveryActionVisible(false);
            hidePendingEnrollmentModal();
            await loadRecentUploads();
        } catch (error) {
            console.error('Failed to finalize pending enrollment:', error);
            showErrorBanner(t('toast_approval_setup_failed'));
        }
    }

    function connectPendingEnrollmentSocket() {
        if (!AUTHENTICATED) return;
        if (pendingEnrollmentSocket && (pendingEnrollmentSocket.readyState === WebSocket.OPEN || pendingEnrollmentSocket.readyState === WebSocket.CONNECTING)) {
            return;
        }

        const scheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const socketUrl = `${scheme}//${window.location.host}/api/me/devices/ws`;

        try {
            pendingEnrollmentSocket = new WebSocket(socketUrl);
        } catch (error) {
            schedulePendingEnrollmentSocketReconnect();
            return;
        }

        pendingEnrollmentSocket.onopen = () => {
            pendingEnrollmentSocketEverOpened = true;
        };
        pendingEnrollmentSocket.onmessage = handlePendingEnrollmentSocketMessage;
        pendingEnrollmentSocket.onclose = () => {
            pendingEnrollmentSocket = null;
            
            
            if (!pendingEnrollmentSocketEverOpened) {
                return;
            }
            schedulePendingEnrollmentSocketReconnect();
        };
        pendingEnrollmentSocket.onerror = () => {
            try {
                pendingEnrollmentSocket?.close();
            } catch (_) {
                
            }
        };
    }

    function schedulePendingEnrollmentSocketReconnect() {
        if (!AUTHENTICATED) return;
        if (pendingEnrollmentSocketRetryTimer) {
            clearTimeout(pendingEnrollmentSocketRetryTimer);
        }

        pendingEnrollmentSocketRetryTimer = setTimeout(() => {
            pendingEnrollmentSocketRetryTimer = null;
            connectPendingEnrollmentSocket();
        }, 5000);
    }

    function handlePendingEnrollmentSocketMessage(event) {
        let payload = null;
        try {
            payload = JSON.parse(event.data);
        } catch (error) {
            return;
        }

        const eventType = payload?.type || '';
        if (eventType === 'device_enrollment_created') {
            loadPendingEnrollments();
            return;
        }

        const requestDeviceId = payload?.request_device?.id || payload?.enrollment?.request_device_id;
        const approverDeviceId = payload?.approver_device_id || '';
        const currentDeviceId = authDeviceIdentity?.deviceId;
        const isCurrentDevice = currentDeviceId && requestDeviceId && requestDeviceId === currentDeviceId;
        const isApproverDevice = currentDeviceId && approverDeviceId && currentDeviceId === approverDeviceId;

        if (eventType === 'device_enrollment_approved' && isCurrentDevice) {
            finalizeWaitingEnrollment();
            return;
        }

        if (eventType === 'device_enrollment_approved' && !isCurrentDevice && !isApproverDevice) {
            const approvedName = payload?.request_device?.device_label || payload?.enrollment?.request_device_id || 'requested device';
            showInfoBanner(tpl('toast_approval_approved', {msg: approvedName}));
        }

        if (eventType === 'device_enrollment_rejected' && isCurrentDevice) {
            hidePendingEnrollmentModal();
            showErrorBanner(t('toast_approval_declined'));
            return;
        }

        loadPendingEnrollments();
    }

    function startPendingEnrollmentRefreshTimer() {
        if (!AUTHENTICATED || pendingEnrollmentRefreshTimer) {
            return;
        }

        pendingEnrollmentRefreshTimer = setInterval(() => {
            loadPendingEnrollments().catch(() => {});
        }, 6000);
    }

    function setRecentState(state) {
        if (!recentLoading || !recentError || !recentEmpty || !recentList) return;
        recentLoading.classList.toggle('hidden', state !== 'loading');
        recentError.classList.toggle('hidden', state !== 'error');
        recentEmpty.classList.toggle('hidden', state !== 'empty');
        recentList.classList.toggle('hidden', state !== 'ready');
        if (recentPagination && state !== 'ready') {
            recentPagination.classList.add('hidden');
        }
    }

    function renderRecentUploads(payload) {
        if (!recentList) return;
        const items = payload?.items || [];
        recentCurrentPage = payload?.page || 1;
        recentTotalPages = payload?.total_pages || 0;
        const totalItems = payload?.total || 0;

        if (!items.length) {
            setRecentState('empty');
            if (recentEmpty) {
                recentEmpty.textContent = recentSearchQuery
                    ? t('state_no_search_results')
                    : t('state_no_uploads');
            }
            updateRecentPagination();
            return;
        }

        setRecentState('ready');

        recentList.innerHTML = items.map((item, idx) => {
            const locked = recentFileStates.get(item.file_id)?.locked;
            const opacity = Math.max(0.05, 1.0 - idx * 0.19);
            const expiresLabel = formatExpiryDate(item.expires_at);
            return `
                <div class="file-entry${locked ? ' is-locked' : ''}" style="opacity: ${opacity};" data-file-id="${item.file_id}" data-file-name="${escapeHtml(item.filename)}" data-share-url="${item.share_url}" data-expires-at="${item.expires_at}"${locked ? ` title="${LOCKED_FILE_INFO}"` : ''}>
                    <div class="file-entry-left">
                        <span class="file-name" title="${escapeHtml(item.filename)}">${escapeHtml(item.filename)}</span>
                        <span class="file-info">${locked ? t('label_file_info_locked') + expiresLabel : SecureCrypto.formatFileSize(item.size_bytes) + t('label_expires_sep') + expiresLabel}</span>
                    </div>
                    <div class="file-entry-right">
                        <button class="recent-action" data-action="copy" aria-label="${t('label_copy_share')}" title="${t('label_copy_share')}" ${locked ? 'disabled' : ''}>
                            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                                <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
                                <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
                            </svg>
                        </button>
                        <button class="recent-action" data-action="download" aria-label="${t('label_download_file')}" title="${t('label_download_file')}" ${locked ? 'disabled' : ''}>
                            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                                <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
                                <polyline points="7 10 12 15 17 10"/>
                                <line x1="12" y1="15" x2="12" y2="3"/>
                            </svg>
                        </button>
                    </div>
                </div>
            `;
        }).join('');

        recentList.querySelectorAll('.recent-action').forEach((btn) => {
            btn.addEventListener('click', handleRecentAction);
        });
        updateRecentPagination();
    }

    function showTunnelInfo(state) {
        if (!state) return;
        showInfoBanner(state);
    }

    function setTunnelEntryControlsHidden(hidden) {
        tunnelDurationSelect?.closest('.tunnel-control-row')?.classList.toggle('hidden', hidden);
        tunnelStartBtn?.closest('.tunnel-control-row')?.classList.toggle('hidden', hidden);
        tunnelJoinCode?.closest('.tunnel-control-row')?.classList.toggle('hidden', hidden);
    }

    function clearTunnelState() {
        activeTunnel = null;
        if (tunnelPollTimer) {
            clearInterval(tunnelPollTimer);
            tunnelPollTimer = null;
        }

        tunnelEndBtn?.classList.add('hidden');
        tunnelFilesSection?.classList.add('hidden');
        tunnelConfirmWrap?.classList.add('hidden');
        tunnelQRWrap?.classList.add('hidden');
        if (tunnelList) {
            tunnelList.innerHTML = '';
            tunnelList.classList.add('hidden');
        }
        tunnelEmpty?.classList.add('hidden');
        if (tunnelCount) tunnelCount.textContent = t('app_0_files');
        if (tunnelActiveMeta) {
            tunnelActiveMeta.classList.add('hidden');
            tunnelActiveMeta.textContent = '';
        }
        if (tunnelQRCode) {
            tunnelQRCode.innerHTML = '';
        }
        setTunnelEntryControlsHidden(false);
    }

    function applyTunnelUI(tunnel, qrPayload = '') {
        activeTunnel = tunnel;
        const parent = recentSection?.parentElement;
        if (parent && tunnelEndBtn && tunnelFilesSection && recentSection) {
            parent.insertBefore(tunnelEndBtn, recentSection);
            parent.insertBefore(tunnelFilesSection, recentSection);
        }
        tunnelEndBtn?.classList.remove('hidden');
        tunnelFilesSection?.classList.remove('hidden');
        tunnelControlsSection?.classList.remove('hidden');
        setTunnelEntryControlsHidden(true);

        const expiresLabel = tunnel?.expires_at ? formatExpiryDate(tunnel.expires_at) : t('format_soon');
        if (tunnelActiveMeta) {
            tunnelActiveMeta.classList.remove('hidden');
            tunnelActiveMeta.innerHTML = `
                <span class="tunnel-meta-label">${t('tunnel_code')}</span>
                <span class="tunnel-code-pill">${escapeHtml(tunnel.code)}</span>
                <span class="tunnel-meta-divider">·</span>
                <span>${t('tunnel_status')}${escapeHtml(tunnel.status)}</span>
                <span class="tunnel-meta-divider">·</span>
                <span>${t('label_expires')}${escapeHtml(expiresLabel)}</span>
            `;
        }

        const waitingConfirm = !!(tunnel && (!tunnel.initiator_confirmed || !tunnel.peer_confirmed));
        tunnelConfirmWrap?.classList.toggle('hidden', !waitingConfirm);

        if (qrPayload && window.QRCode && tunnelQRCode) {
            tunnelQRCode.innerHTML = '';
            new QRCode(tunnelQRCode, {
                text: qrPayload,
                width: 176,
                height: 176,
                colorDark: '#f3f3f3',
                colorLight: '#111111',
            });
            tunnelQRWrap?.classList.remove('hidden');
        }
    }

    function renderTunnelFiles(items) {
        if (!tunnelList || !tunnelEmpty || !tunnelCount) return;

        const files = Array.isArray(items) ? items : [];
        tunnelCount.textContent = `${files.length} ${t(files.length === 1 ? 'label_file' : 'label_files')}`;

        if (!files.length) {
            tunnelList.classList.add('hidden');
            tunnelEmpty.classList.remove('hidden');
            return;
        }

        tunnelEmpty.classList.add('hidden');
        tunnelList.classList.remove('hidden');
        tunnelList.innerHTML = files.map((item) => `
            <article class="recent-item" data-file-id="${escapeHtml(item.file_id || '')}" data-file-name="${escapeHtml(item.filename || '')}" data-tunnel-id="${escapeHtml(activeTunnel?.id || '')}">
                <div class="recent-main">
                    <div class="recent-name-wrap">
                        <div class="recent-name" title="${escapeHtml(item.filename)}">${escapeHtml(item.filename)}</div>
                    </div>
                    <div class="recent-actions">
                        <button class="recent-action" data-action="download" aria-label="${t('label_download_tunnel')}" title="${t('label_download_tunnel')}">
                            <i data-lucide="download" style="width: 0.85rem; height: 0.85rem;"></i>
                        </button>
                    </div>
                </div>
                <div class="recent-meta">
                    <span>${SecureCrypto.formatFileSize(item.size_bytes)}</span>
                    <span>${t('label_uploaded')}${formatUploadDate(item.created_at)}</span>
                </div>
            </article>
        `).join('');

        tunnelList.querySelectorAll('.recent-action').forEach((btn) => {
            btn.addEventListener('click', handleTunnelFileAction);
        });

        if (window.lucide?.createIcons) {
            window.lucide.createIcons();
        }
    }

    async function handleTunnelFileAction(event) {
        const button = event.currentTarget;
        const item = button.closest('.recent-item');
        if (!item) return;

        const fileId = item.dataset.fileId;
        const fileName = item.dataset.fileName;
        const tunnelId = item.dataset.tunnelId;

        try {
            button.disabled = true;
            await downloadOwnedFile(fileId, fileName, tunnelId, item);
        } catch (error) {
            console.error('Tunnel download failed:', error);
            showErrorBanner(error.message || t('toast_tunnel_download_failed'));
        } finally {
            button.disabled = false;
        }
    }

    async function refreshTunnelState() {
        if (!activeTunnel?.id) return;

        try {
            const response = await fetch(`/api/me/tunnels/${encodeURIComponent(activeTunnel.id)}`, {
                headers: { 'X-CSRF-Token': getCookieValue('csrf_token') }
            });
            if (!response.ok) {
                if (response.status === 410 || response.status === 404 || response.status === 403) {
                    clearTunnelState();
                    showInfoBanner(t('toast_tunnel_session_ended'));
                    return;
                }
                throw new Error('Failed to refresh tunnel state');
            }

            const payload = await response.json().catch(() => ({}));
            if (payload?.tunnel) {
                applyTunnelUI(payload.tunnel);
            }
            renderTunnelFiles(payload?.files || []);
        } catch (error) {
            console.error('Tunnel refresh failed:', error);
        }
    }

    function startTunnelPolling() {
        if (tunnelPollTimer) {
            clearInterval(tunnelPollTimer);
        }
        tunnelPollTimer = setInterval(() => {
            refreshTunnelState();
        }, 4000);
    }

    async function handleStartTunnel() {
        if (!authDeviceIdentity) {
            try {
                authDeviceIdentity = await SecureCrypto.getOrCreateDeviceIdentity();
            } catch (error) {
                showErrorBanner(tpl('toast_device_identity_failed', {msg: error.message}));
                return;
            }
        }

        const duration = tunnelDurationSelect?.value || '1h';
        const response = await fetch('/api/me/tunnels/start', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'X-CSRF-Token': getCookieValue('csrf_token')
            },
            body: JSON.stringify({
                duration,
                device_id: authDeviceIdentity?.deviceId || ''
            })
        });

        if (!response.ok) {
            const error = await response.json().catch(() => ({}));
            throw new Error(error.error || 'Failed to start tunnel');
        }

        const payload = await response.json();
        applyTunnelUI(payload.tunnel, payload.qr_payload || '');
        showTunnelInfo(t('toast_tunnel_created'));
        startTunnelPolling();

        await fetch(`/api/me/tunnels/${encodeURIComponent(payload.tunnel.id)}/confirm`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'X-CSRF-Token': getCookieValue('csrf_token')
            },
            body: JSON.stringify({ device_id: authDeviceIdentity?.deviceId || '' })
        }).catch(() => {});

        refreshTunnelState();
    }

    async function handleJoinTunnel() {
        if (!authDeviceIdentity) {
            try {
                authDeviceIdentity = await SecureCrypto.getOrCreateDeviceIdentity();
            } catch (error) {
                showErrorBanner(tpl('toast_device_identity_failed', {msg: error.message}));
                return;
            }
        }

        const code = (tunnelJoinCode?.value || '').trim();
        if (!code) {
            showErrorBanner(t('toast_tunnel_enter_code'));
            return;
        }

        const response = await fetch('/api/me/tunnels/join', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'X-CSRF-Token': getCookieValue('csrf_token')
            },
            body: JSON.stringify({
                code,
                device_id: authDeviceIdentity?.deviceId || ''
            })
        });

        if (!response.ok) {
            const error = await response.json().catch(() => ({}));
            throw new Error(error.error || 'Failed to join tunnel');
        }

        const payload = await response.json();
        applyTunnelUI(payload.tunnel, payload.qr_payload || '');
        showTunnelInfo(t('toast_tunnel_joined'));
        startTunnelPolling();
        await refreshTunnelState();
    }

    async function handleConfirmTunnel() {
        if (!activeTunnel?.id) return;

        const response = await fetch(`/api/me/tunnels/${encodeURIComponent(activeTunnel.id)}/confirm`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'X-CSRF-Token': getCookieValue('csrf_token')
            },
            body: JSON.stringify({ device_id: authDeviceIdentity?.deviceId || '' })
        });

        if (!response.ok) {
            const error = await response.json().catch(() => ({}));
            throw new Error(error.error || 'Failed to confirm tunnel');
        }

        const payload = await response.json().catch(() => ({}));
        if (payload?.tunnel) {
            applyTunnelUI(payload.tunnel);
        }
        showTunnelInfo(t('toast_tunnel_confirmed'));
    }

    async function handleEndTunnel() {
        if (!activeTunnel?.id) return;

        const confirmed = await openActionModal({
            title: t('toast_tunnel_end_title'),
            description: t('toast_tunnel_end_desc'),
            confirmText: t('toast_tunnel_end_confirm'),
            cancelText: t('toast_tunnel_end_cancel'),
            kicker: t('toast_tunnel_end_kicker'),
            tone: 'warning'
        });
        if (!confirmed) return;

        const response = await fetch(`/api/me/tunnels/${encodeURIComponent(activeTunnel.id)}`, {
            method: 'DELETE',
            headers: {
                'Content-Type': 'application/json',
                'X-CSRF-Token': getCookieValue('csrf_token')
            },
            body: JSON.stringify({ device_id: authDeviceIdentity?.deviceId || '' })
        });

        if (!response.ok) {
            const error = await response.json().catch(() => ({}));
            throw new Error(error.error || 'Failed to end tunnel');
        }

        clearTunnelState();
        showInfoBanner(t('toast_tunnel_ended'));
    }

    async function prefetchRecentLockStates(items) {
        if (!Array.isArray(items) || !items.length) {
            return;
        }

        await Promise.allSettled(items.map(async (item) => {
            const fileId = item?.file_id;
            if (!fileId) return;
            if (recentFileStates.get(fileId)?.locked) return;

            try {
                await getOwnedFilePassphrase(fileId);
            } catch (error) {
                if (isLockedFileError(error)) {
                    markRecentFileLocked(fileId, LOCKED_FILE_INFO);
                }
            }
        }));
    }

    function updateRecentPagination() {
        if (!recentPagination || !recentPrev || !recentNext || !recentPageLabel) return;

        const hasPages = recentTotalPages > 1;
        recentPagination.classList.toggle('hidden', !hasPages);
        if (!hasPages) {
            return;
        }

        recentPrev.disabled = recentCurrentPage <= 1;
        recentNext.disabled = recentCurrentPage >= recentTotalPages;
        recentPageLabel.textContent = `Page ${recentCurrentPage} of ${recentTotalPages}`;
    }

    function setRecentSearchOpen(isOpen) {
        recentSearchOpen = isOpen;
        if (!recentSearchWrap || !recentSearchToggle) return;

        recentSearchWrap.classList.toggle('hidden', !isOpen);
        recentSearchToggle.setAttribute('aria-expanded', isOpen ? 'true' : 'false');
        recentSearchToggle.innerHTML = isOpen
            ? '<i data-lucide="x" style="width: 0.9rem; height: 0.9rem;"></i>'
            : '<i data-lucide="search" style="width: 0.9rem; height: 0.9rem;"></i>';

        if (window.lucide?.createIcons) {
            window.lucide.createIcons();
        }

        if (isOpen) {
            recentSearchInput?.focus();
            return;
        }

        if (recentSearchQuery) {
            recentSearchQuery = '';
            if (recentSearchInput) {
                recentSearchInput.value = '';
            }
            recentCurrentPage = 1;
            loadRecentUploads(1);
        }
    }

    function handleRecentSearchInput() {
        if (!recentSearchInput) return;
        const nextQuery = recentSearchInput.value.trim();
        if (nextQuery === recentSearchQuery) return;

        recentSearchQuery = nextQuery;
        recentCurrentPage = 1;

        if (recentSearchDebounceTimer) {
            clearTimeout(recentSearchDebounceTimer);
        }
        recentSearchDebounceTimer = setTimeout(() => {
            loadRecentUploads(1);
        }, RECENT_SEARCH_DEBOUNCE_MS);
    }

    async function handleRecentAction(event) {
        const button = event.currentTarget;
        const item = button.closest('.file-entry');
        if (!item) return;

        const fileId = item.dataset.fileId;
        const fileName = item.dataset.fileName;
        const shareUrl = item.dataset.shareUrl;
        const action = button.dataset.action;
        let keepDisabled = false;

        try {
            if (action === 'download') {
                button.disabled = true;
                await downloadOwnedFile(fileId, fileName, '', item);
            } else if (action === 'copy') {
                const passphrase = await getOwnedFilePassphrase(fileId);
                const copied = await copyToClipboard(`${shareUrl}#${passphrase}`, false, true);
                if (!copied) {
                    showToast(t('toast_copy_failed'));
                }
            }
        } catch (error) {
            console.error(error);
            if (isLockedFileError(error)) {
                markRecentFileLocked(fileId, error.message);
                keepDisabled = true;
                showErrorBanner(error.message);
                return;
            }
            showErrorBanner(tpl('toast_action_failed', {msg: error.message}));
        } finally {
            if (!keepDisabled) {
                button.disabled = false;
            }
        }
    }

    async function downloadOwnedFile(fileId, fileName, tunnelId = '', cardEl = null) {
        if (activeDownloads.has(fileId)) return;
        const passphrase = await getOwnedFilePassphrase(fileId, tunnelId);

        let progressFill = null;
        let downloadBtn = null;
        let originalBtnHtml = '';

        if (cardEl) {
            const progressBar = document.createElement('div');
            progressBar.className = 'file-download-progress';
            progressFill = document.createElement('div');
            progressFill.className = 'file-download-progress-bar';
            progressBar.appendChild(progressFill);
            cardEl.appendChild(progressBar);

            activeDownloads.add(fileId);
            downloadBtn = cardEl.querySelector('.recent-action[data-action="download"]');
            if (downloadBtn && !downloadBtn.disabled) {
                originalBtnHtml = downloadBtn.innerHTML;
                downloadBtn.innerHTML = '<div class="downloading-spinner" style="width:16px;height:16px;border-width:2px;"></div>';
                downloadBtn.disabled = true;
            }
        }

        const updateProgress = (pct) => {
            if (progressFill) {
                progressFill.style.width = `${Math.min(100, Math.max(0, pct))}%`;
            }
        };

        try {
            const response = await fetch(`/api/file/${fileId}/download`);
            if (!response.ok) {
                throw new Error('Failed to download encrypted file');
            }

            const contentLength = response.headers.get('Content-Length');
            const total = parseInt(contentLength, 10);
            const reader = response.body.getReader();
            const chunks = [];
            let received = 0;

            while (true) {
                const { done, value } = await reader.read();
                if (done) break;
                chunks.push(value);
                received += value.length;
                if (total) {
                    updateProgress((received / total) * 80);
                }
            }

            const encryptedBlob = new Blob(chunks);
            let decrypted;
            try {
                decrypted = await SecureCrypto.decryptBlob(encryptedBlob, passphrase, (progress) => {
                    updateProgress(80 + progress * 0.2);
                });
            } catch (error) {
                const lockedError = new Error('This file is locked. This could happen if you recovered your account after you uploaded this file.');
                lockedError.code = 'FILE_LOCKED';
                throw lockedError;
            }

            const blob = new Blob([decrypted], { type: 'application/octet-stream' });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = fileName || `${fileId}.bin`;
            document.body.appendChild(a);
            a.click();
            a.remove();
            URL.revokeObjectURL(url);

            updateProgress(100);
            await new Promise((resolve) => setTimeout(resolve, 600));
        } finally {
            activeDownloads.delete(fileId);
            if (progressFill && progressFill.parentNode) {
                const bar = progressFill.parentNode;
                if (bar.parentNode) bar.parentNode.removeChild(bar);
            }
            if (downloadBtn && originalBtnHtml) {
                downloadBtn.innerHTML = originalBtnHtml;
                downloadBtn.disabled = false;
            }
        }
    }

    async function getOwnedFilePassphrase(fileId, tunnelId = '') {
        const cached = SecureCrypto.getCachedFileKey(fileId);
        if (cached) {
            return cached;
        }

        if (!authDeviceIdentity) {
            const ready = await ensureDeviceReady();
            if (!ready && isDeviceUntrusted) {
                throw new Error('Approve this device from a trusted device to access your files.');
            }
        }

        let accessUrl = '';
        if (tunnelId) {
            accessUrl = `/api/tunnels/${encodeURIComponent(tunnelId)}/files/${encodeURIComponent(fileId)}/access`;
        } else if (AUTHENTICATED) {
            accessUrl = `/api/me/files/${fileId}/access?device_id=${encodeURIComponent(authDeviceIdentity.deviceId)}`;
        } else {
            throw new Error('A tunnel is required to access this file.');
        }

        const response = await fetch(accessUrl, {
            headers: { 'X-CSRF-Token': getCookieValue('csrf_token') }
        });
        if (!response.ok) {
            const errorPayload = await response.json().catch(() => ({}));
            if (tunnelId && AUTHENTICATED && errorPayload.code !== 'TUNNEL_NOT_AVAILABLE') {
                const fallbackUrl = `/api/me/files/${fileId}/access?device_id=${encodeURIComponent(authDeviceIdentity.deviceId)}`;
                const fallbackRes = await fetch(fallbackUrl, {
                    headers: { 'X-CSRF-Token': getCookieValue('csrf_token') }
                });
                if (fallbackRes.ok) {
                    const payload = await fallbackRes.json();
                    const wrappedDEK = SecureCrypto.fromBase64(payload.file_key_envelope.wrapped_dek_b64);
                    const dekWrapAlg = (payload.file_key_envelope.dek_wrap_alg || '').toUpperCase();
                    let dekBytes;
                    if (dekWrapAlg.startsWith('RAW-DEK')) {
                        dekBytes = wrappedDEK;
                    } else if (dekWrapAlg.startsWith('RSA-OAEP')) {
                        if (!AUTHENTICATED) {
                            if (!ephemeralKeyPair) throw new Error('Ephemeral key not available for guest decryption.');
                            const raw = await crypto.subtle.decrypt({ name: 'RSA-OAEP' }, ephemeralKeyPair.privateKey, wrappedDEK);
                            dekBytes = new Uint8Array(raw);
                        } else {
                            dekBytes = await SecureCrypto.unwrapUserKeyForDevice(wrappedDEK, authDeviceIdentity.privateKeyJWK);
                        }
                    } else {
                        let userKeyRaw = SecureCrypto.getUserKeyRaw(CNS_USER_ID);
                        if (!userKeyRaw) {
                            const wrappedUKB64 = payload?.user_key_envelope?.wrapped_uk_b64;
                            if (!wrappedUKB64) throw new Error('Unable to access decryption key for this file on this device.');
                            const wrappedUK = SecureCrypto.fromBase64(wrappedUKB64);
                            userKeyRaw = await SecureCrypto.unwrapUserKeyForDevice(wrappedUK, authDeviceIdentity.privateKeyJWK);
                            SecureCrypto.saveUserKeyRaw(CNS_USER_ID, userKeyRaw);
                        }
                        const nonce = payload.file_key_envelope.dek_wrap_nonce_b64 ? SecureCrypto.fromBase64(payload.file_key_envelope.dek_wrap_nonce_b64) : new Uint8Array();
                        dekBytes = await SecureCrypto.unwrapSecretWithUserKey(wrappedDEK, nonce, userKeyRaw);
                    }
                    const passphrase = new TextDecoder().decode(dekBytes);
                    SecureCrypto.cacheFileKey(fileId, passphrase);
                    return passphrase;
                }
            }
            throw new Error(errorPayload.error || 'Unable to access decryption key for this file.');
        }

        const payload = await response.json();
        const wrappedDEK = SecureCrypto.fromBase64(payload.file_key_envelope.wrapped_dek_b64);
        const dekWrapAlg = (payload.file_key_envelope.dek_wrap_alg || '').toUpperCase();
        let dekBytes;

        if (dekWrapAlg.startsWith('RAW-DEK')) {
            dekBytes = wrappedDEK;
        } else if (dekWrapAlg.startsWith('RSA-OAEP')) {
            if (ephemeralKeyPair?.privateKey) {
                try {
                    const raw = await crypto.subtle.decrypt({ name: 'RSA-OAEP' }, ephemeralKeyPair.privateKey, wrappedDEK);
                    dekBytes = new Uint8Array(raw);
                } catch (error) {
                    throw new Error('Failed to decrypt file key with ephemeral key.');
                }
            } else if (!AUTHENTICATED) {
                throw new Error('Ephemeral key not available for guest decryption.');
            } else {
                try {
                    dekBytes = await SecureCrypto.unwrapUserKeyForDevice(wrappedDEK, authDeviceIdentity.privateKeyJWK);
                } catch (error) {
                    const lockedError = new Error('This file is locked. This could happen if you recovered your account after you uploaded this file.');
                    lockedError.code = 'FILE_LOCKED';
                    throw lockedError;
                }
            }
        } else {
            if (!AUTHENTICATED) {
                throw new Error('Unsupported key envelope for guest decryption.');
            }
            let userKeyRaw = SecureCrypto.getUserKeyRaw(CNS_USER_ID);
            if (!userKeyRaw) {
                const wrappedUKB64 = payload?.user_key_envelope?.wrapped_uk_b64;
                if (!wrappedUKB64) {
                    throw new Error('Unable to access decryption key for this file on this device.');
                }

                const wrappedUK = SecureCrypto.fromBase64(wrappedUKB64);
                try {
                    userKeyRaw = await SecureCrypto.unwrapUserKeyForDevice(wrappedUK, authDeviceIdentity.privateKeyJWK);
                } catch (error) {
                    const lockedError = new Error('This file is locked. This could happen if you recovered your account after you uploaded this file.');
                    lockedError.code = 'FILE_LOCKED';
                    throw lockedError;
                }
                SecureCrypto.saveUserKeyRaw(CNS_USER_ID, userKeyRaw);
            }

            const nonce = payload.file_key_envelope.dek_wrap_nonce_b64
                ? SecureCrypto.fromBase64(payload.file_key_envelope.dek_wrap_nonce_b64)
                : new Uint8Array();
            try {
                dekBytes = await SecureCrypto.unwrapSecretWithUserKey(wrappedDEK, nonce, userKeyRaw);
            } catch (error) {
                const lockedError = new Error('This file is locked. This could happen if you recovered your account after you uploaded this file.');
                lockedError.code = 'FILE_LOCKED';
                throw lockedError;
            }
        }
        const passphrase = new TextDecoder().decode(dekBytes);
        SecureCrypto.cacheFileKey(fileId, passphrase);
        return passphrase;
    }

    async function buildTunnelPeerEnvelope(secretBytes) {
        if (!AUTHENTICATED || !activeTunnel?.id || !secretBytes) {
            return null;
        }

        const response = await fetch(`/api/me/tunnels/${encodeURIComponent(activeTunnel.id)}/peer-wrap-key`, {
            headers: { 'X-CSRF-Token': getCookieValue('csrf_token') }
        });

        if (!response.ok) {
            const errorPayload = await response.json().catch(() => ({}));
            if (errorPayload.code === 'PEER_KEY_NOT_REQUIRED') {
                return null;
            }
            throw new Error(errorPayload.error || 'Failed to fetch tunnel peer key material');
        }

        const payload = await response.json();
        if (!payload?.public_key_jwk) {
            return null;
        }

        const wrappedForPeer = await SecureCrypto.wrapUserKeyForDevice(secretBytes, payload.public_key_jwk);
        return {
            peer_wrapped_dek_b64: SecureCrypto.toBase64(wrappedForPeer),
            peer_dek_wrap_alg: 'RSA-OAEP-2048-v1',
            peer_dek_wrap_version: 1
        };
    }

    function isLockedFileError(error) {
        if (!error) return false;
        return error.code === 'FILE_LOCKED' || /locked|recovered your account/i.test(error.message || '');
    }

    function markRecentFileLocked(fileId, reason) {
        if (!fileId) return;
        recentFileStates.set(fileId, { locked: true, reason: reason || LOCKED_FILE_INFO });
        updateRecentFileLockedState(fileId);
    }

    function updateRecentFileLockedState(fileId) {
        if (!fileId) return;

        const selectors = [
            recentList?.querySelector(`.file-entry[data-file-id="${CSS.escape(fileId)}"]`),
            popupRecentList?.querySelector(`.popup-entry[data-file-id="${CSS.escape(fileId)}"]`)
        ];

        selectors.forEach((item) => {
            if (!item) return;

            item.classList.add('is-locked');
            item.setAttribute('title', LOCKED_FILE_INFO);

            if (item.classList.contains('file-entry')) {
                item.querySelectorAll('.recent-action').forEach((btn) => {
                    btn.disabled = true;
                });
                const infoEl = item.querySelector('.file-info');
                const expiresAt = item.dataset.expiresAt;
                if (infoEl && expiresAt) {
                    infoEl.textContent = t('label_file_info_locked') + formatExpiryDate(expiresAt);
                }
            } else if (item.classList.contains('popup-entry')) {
                const downloadBtn = item.querySelector('.popup-entry-download');
                if (downloadBtn) downloadBtn.disabled = true;
            }
        });
    }

    function formatUploadDate(dateStr) {
        const date = new Date(dateStr);
        const now = new Date();
        const todayStart = new Date(now.getFullYear(), now.getMonth(), now.getDate());
        const dateStart = new Date(date.getFullYear(), date.getMonth(), date.getDate());
        const dayDiff = Math.round((dateStart - todayStart) / 86400000);
        const time = date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

        if (dayDiff === 0) return `Today ${time}`;
        if (dayDiff === -1) return `Yesterday ${time}`;
        return date.toLocaleString([], { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
    }

    function formatExpiryDate(dateStr) {
        const date = new Date(dateStr);
        const now = new Date();
        if (date <= now) return t('format_expired');

        const todayStart = new Date(now.getFullYear(), now.getMonth(), now.getDate());
        const dateStart = new Date(date.getFullYear(), date.getMonth(), date.getDate());
        const dayDiff = Math.round((dateStart - todayStart) / 86400000);

        if (dayDiff === 0) {
            return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
        }
        if (dayDiff === 1) {
            return t('format_tomorrow');
        }
        return date.toLocaleDateString([], { year: 'numeric', month: 'short', day: 'numeric' });
    }

    function escapeHtml(value) {
        return String(value || '')
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#39;');
    }

     

    const recentFilesOverlay = document.getElementById('recent-files-overlay');
    const recentFilesPopup = recentFilesOverlay?.querySelector('.recent-files-popup');
    const popupRecentList = document.getElementById('popup-recent-list');
    const popupClose = recentFilesOverlay?.querySelector('.popup-close');

    let recentFilesCache = null;
    let recentFilesCacheTimer = null;

    async function refreshRecentFilesCache() {
        if (!AUTHENTICATED) return;
        try {
            const params = new URLSearchParams({ page: '1', per_page: '100' });
            const response = await fetch(`/api/me/recent-uploads?${params.toString()}`, {
                headers: { 'X-CSRF-Token': getCookieValue('csrf_token') }
            });
            if (!response.ok) throw new Error('Failed to load uploads');
            const payload = await response.json();
            recentFilesCache = payload.items || [];
        } catch (error) {
            console.error('Failed to refresh recent files cache:', error);
        }
    }

    async function openRecentFilesPopup() {
        if (!AUTHENTICATED || !recentFilesOverlay || !recentFilesPopup) return;

        recentFilesOverlay.classList.remove('hidden');
        recentFilesPopup.classList.remove('closing');
        document.addEventListener('keydown', onEscKey);

        if (window.lucide && lucide.createIcons) {
            lucide.createIcons();
        }

        if (recentFilesCache) {
            renderPopupRecentFiles(recentFilesCache);
        } else {
            popupRecentList.innerHTML = '<p class="popup-empty">' + t('state_loading') + '</p>';
            await refreshRecentFilesCache();
            if (recentFilesCache) {
                renderPopupRecentFiles(recentFilesCache);
            } else {
                popupRecentList.innerHTML = '<p class="popup-empty">' + t('state_failed_load') + '</p>';
            }
        }

        prefetchRecentLockStates(recentFilesCache || []).catch(() => {});
    }

    function closeRecentFilesPopup() {
        if (!recentFilesOverlay || !recentFilesPopup) return;
        recentFilesOverlay.classList.add('hidden');
        recentFilesPopup.classList.remove('closing');
        document.removeEventListener('keydown', onEscKey);
    }

    function onEscKey(e) {
        if (e.key === 'Escape') closeRecentFilesPopup();
    }

    function renderPopupRecentFiles(items) {
        if (!items.length) {
            popupRecentList.innerHTML = '<p class="popup-empty">' + t('state_no_files_yet') + '</p>';
            return;
        }

        popupRecentList.innerHTML = items.map((item) => {
            const locked = recentFileStates.get(item.file_id)?.locked;
            const expiresText = formatExpiryDate(item.expires_at);
            return `
                <div class="popup-entry${locked ? ' is-locked' : ''}" data-file-id="${escapeHtml(item.file_id)}" data-file-name="${escapeHtml(item.filename)}" data-share-url="${escapeHtml(item.share_url)}" data-expires-at="${item.expires_at}"${locked ? ` title="${LOCKED_FILE_INFO}"` : ''}>
                    <span class="popup-entry-filename" title="${escapeHtml(item.filename)}">${escapeHtml(item.filename)}</span>
                    <span class="popup-entry-expires">${t('label_expires')}${expiresText}</span>
                    <button class="popup-entry-download" aria-label="${t('label_download')}" title="${t('label_download')}" ${locked ? 'disabled' : ''}>
                        <svg xmlns="http://www.w3.org/2000/svg" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
                            <polyline points="7 10 12 15 17 10"/>
                            <line x1="12" y1="15" x2="12" y2="3"/>
                        </svg>
                    </button>
                </div>
            `;
        }).join('');

        popupRecentList.querySelectorAll('.popup-entry-download').forEach((btn) => {
            const entry = btn.closest('.popup-entry');
            if (!entry) return;
            btn.addEventListener('click', () => {
                if (entry.classList.contains('is-locked')) return;
                const fileId = entry.dataset.fileId;
                const fileName = entry.dataset.fileName;
                const shareUrl = entry.dataset.shareUrl;
                if (fileId && fileName) {
                    downloadOwnedFile(fileId, fileName, '', entry);
                }
            });
        });
    }

    function setupEventListeners() {
        dropZone?.addEventListener('click', () => fileInput?.click());
        dropZone?.addEventListener('dragover', handleDragOver);
        dropZone?.addEventListener('dragleave', handleDragLeave);
        dropZone?.addEventListener('drop', handleDrop);
        fileInput?.addEventListener('change', handleFileSelect);

        const resetVaultEl = document.getElementById('reset-vault');
        resetVaultEl?.addEventListener('click', (e) => {
            e.stopPropagation();
            resetUpload();
        });
        const startOverBtn = document.getElementById('start-over-btn');
        startOverBtn?.addEventListener('click', () => resetUpload());
        finalizeBtn?.addEventListener('click', handleFinalize);

         
        document.querySelectorAll('.copy-trigger').forEach(btn => {
            btn.addEventListener('click', async function() {
                const input = this.parentElement.querySelector('input');
                const copied = await copyToClipboard(input.value);
                if (!copied) {
                    showToast(t('toast_copy_failed'));
                    return;
                }
                const original = this.innerHTML;
                this.innerHTML = '<i data-lucide="check" style="width: 1rem; height: 1rem;"></i>';
                this.style.background = 'var(--accent)';
                this.style.color = '#000';
                lucide.createIcons();
                setTimeout(() => {
                    this.innerHTML = original;
                    this.style.background = 'transparent';
                    this.style.color = 'inherit';
                    lucide.createIcons();
                }, 2000);
            });
        });

        recentSearchToggle?.addEventListener('click', () => {
            setRecentSearchOpen(!recentSearchOpen);
        });
        recentSearchInput?.addEventListener('input', handleRecentSearchInput);
        recentPrev?.addEventListener('click', () => {
            if (recentCurrentPage > 1) {
                loadRecentUploads(recentCurrentPage - 1);
            }
        });
        recentNext?.addEventListener('click', () => {
            if (recentCurrentPage < recentTotalPages) {
                loadRecentUploads(recentCurrentPage + 1);
            }
        });

        recentRecoverDevice?.addEventListener('click', handleRecoverLostDevice);

        deviceApprovalApprove?.addEventListener('click', handleApprovePendingEnrollment);
        deviceApprovalDecline?.addEventListener('click', handleDeclinePendingEnrollment);
        deviceApprovalRecover?.addEventListener('click', handleRecoverLostDevice);

        const downloadCloseBtn = document.getElementById('download-activity-close');
        downloadCloseBtn?.addEventListener('click', () => showDownloadActivityOverlay(false));

        tunnelControlsSection?.classList.remove('hidden');
        tunnelStartBtn?.addEventListener('click', async () => {
            try {
                await handleStartTunnel();
            } catch (error) {
                showErrorBanner(error.message || t('toast_tunnel_failed_start'));
            }
        });
        tunnelJoinBtn?.addEventListener('click', async () => {
            try {
                await handleJoinTunnel();
            } catch (error) {
                showErrorBanner(error.message || t('toast_tunnel_failed_join'));
            }
        });
        tunnelConfirmBtn?.addEventListener('click', async () => {
            try {
                await handleConfirmTunnel();
                await refreshTunnelState();
            } catch (error) {
                showErrorBanner(error.message || t('toast_tunnel_failed_confirm'));
            }
        });
        tunnelEndBtn?.addEventListener('click', async () => {
            try {
                await handleEndTunnel();
            } catch (error) {
                showErrorBanner(error.message || t('toast_tunnel_failed_end'));
            }
        });

         

        const recentFilesLink = document.querySelector('.recent-files-link');
        recentFilesLink?.addEventListener('click', (e) => {
            e.preventDefault();
            openRecentFilesPopup();
        });
        popupClose?.addEventListener('click', closeRecentFilesPopup);
        recentFilesOverlay?.addEventListener('click', (e) => {
            if (e.target === recentFilesOverlay) {
                closeRecentFilesPopup();
            }
        });
    }

    function handleDragOver(e) {
        e.preventDefault();
        e.stopPropagation();
        e.dataTransfer.dropEffect = 'copy';
        dropZone.classList.add('active');
    }

    function handleDragLeave(e) {
        e.preventDefault();
        e.stopPropagation();
         
        if (e.target === dropZone) {
            dropZone.classList.remove('active');
        }
    }

    function handleDrop(e) {
        e.preventDefault();
        e.stopPropagation();
        dropZone.classList.remove('active');

        const files = e.dataTransfer.files;
        if (files.length > 0) {
            processFile(files[0]);
        }
    }

    function handleFileSelect(e) {
        const files = e.target.files;
        if (files.length > 0) {
            processFile(files[0]);
        }
    }

    async function processFile(file) {
        if (isUploading || isFinalizing) return;

        if (file.size > MAX_FILE_SIZE) {
            showFileSizeWarning();
            return;
        }
        if (file.size === 0) {
            showErrorBanner(t('app_cannot_upload_empty'));
            return;
        }

        selectedFile = file;
        fileName.textContent = file.name;
        fileSize.textContent = SecureCrypto.formatFileSize(file.size);

        dropZone.classList.add('hidden');
        fileDetails.classList.remove('hidden');
        stageEntry.classList.add('hidden');
        stageProcessing.classList.remove('hidden');
        stagePending.classList.add('hidden');
        stageOutput.classList.add('hidden');
        statusText.textContent = t('status_uploading_short');
        processMain.textContent = t('status_uploading_short');
        processSub.textContent = '';

        runProtocolInBackground();
    }

    function showFileSizeWarning() {
        const sub = dropZone.querySelector('p');
        if (sub) {
            const original = sub.textContent;
            sub.textContent = tpl('app_file_too_large', {size: SecureCrypto.formatFileSize(MAX_FILE_SIZE)});
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
        statusText.textContent = t('status_uploading');

        if (uploadComplete) {
            finalizeUpload();
        } else if (uploadError) {
            isFinalizing = false;
            updateFinalizeButtonState();
            stageProcessing.classList.add('hidden');
            stagePending.classList.remove('hidden');
            showErrorBanner(tpl('toast_upload_failed', {msg: uploadError}));
        } else {
            const poll = setInterval(() => {
                if (uploadComplete) {
                    clearInterval(poll);
                    finalizeUpload();
                } else if (uploadError) {
                    clearInterval(poll);
                    isFinalizing = false;
                    updateFinalizeButtonState();
                    stageProcessing.classList.add('hidden');
                    stagePending.classList.remove('hidden');
                    statusText.textContent = t('status_ready');
                    statusText.style.color = 'var(--accent)';
                    showErrorBanner(tpl('toast_upload_failed', {msg: uploadError}));
                }
            }, 500);
        }
    }

    function updateFinalizeButtonState() {
        finalizeBtn.disabled = isFinalizing;
    }
    function updateUploadProgress() {
        processMain.textContent = t('status_uploading_short');
        processSub.textContent = '';
    }

    async function runProtocolInBackground() {
        isUploading = true;
        uploadComplete = false;
        uploadError = null;
        finalizeEnvelopePayload = null;

        try {
            generatedPassword = await SecureCrypto.generatePassword();
            const dekBytes = new TextEncoder().encode(generatedPassword);

            if (!AUTHENTICATED && activeTunnel?.id) {
                
                if (!ephemeralKeyPair) {
                    const keyPair = await crypto.subtle.generateKey(
                        {
                            name: 'RSA-OAEP',
                            modulusLength: 2048,
                            publicExponent: new Uint8Array([1, 0, 1]),
                            hash: 'SHA-256'
                        },
                        false,
                        ['encrypt', 'decrypt']
                    );
                    const publicKeyJWK = await crypto.subtle.exportKey('jwk', keyPair.publicKey);
                    ephemeralKeyPair = { publicKeyJWK, privateKey: keyPair.privateKey };
                }
                const publicKey = await crypto.subtle.importKey(
                    'jwk',
                    ephemeralKeyPair.publicKeyJWK,
                    { name: 'RSA-OAEP', hash: 'SHA-256' },
                    false,
                    ['encrypt']
                );
                const wrapped = await crypto.subtle.encrypt({ name: 'RSA-OAEP' }, publicKey, dekBytes);
                finalizeEnvelopePayload = {
                    wrapped_dek_b64: SecureCrypto.toBase64(new Uint8Array(wrapped)),
                    dek_wrap_alg: 'RSA-OAEP-2048-v1',
                    dek_wrap_version: 1
                };
            }

            if (AUTHENTICATED) {
                if (!authUserKeyRaw) {
                    await ensureDeviceReady();
                    authUserKeyRaw = SecureCrypto.getUserKeyRaw(CNS_USER_ID);
                }
                if (!authUserKeyRaw) {
                    throw new Error('Approve this device from a trusted device before uploading as an authenticated user');
                }
                if (authUserKeyRaw) {
                    const wrapped = await SecureCrypto.wrapSecretWithUserKey(dekBytes, authUserKeyRaw);
                    finalizeEnvelopePayload = {
                        wrapped_dek_b64: SecureCrypto.toBase64(wrapped.wrapped),
                        dek_wrap_alg: 'AES-GCM-UK-v1',
                        dek_wrap_nonce_b64: SecureCrypto.toBase64(wrapped.nonce),
                        dek_wrap_version: 1
                    };

                    if (activeTunnel?.id) {
                        const peerEnvelope = await buildTunnelPeerEnvelope(dekBytes);
                        if (peerEnvelope) {
                            finalizeEnvelopePayload = {
                                ...finalizeEnvelopePayload,
                                ...peerEnvelope
                            };
                        }
                    }
                }
            }
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
            if (!isFinalizing) {
                handleFinalize();
            }
        } catch (error) {
            console.error('Upload pipeline failed:', error);
            uploadError = error.message;
            isUploading = false;
            uploadComplete = false;
            isFinalizing = false;
            updateFinalizeButtonState();
            stageProcessing.classList.add('hidden');
            stageEntry.classList.remove('hidden');
            statusText.textContent = t('status_ready');
            statusText.style.color = 'var(--accent)';
            showErrorBanner(tpl('toast_upload_failed', {msg: error.message}));
        }
    }

    async function waitForAssembly(sessionId, intervalMs = 1500, timeoutMs = 600000) {
        const deadline = Date.now() + timeoutMs;
        while (Date.now() < deadline) {
            const res = await fetch(`/api/upload/status/${sessionId}`);
            if (!res.ok) throw new Error('Failed to check assembly status');
            const { status } = await res.json();
            if (status === 'done') return;
            if (status.startsWith('error:')) throw new Error(status.slice(6));
            statusText.textContent = t('status_finalizing');
            await new Promise(r => setTimeout(r, intervalMs));
        }
        throw new Error('Assembly timed out');
    }

    async function runProtocol() {
        stageEntry.classList.add('hidden');
        stagePending.classList.add('hidden');
        stageOutput.classList.add('hidden');
        stageProcessing.classList.remove('hidden');
        statusText.textContent = t('status_uploading');
        
        try {
            generatedPassword = await SecureCrypto.generatePassword();
            updateProgress(0, t('status_scrambling'), t('status_uploading'));

            const totalChunks = Math.ceil(selectedFile.size / CHUNK_SIZE);
            const initResponse = await initUpload(selectedFile.size, totalChunks);
            uploadSessionId = initResponse.session_id;
            let uploadedCount = 0;

            await SecureCrypto.encryptFileChunked(
                selectedFile,
                generatedPassword,
                CHUNK_SIZE,
                async (chunkIndex, chunkData) => {
                    await uploadOneChunk(uploadSessionId, chunkIndex, chunkData);
                    uploadedCount++;
                    updateProgress(
                        50 + (uploadedCount / totalChunks) * 45,
                        t('status_to_clouds'),
                        t('status_uploading')
                    );
                },
                { concurrency: PARALLEL_CHUNK_UPLOADS }
            );

            const completeResponse = await completeUpload();
            await waitForAssembly(uploadSessionId);
            pendingExpiresAt = completeResponse.pending_expires_at
                ? new Date(completeResponse.pending_expires_at).getTime()
                : null;
            startPendingCountdown();
            showPendingUI();
        } catch (error) {
            console.error('Something failed:', error);
            showErrorBanner(tpl('toast_something_failed', {msg: error.message}));
        }
    }

    async function uploadOneChunk(sessionId, chunkIndex, chunkData) {
        let lastError;

        for (let attempt = 0; attempt < MAX_CHUNK_UPLOAD_RETRIES; attempt++) {
            if (attempt > 0) {
                await new Promise(r => setTimeout(r, 2000 * attempt));
            }

            try {
                const formData = new FormData();
                formData.append('session_id', sessionId);
                formData.append('chunk_index', chunkIndex.toString());
                formData.append('chunk', new Blob([chunkData]));

                const response = await fetch('/api/upload/chunk', {
                    method: 'POST',
                    headers: {
                        'X-CSRF-Token': getCookieValue('csrf_token')
                    },
                    body: formData
                });

                if (!response.ok) {
                    const error = await response.json();
                    throw new Error(error.error || `Failed to upload chunk ${chunkIndex + 1}`);
                }

                return;
            } catch (error) {
                lastError = error;
                console.warn(`Chunk ${chunkIndex} attempt ${attempt + 1} failed:`, error.message);
            }
        }

        throw lastError;
    }

    async function initUpload(fileSize, totalChunks) {
        const response = await fetch('/api/upload/init', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'X-CSRF-Token': getCookieValue('csrf_token')
            },
            body: JSON.stringify({
                file_name: selectedFile.name,
                file_size: fileSize,
                total_chunks: totalChunks,
                chunk_size: CHUNK_SIZE
            })
        });

        if (!response.ok) {
            const error = await response.json();
            throw new Error(error.error || 'Failed to initialize upload');
        }

        return response.json();
    }

    async function completeUpload() {
        updateProgress(95, t('status_everything_arrived'), t('status_finalizing'));

        const response = await fetch('/api/upload/complete', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'X-CSRF-Token': getCookieValue('csrf_token')
            },
            body: JSON.stringify({
                session_id: uploadSessionId,
                confirmed: true
            })
        });

        if (!response.ok) {
            const error = await response.json();
            throw new Error(error.error || 'Failed to complete upload');
        }

        updateProgress(100, t('app_yippe'), t('status_complete'));
        return response.json();
    }

    function showPendingUI() {
        hideErrorBanner();
        stageEntry.classList.add('hidden');
        stageProcessing.classList.add('hidden');
        stagePending.classList.remove('hidden');
        stageOutput.classList.add('hidden');
        statusText.textContent = uploadError ? t('status_upload_failed') : t('status_pending');
        statusText.style.color = uploadError ? '#f44336' : 'var(--accent)';
        updateFinalizeButtonState();
    }

    function showPending(response) {
        uploadSessionId = response.session_id;
        pendingExpiresAt = response.pending_expires_at ? new Date(response.pending_expires_at).getTime() : null;
        showPendingUI();
        startPendingCountdown();
    }

    function selectedDuration() {
        return RETENTION;
    }

    function selectedDurationLabel() {
        return RETENTION_LABEL;
    }

    async function finalizeUpload() {
        statusText.textContent = t('status_finalizing');

        try {
            const finalizePayload = {
                session_id: uploadSessionId,
                ...(finalizeEnvelopePayload || {})
            };
            if (activeTunnel?.id) {
                finalizePayload.tunnel_id = activeTunnel.id;

                if (AUTHENTICATED && generatedPassword && activeTunnel?.peer_cns_user_id && activeTunnel.peer_cns_user_id !== CNS_USER_ID) {
                    if (!finalizePayload.peer_wrapped_dek_b64) {
                        const dekBytes = new TextEncoder().encode(generatedPassword);
                        const peerEnvelope = await buildTunnelPeerEnvelope(dekBytes);
                        if (!peerEnvelope) {
                            throw new Error('Cross-account tunnel upload requires a peer key envelope. Peer may not be ready yet.');
                        }
                        Object.assign(finalizePayload, peerEnvelope);
                    }
                }
            } else {
                finalizePayload.duration = selectedDuration();
            }

            const response = await fetch('/api/upload/finalize', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': getCookieValue('csrf_token')
                },
                body: JSON.stringify(finalizePayload)
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.error || 'Failed to finalize upload');
            }

            const payload = await response.json();
            showSuccess(payload);
            if (activeTunnel?.id) {
                refreshTunnelState();
            }
        } catch (error) {
            console.error('Finalize failed:', error);
            isFinalizing = false;
            updateFinalizeButtonState();
            stageProcessing.classList.add('hidden');
            stagePending.classList.remove('hidden');
            statusText.textContent = t('status_ready');
            statusText.style.color = 'var(--accent)';
            showErrorBanner(tpl('toast_finalize_failed', {msg: error.message}));
        }
    }

    async function cancelUpload() {
        if (!uploadSessionId) return;

        await fetch('/api/upload/cancel', {
            method: 'DELETE',
            headers: {
                'Content-Type': 'application/json',
                'X-CSRF-Token': getCookieValue('csrf_token')
            },
            body: JSON.stringify({
                session_id: uploadSessionId
            })
        });

        uploadSessionId = null;
    }

    function showSuccess(response) {
        clearPendingCountdown();
        isFinalizing = false;
        if (response.file_id && generatedPassword) {
            SecureCrypto.cacheFileKey(response.file_id, generatedPassword);
        }

        const fullShareUrl = `${response.share_url}#${generatedPassword}`;
        outUrl.value = fullShareUrl;
        outPin.value = response.numeric_code;
        outKey.value = generatedPassword;
        outExpiryLabel.textContent = t('label_expiry_prefix') + RETENTION_LABEL + t('label_retention');
        uploadSessionId = null;
        lastShareUrl = fullShareUrl;

        stageProcessing.classList.add('hidden');
        stagePending.classList.add('hidden');
        stageOutput.classList.remove('hidden');
        statusText.textContent = t('status_secure');
        statusText.style.color = 'var(--accent)';

        setupIdleCopy(fullShareUrl);

        if (AUTHENTICATED) {
            loadRecentUploads().catch(() => {});
        }
    }

    function showNotification(message, type = 'error') {
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

    function showInfoBanner(message) {
        showNotification(message, 'info');
    }

    function showRecoveryBanner(message) {
        isDeviceUntrusted = true;
        setRecoveryActionVisible(true);
        showInfoBanner(message);
    }

    function hideErrorBanner() {}

    function updateProgress(percent, sub, main) {
        progressVal.textContent = `${Math.floor(Math.min(100, Math.max(0, percent)))}%`;
        if (sub) processSub.textContent = sub;
        if (main) processMain.textContent = main;
    }

    async function copyToClipboard(text, silent = false, showBanner = false) {
        try {
            await navigator.clipboard.writeText(text);
            if (!silent) {
                if (showBanner) {
                    showShareBanner();
                } else {
                    showToast(t('toast_copied'));
                }
            }
            return true;
        } catch (error) {
            console.error('Failed to copy:', error);
            const textarea = document.createElement('textarea');
            textarea.value = text;
            textarea.style.position = 'fixed';
            textarea.style.opacity = '0';
            document.body.appendChild(textarea);
            textarea.select();
            const copiedWithFallback = document.execCommand('copy');
            document.body.removeChild(textarea);

            if (!copiedWithFallback) {
                return false;
            }

            if (!silent) {
                if (showBanner) {
                    showShareBanner();
                } else {
                    showToast(t('toast_copied'));
                }
            }
            return true;
        }
    }

    async function attemptAutoCopy(text) {
        const copied = await copyToClipboard(text, true, true);
        if (copied) {
            showShareBanner();
            return;
        }

        showToast(t('toast_copy_retry'));
        queueAutoCopyOnNextInteraction(text, true);
    }

    function queueAutoCopyOnNextInteraction(text, showBanner) {
        pendingAutoCopyText = text;
        pendingAutoCopyBanner = showBanner;

        if (pendingAutoCopyBound) {
            return;
        }

        pendingAutoCopyBound = true;
        ['click', 'keydown', 'touchstart'].forEach((eventName) => {
            document.addEventListener(eventName, handlePendingAutoCopy, true);
        });
    }

    async function handlePendingAutoCopy() {
        if (!pendingAutoCopyText) {
            clearPendingAutoCopyListeners();
            return;
        }

        const textToCopy = pendingAutoCopyText;
        const shouldShowBanner = pendingAutoCopyBanner;
        const copied = await copyToClipboard(textToCopy, true, shouldShowBanner);

        if (copied) {
            if (shouldShowBanner) {
                showShareBanner();
            } else {
                showToast(t('toast_copied'));
            }
            pendingAutoCopyText = null;
            pendingAutoCopyBanner = false;
            clearPendingAutoCopyListeners();
        }
    }

    function clearPendingAutoCopyListeners() {
        if (!pendingAutoCopyBound) {
            return;
        }

        ['click', 'keydown', 'touchstart'].forEach((eventName) => {
            document.removeEventListener(eventName, handlePendingAutoCopy, true);
        });
        pendingAutoCopyBound = false;
    }

    function showShareBanner() {
        showToast(t('toast_link_copied_notification'));    }

    function setupIdleCopy(text) {
        idleCopyDone = false;
        idleCopyBannerShown = false;
        const infoBox = stageOutput.querySelector('.info-box');
        if (infoBox) {
            const idleMsg = document.createElement('p');
            idleMsg.className = 'info-text';
            idleMsg.id = 'idle-copy-msg';
            idleMsg.textContent = t('toast_copy_move_mouse');
            idleMsg.style.cursor = 'pointer';
            idleMsg.style.color = 'var(--accent)';
            infoBox.parentNode.insertBefore(idleMsg, infoBox);
        }

        const onMove = () => {
            if (idleCopyDone) return;
            copyToClipboard(text, true, true).then(ok => {
                if (ok) {
                    idleCopyDone = true;
                    const msg = document.getElementById('idle-copy-msg');
                    if (msg) msg.textContent = t('toast_link_copied_idle');
                    showShareBanner();
                    setTimeout(() => {
                        idleCopyDone = false;
                        const msg2 = document.getElementById('idle-copy-msg');
                        if (msg2) msg2.textContent = t('toast_copy_move_mouse');
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

    function showToast(message) {
        showNotification(message, 'info');
    }

    function resetUpload() {
        clearPendingCountdown();
        pendingAutoCopyText = null;
        pendingAutoCopyBanner = false;
        clearPendingAutoCopyListeners();
        hideErrorBanner();
        selectedFile = null;
        generatedPassword = null;
        const sessionToCancel = uploadSessionId;
        uploadSessionId = null;
        pendingExpiresAt = null;
        finalizeEnvelopePayload = null;
        isFinalizing = false;
        isUploading = false;
        uploadComplete = false;
        uploadError = null;
        idleCopyDone = false;
        idleCopyBannerShown = false;
        lastShareUrl = '';

        if (sessionToCancel) {
            fetch('/api/upload/cancel', {
                method: 'DELETE',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': getCookieValue('csrf_token')
                },
                body: JSON.stringify({ session_id: sessionToCancel })
            }).catch(e => console.error('Failed to cancel upload:', e));
        }

        fileInput.value = '';
        dropZone.classList.remove('hidden');
        fileDetails.classList.add('hidden');
        finalizeBtn.disabled = true;
        statusText.textContent = t('status_ready');
        statusText.style.color = 'var(--accent)';
        stageEntry.classList.remove('hidden');
        stageProcessing.classList.add('hidden');
        stagePending.classList.add('hidden');
        stageOutput.classList.add('hidden');

        const msg = document.getElementById('idle-copy-msg');
        if (msg) msg.remove();
    }

    function startPendingCountdown() {
        clearPendingCountdown();
        if (!pendingExpiresAt) {
            pendingCountdown.textContent = t('state_upload_pending');
            return;
        }

        const tick = () => {
            const remainingMs = pendingExpiresAt - Date.now();
            if (remainingMs <= 0) {
                clearPendingCountdown();
                pendingCountdown.textContent = t('state_upload_expired');
                openActionModal({
                    title: t('toast_session_expired_title'),
                    description: t('toast_session_expired_desc'),
                    confirmText: t('toast_session_expired_confirm'),
                    hideCancel: true,
                    kicker: t('toast_session_expired_kicker')
                });
                resetUpload();
                return;
            }

            const totalSeconds = Math.floor(remainingMs / 1000);
            const minutes = Math.floor(totalSeconds / 60);
            const seconds = totalSeconds % 60;
            pendingCountdown.textContent = `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
        };

        tick();
        pendingCountdownTimer = setInterval(tick, 1000);
    }

    function clearPendingCountdown() {
        if (pendingCountdownTimer) {
            clearInterval(pendingCountdownTimer);
            pendingCountdownTimer = null;
        }
    }

    function setRecoveryActionVisible(visible) {
        if (!recentRecoverDevice) return;
        recentRecoverDevice.classList.toggle('hidden', !(visible && isDeviceUntrusted));
    }

    function showDownloadActivityOverlay(show) {
        if (!downloadActivityOverlay) return;

        if (!show) {
            downloadActivityOverlay.classList.add('hidden');
            downloadActivityOverlay.setAttribute('aria-hidden', 'true');
            return;
        }

        const isComplete = show === 'complete';
        downloadActivityOverlay.classList.remove('hidden');
        downloadActivityOverlay.setAttribute('aria-hidden', 'false');

        const titleEl = document.getElementById('download-activity-title');
        const iconEl = document.getElementById('download-activity-icon');
        const percentEl = document.getElementById('download-activity-percent');

        if (isComplete) {
            if (titleEl) titleEl.textContent = t('status_complete');
            if (iconEl) {
                iconEl.innerHTML = '<i data-lucide="check-circle" style="width:36px;height:36px;color:#007AFF;"></i>';
            }
            if (percentEl) percentEl.textContent = t('state_download_automatically');
        } else {
            if (titleEl) titleEl.textContent = t('status_downloading');
            if (iconEl) {
                iconEl.innerHTML = '<div class="downloading-spinner"></div>';
            }
            if (percentEl) percentEl.textContent = t('app_0_pct');
        }

        if (window.lucide?.createIcons) {
            window.lucide.createIcons();
        }
    }

    function openActionModal(options) {
        if (!actionModal || !actionModalTitle || !actionModalDescription || !actionModalConfirm || !actionModalCancel) {
            return Promise.resolve(false);
        }

        if (actionModalResolver) {
            actionModalResolver(false);
            actionModalResolver = null;
        }

        const {
            title = t('action_modal_title'),
            description = '',
            confirmText = t('action_modal_continue'),
            cancelText = t('action_modal_cancel'),
            hideCancel = false,
            tone = 'default'
        } = options || {};

        actionModal.classList.remove('action-tone-warning');
        if (tone === 'warning') {
            actionModal.classList.add('action-tone-warning');
        }

        const hiddenOverlays = [];
        document.querySelectorAll('.tos-overlay:not(.hidden)').forEach((overlay) => {
            if (overlay === actionModal) return;
            hiddenOverlays.push(overlay);
            overlay.classList.add('hidden');
            overlay.setAttribute('aria-hidden', 'true');
        });

        actionModalTitle.textContent = title;
        actionModalDescription.textContent = description;
        actionModalConfirm.textContent = confirmText;
        actionModalCancel.textContent = cancelText;
        actionModalCancel.classList.toggle('hidden', !!hideCancel);

        actionModal.classList.remove('hidden');
        actionModal.setAttribute('aria-hidden', 'false');

        return new Promise((resolve) => {
            const close = (value) => {
                actionModal.classList.add('hidden');
                actionModal.setAttribute('aria-hidden', 'true');
                actionModalConfirm.removeEventListener('click', onConfirm);
                actionModalCancel.removeEventListener('click', onCancel);
                actionModal.removeEventListener('click', onBackdrop);
                actionModalResolver = null;

                hiddenOverlays.forEach((overlay) => {
                    if (overlay.classList.contains('hidden')) {
                        overlay.classList.remove('hidden');
                        overlay.setAttribute('aria-hidden', 'false');
                    }
                });

                resolve(value);
            };

            const onConfirm = () => close(true);
            const onCancel = () => close(false);
            const onBackdrop = (event) => {
                if (event.target === actionModal && !hideCancel) {
                    close(false);
                }
            };

            actionModalResolver = close;
            actionModalConfirm.addEventListener('click', onConfirm);
            actionModalCancel.addEventListener('click', onCancel);
            actionModal.addEventListener('click', onBackdrop);
        });
    }

     
    const style = document.createElement('style');
    style.textContent = `
        @keyframes fadeIn {
            from { opacity: 0; transform: translateX(-50%) translateY(20px); }
            to { opacity: 1; transform: translateX(-50%) translateY(0); }
        }
        @keyframes fadeOut {
            from { opacity: 1; transform: translateX(-50%) translateY(0); }
            to { opacity: 0; transform: translateX(-50%) translateY(20px); }
        }
    `;
    document.head.appendChild(style);

     
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();