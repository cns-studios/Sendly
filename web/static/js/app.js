(function() {
    'use strict';

     
    const CHUNK_SIZE = 5 * 1024 * 1024;  
    const AUTHENTICATED = window.CONFIG?.authenticated || false;
    const CNS_USER_ID = window.CONFIG?.cnsUserId || 0;
    const CNS_USERNAME = window.CONFIG?.cnsUsername || '';
    const TOS_VERSION = window.CONFIG?.tosVersion || '2026-04-05';
    const TOS_COOKIE_NAME = 'shareit_tos_accepted';
    const MAX_FILE_SIZE = AUTHENTICATED ? (1.5 * 1024 * 1024 * 1024) : 786432000;
    const ALLOWED_DURATIONS = window.CONFIG?.allowedDurations || ['24h', '7d'];    const PARALLEL_CHUNK_UPLOADS = window.CONFIG?.parallelChunkUploads || 6;
    const MAX_CHUNK_UPLOAD_RETRIES = 5;
    const RECENT_UPLOADS_PER_PAGE = 10;
    const RECENT_SEARCH_DEBOUNCE_MS = 180;

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
    let pendingAutoCopyText = null;
    let pendingAutoCopyBanner = false;
    let pendingAutoCopyBound = false;
    let errorBannerHideTimer = null;
    let errorBannerCloseTimer = null;
    let authDeviceIdentity = null;
    let authUserKeyRaw = null;
    let finalizeEnvelopePayload = null;
    let recentCurrentPage = 1;
    let recentTotalPages = 0;
    let recentSearchQuery = '';
    let recentSearchDebounceTimer = null;
    let recentSearchOpen = false;

     
    const dropZone = document.getElementById('drop-zone');
    const fileInput = document.getElementById('file-input');
    const fileDetails = document.getElementById('file-details');
    const fileName = document.getElementById('file-name');
    const fileSize = document.getElementById('file-size');
    const resetVault = document.getElementById('reset-vault');
    const startOverBtn = document.getElementById('start-over-btn');
    const finalizeBtn = document.getElementById('finalize-btn');
    const statusText = document.getElementById('status-text');
    const errorBanner = document.getElementById('error-banner');
    const errorBannerText = document.getElementById('error-banner-text');
    const errorBannerClose = document.getElementById('error-banner-close');

    const stageEntry = document.getElementById('stage-entry');
    const stageProcessing = document.getElementById('stage-processing');
    const stagePending = document.getElementById('stage-pending');
    const stageOutput = document.getElementById('stage-output');
    const pendingCountdown = document.getElementById('pending-countdown');
    
    const progressVal = document.getElementById('progress-val');
    const processMain = document.getElementById('process-main');
    const processSub = document.getElementById('process-sub');

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
    const actionModalKicker = document.getElementById('action-modal-kicker');
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
    let isDeviceUntrusted = false;
    const recentFileStates = new Map();
    const LOCKED_FILE_INFO = 'This file was encrypted on a different trusted device. Because recovery happened after this files was uploaded, this client cannot unlock that older file key. Please re-upload this file again. To avoid this in the future, approve new devices from already trusted devices (this is a trusted device) so you can keep access to all your files across devices.';
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

    async function init() {
        setupTOSGate();

         
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
            await ensureDeviceReady();
            await loadRecentUploads();
            await loadPendingEnrollments();
        }
    }

    function applyTierUI() {
        if (!AUTHENTICATED) {
            const nudge = document.getElementById('auth-nudge');
            if (nudge) nudge.classList.remove('hidden');
        }

        document.querySelectorAll('input[name="expiration"]').forEach(input => {
            const allowed = ALLOWED_DURATIONS.includes(input.value);
            input.disabled = !allowed;
            const label = input.closest('.duration-option');
            if (label) {
                if (allowed) {
                    label.classList.remove('duration-locked');
                    const hint = label.querySelector('.lock-hint');
                    if (hint) hint.style.display = 'none';
                }
            }
        });

        const firstAllowed = document.querySelector('input[name="expiration"]:not([disabled])');
        if (firstAllowed) firstAllowed.checked = true;
    }

    async function ensureDeviceReady() {
        try {
            const payload = await registerCurrentDevice(true);
            if (payload?.needs_enrollment) {
                isDeviceUntrusted = true;
                setRecoveryActionVisible(true);
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
                            device_label: `${CNS_USERNAME || 'ShareIt User'} device`,
                            public_key_jwk: authDeviceIdentity.publicKeyJWK,
                            key_algorithm: authDeviceIdentity.keyAlgorithm,
                            key_version: authDeviceIdentity.keyVersion
                        }
                    }, 1);
                    return;
                }

                showRecoveryBanner('Approve this browser from a trusted device before decrypting or finalizing authenticated uploads.');
                return;
            }

            isDeviceUntrusted = false;
            setRecoveryActionVisible(false);
        } catch (error) {
            console.error('Failed to initialize authenticated device state:', error);
            showErrorBanner('Authenticated key setup failed. Recent uploads may be unavailable on this device.');
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
                device_label: `${CNS_USERNAME || 'ShareIt User'} device`,
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
            await prefetchRecentLockStates(payload?.items || []);
            renderRecentUploads(payload);
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

            if (pendingEnrollmentItems.length > 0) {
                showApprovalEnrollment(pendingEnrollmentItems[0], pendingEnrollmentItems.length);
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

        deviceApprovalTitle.textContent = 'New device wants access';
        deviceApprovalMessage.textContent = 'A new device from your CNS account wants to view your files. If you did not ask for this, decline and change your CNS password.';
        deviceApprovalMeta.innerHTML = [
            `<span>Device: ${escapeHtml(deviceName)}</span>`,
            `<span>Device ID: ${escapeHtml(deviceId)}</span>`,
            `<span>Key: ${escapeHtml(keyAlgorithm)}</span>`,
            `<span>Requested: ${escapeHtml(requestedAt)}</span>`
        ].join('');

        if (deviceApprovalCount) {
            deviceApprovalCount.textContent = count > 1 ? `${count} pending` : '1 pending';
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

        deviceApprovalTitle.textContent = 'Waiting for approval';
        deviceApprovalMessage.textContent = 'This browser requested access and is waiting for a trusted device to approve it.';
        deviceApprovalMeta.innerHTML = [
            `<span>Device: ${escapeHtml(deviceName)}</span>`,
            `<span>Device ID: ${escapeHtml(deviceId)}</span>`,
            `<span>Requested: ${escapeHtml(requestedAt)}</span>`
        ].join('');

        if (deviceApprovalCount) {
            deviceApprovalCount.textContent = count > 1 ? `${count} pending` : 'Waiting';
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

            await loadPendingEnrollments();
            await loadRecentUploads();
        } catch (error) {
            console.error('Approve enrollment failed:', error);
            showErrorBanner('Approval failed: ' + error.message);
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
            showErrorBanner('Decline failed: ' + error.message);
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
            title: 'Recover this browser?',
            description: 'This rotates trusted-device state and makes this browser your new trusted device. Previously protected files may remain unreadable until they are re-shared or re-uploaded.',
            confirmText: 'Recover device',
            cancelText: 'Cancel',
            kicker: 'Important',
            tone: 'warning'
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
            showInfoBanner('This browser is now the new trusted device. Previously protected files may need to be re-shared or re-uploaded.');
            await loadRecentUploads();
        } catch (error) {
            console.error('Lost-device recovery failed:', error);
            showErrorBanner('Recovery failed: ' + error.message);
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
                showErrorBanner('This approval request was declined or expired. Request a new approval from a trusted device.');
                return;
            }

            isDeviceUntrusted = false;
            setRecoveryActionVisible(false);
            hidePendingEnrollmentModal();
            await loadRecentUploads();
        } catch (error) {
            console.error('Failed to finalize pending enrollment:', error);
            showErrorBanner('Approval detected, but this browser could not finish setup.');
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

        pendingEnrollmentSocket.onmessage = handlePendingEnrollmentSocketMessage;
        pendingEnrollmentSocket.onclose = schedulePendingEnrollmentSocketReconnect;
        pendingEnrollmentSocket.onerror = () => {
            try {
                pendingEnrollmentSocket?.close();
            } catch (_) {
                // ignore
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
        const currentDeviceId = authDeviceIdentity?.deviceId;
        const isCurrentDevice = currentDeviceId && requestDeviceId && requestDeviceId === currentDeviceId;

        if (eventType === 'device_enrollment_approved' && isCurrentDevice) {
            finalizeWaitingEnrollment();
            return;
        }

        if (eventType === 'device_enrollment_rejected' && isCurrentDevice) {
            hidePendingEnrollmentModal();
            showErrorBanner('This device approval request was declined. Request approval again from another trusted device.');
            return;
        }

        loadPendingEnrollments();
    }

    function startPendingEnrollmentRefreshTimer() {
        if (!AUTHENTICATED || pendingEnrollmentRefreshTimer) {
            return;
        }

        pendingEnrollmentRefreshTimer = setInterval(() => {
            if (pendingEnrollmentMode === 'idle') {
                return;
            }

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
                    ? 'No uploads match this search.'
                    : 'No uploads yet on this account.';
            }
            if (recentCount) recentCount.textContent = `${totalItems} files`;
            updateRecentPagination();
            return;
        }

        setRecentState('ready');
        if (recentCount) {
            recentCount.textContent = `${totalItems} file${totalItems === 1 ? '' : 's'}`;
        }

        recentList.innerHTML = items.map((item) => `
            <article class="recent-item ${recentFileStates.get(item.file_id)?.locked ? 'is-locked' : ''}" data-file-id="${item.file_id}" data-file-name="${escapeHtml(item.filename)}" data-share-url="${item.share_url}">
                <div class="recent-main">
                    <div class="recent-name-wrap">
                        <div class="recent-name" title="${escapeHtml(item.filename)}">${escapeHtml(item.filename)}</div>
                        ${recentFileStates.get(item.file_id)?.locked ? `<div class="recent-lock-pill" title="${escapeHtml(LOCKED_FILE_INFO)}" aria-label="${escapeHtml(LOCKED_FILE_INFO)}">Locked on this browser</div>` : ''}
                    </div>
                    <div class="recent-actions">
                        <button class="recent-action" data-action="download" aria-label="Download file" title="Download file" ${recentFileStates.get(item.file_id)?.locked ? 'disabled' : ''}>
                            <i data-lucide="download" style="width: 0.85rem; height: 0.85rem;"></i>
                        </button>
                        <button class="recent-action" data-action="copy" aria-label="Copy share link" title="Copy share link" ${recentFileStates.get(item.file_id)?.locked ? 'disabled' : ''}>
                            <i data-lucide="link" style="width: 0.85rem; height: 0.85rem;"></i>
                        </button>
                    </div>
                </div>
                <div class="recent-meta">
                    <span>${SecureCrypto.formatFileSize(item.size_bytes)}</span>
                    <span>Uploaded ${formatUploadDate(item.created_at)}</span>
                    <span>Expires ${formatExpiryDate(item.expires_at)}</span>
                </div>
            </article>
        `).join('');

        recentList.querySelectorAll('.recent-action').forEach((btn) => {
            btn.addEventListener('click', handleRecentAction);
        });
        updateRecentPagination();
        if (window.lucide?.createIcons) {
            window.lucide.createIcons();
        }
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
        const item = button.closest('.recent-item');
        if (!item) return;

        const fileId = item.dataset.fileId;
        const fileName = item.dataset.fileName;
        const shareUrl = item.dataset.shareUrl;
        const action = button.dataset.action;
        let keepDisabled = false;

        try {
            if (action === 'download') {
                button.disabled = true;
                await downloadOwnedFile(fileId, fileName);
            } else if (action === 'copy') {
                const passphrase = await getOwnedFilePassphrase(fileId);
                const copied = await copyToClipboard(`${shareUrl}#${passphrase}`, false, true);
                if (!copied) {
                    showToast('Copy failed. Please use Ctrl+C.');
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
            showErrorBanner(`Action failed: ${error.message}`);
        } finally {
            if (!keepDisabled) {
                button.disabled = false;
            }
        }
    }

    async function downloadOwnedFile(fileId, fileName) {
        const passphrase = await getOwnedFilePassphrase(fileId);
        showDownloadActivityOverlay(true);
        try {
            const response = await fetch(`/api/file/${fileId}/download`);
            if (!response.ok) {
                throw new Error('Failed to download encrypted payload');
            }
            const encryptedBlob = await response.blob();
            let decrypted;
            try {
                decrypted = await SecureCrypto.decryptBlob(encryptedBlob, passphrase);
            } catch (error) {
                const lockedError = new Error('This file is locked on this browser. Recover this browser as trusted to open it.');
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
        } finally {
            showDownloadActivityOverlay(false);
        }
    }

    async function getOwnedFilePassphrase(fileId) {
        const cached = SecureCrypto.getCachedFileKey(fileId);
        if (cached) {
            return cached;
        }

        if (!authDeviceIdentity) {
            await ensureDeviceReady();
        }
        const response = await fetch(`/api/me/files/${fileId}/access?device_id=${encodeURIComponent(authDeviceIdentity.deviceId)}`, {
            headers: { 'X-CSRF-Token': getCookieValue('csrf_token') }
        });
        if (!response.ok) {
            const errorPayload = await response.json().catch(() => ({}));
            throw new Error(errorPayload.error || 'Unable to access wrapped key for this file');
        }

        const payload = await response.json();
        let userKeyRaw = SecureCrypto.getUserKeyRaw(CNS_USER_ID);
        if (!userKeyRaw) {
            const wrappedUK = SecureCrypto.fromBase64(payload.user_key_envelope.wrapped_uk_b64);
            try {
                userKeyRaw = await SecureCrypto.unwrapUserKeyForDevice(wrappedUK, authDeviceIdentity.privateKeyJWK);
            } catch (error) {
                const lockedError = new Error('This file is locked on this browser. Recover from another trusted device to unlock it.');
                lockedError.code = 'FILE_LOCKED';
                throw lockedError;
            }
            SecureCrypto.saveUserKeyRaw(CNS_USER_ID, userKeyRaw);
        }

        const wrappedDEK = SecureCrypto.fromBase64(payload.file_key_envelope.wrapped_dek_b64);
        const nonce = payload.file_key_envelope.dek_wrap_nonce_b64
            ? SecureCrypto.fromBase64(payload.file_key_envelope.dek_wrap_nonce_b64)
            : new Uint8Array();
        let dekBytes;
        try {
            dekBytes = await SecureCrypto.unwrapSecretWithUserKey(wrappedDEK, nonce, userKeyRaw);
        } catch (error) {
            const lockedError = new Error('This file is locked on this browser. Recover from another trusted device to unlock it.');
            lockedError.code = 'FILE_LOCKED';
            throw lockedError;
        }
        const passphrase = new TextDecoder().decode(dekBytes);
        SecureCrypto.cacheFileKey(fileId, passphrase);
        return passphrase;
    }

    function isLockedFileError(error) {
        if (!error) return false;
        return error.code === 'FILE_LOCKED' || /locked on this browser|recover from another trusted device/i.test(error.message || '');
    }

    function markRecentFileLocked(fileId, reason) {
        if (!fileId) return;
        recentFileStates.set(fileId, { locked: true, reason: reason || LOCKED_FILE_INFO });
        updateRecentFileLockedState(fileId);
    }

    function updateRecentFileLockedState(fileId) {
        if (!recentList || !fileId) return;

        const item = recentList.querySelector(`.recent-item[data-file-id="${CSS.escape(fileId)}"]`);
        if (!item) return;

        item.classList.add('is-locked');
        item.querySelectorAll('.recent-action').forEach((btn) => {
            btn.disabled = true;
        });

        const nameWrap = item.querySelector('.recent-name-wrap');
        if (nameWrap && !nameWrap.querySelector('.recent-lock-pill')) {
            const pill = document.createElement('div');
            pill.className = 'recent-lock-pill';
            pill.textContent = 'Locked on this browser';
            pill.title = LOCKED_FILE_INFO;
            pill.setAttribute('aria-label', LOCKED_FILE_INFO);
            nameWrap.appendChild(pill);
        }

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
        if (date <= now) return 'Expired';

        const todayStart = new Date(now.getFullYear(), now.getMonth(), now.getDate());
        const dateStart = new Date(date.getFullYear(), date.getMonth(), date.getDate());
        const dayDiff = Math.round((dateStart - todayStart) / 86400000);

        if (dayDiff === 0) {
            return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
        }
        if (dayDiff === 1) {
            return 'Tomorrow';
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

    function setupEventListeners() {
         
        dropZone.addEventListener('click', () => fileInput.click());
        dropZone.addEventListener('dragover', handleDragOver);
        dropZone.addEventListener('dragleave', handleDragLeave);
        dropZone.addEventListener('drop', handleDrop);
        fileInput.addEventListener('change', handleFileSelect);

         
        resetVault.addEventListener('click', (e) => {
            e.stopPropagation();
            resetUpload();
        });
        startOverBtn.addEventListener('click', () => resetUpload());
        finalizeBtn.addEventListener('click', handleFinalize);

         
        document.querySelectorAll('.copy-trigger').forEach(btn => {
            btn.addEventListener('click', async function() {
                const input = this.parentElement.querySelector('input');
                const copied = await copyToClipboard(input.value);
                if (!copied) {
                    showToast('Copy failed. Please use Ctrl+C.');
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

        document.querySelectorAll('input[name="expiration"]').forEach(input => {
            input.addEventListener('change', updateFinalizeButtonState);
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

        if (errorBannerClose) {
            errorBannerClose.addEventListener('click', hideErrorBanner);
        }

        recentRecoverDevice?.addEventListener('click', handleRecoverLostDevice);

        deviceApprovalApprove?.addEventListener('click', handleApprovePendingEnrollment);
        deviceApprovalDecline?.addEventListener('click', handleDeclinePendingEnrollment);
        deviceApprovalRecover?.addEventListener('click', handleRecoverLostDevice);
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
            showErrorBanner(`File too large. Maximum size is ${SecureCrypto.formatFileSize(MAX_FILE_SIZE)}`);
            return;
        }
        if (file.size === 0) {
            showErrorBanner('Cannot upload empty file or directory.');
            return;
        }

        selectedFile = file;
        fileName.textContent = file.name;
        fileSize.textContent = SecureCrypto.formatFileSize(file.size);

        dropZone.classList.add('hidden');
        fileDetails.classList.remove('hidden');
        stageEntry.classList.add('hidden');
        stageProcessing.classList.add('hidden');
        stagePending.classList.remove('hidden');
        stageOutput.classList.add('hidden');
        statusText.textContent = 'Ready';
        statusText.style.color = 'var(--accent)';
        updateFinalizeButtonState();

        runProtocolInBackground();
    }

    function handleFinalize() {
        if (isFinalizing) return;

        isFinalizing = true;
        updateFinalizeButtonState();
        stagePending.classList.add('hidden');
        stageProcessing.classList.remove('hidden');
        statusText.textContent = 'Uploading...';

        if (uploadComplete) {
            finalizeUpload();
        } else if (uploadError) {
            isFinalizing = false;
            updateFinalizeButtonState();
            stageProcessing.classList.add('hidden');
            stagePending.classList.remove('hidden');
            showErrorBanner('Upload failed: ' + uploadError);
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
                    statusText.textContent = 'Ready';
                    statusText.style.color = 'var(--accent)';
                    showErrorBanner('Upload failed: ' + uploadError);
                }
            }, 500);
        }
    }

    function updateFinalizeButtonState() {
        finalizeBtn.disabled = isFinalizing;
    }
    function updateUploadProgress() {
        if (totalChunks === 0) return;
        const pct = Math.floor((uploadedChunks / totalChunks) * 100);
        progressVal.textContent = `${pct}%`;
        processSub.textContent = pct < 30
            ? 'Sending your file...'
            : pct < 60
            ? 'Upload in progress...'
            : pct < 90
            ? 'Almost there...'
            : 'Finishing up...';
        processMain.textContent = 'Uploading';
    }

    async function runProtocolInBackground() {
        isUploading = true;
        uploadComplete = false;
        uploadError = null;
        finalizeEnvelopePayload = null;

        try {
            generatedPassword = await SecureCrypto.generatePassword();
            if (AUTHENTICATED) {
                if (!authUserKeyRaw) {
                    await ensureDeviceReady();
                    authUserKeyRaw = SecureCrypto.getUserKeyRaw(CNS_USER_ID);
                }
                if (!authUserKeyRaw) {
                    throw new Error('Approve this device from a trusted browser before uploading as an authenticated user');
                }
                if (authUserKeyRaw) {
                    const wrapped = await SecureCrypto.wrapSecretWithUserKey(new TextEncoder().encode(generatedPassword), authUserKeyRaw);
                    finalizeEnvelopePayload = {
                        wrapped_dek_b64: SecureCrypto.toBase64(wrapped.wrapped),
                        dek_wrap_alg: 'AES-GCM-UK-v1',
                        dek_wrap_nonce_b64: SecureCrypto.toBase64(wrapped.nonce),
                        dek_wrap_version: 1
                    };
                }
            }
            encryptedBlob = await SecureCrypto.encryptFile(selectedFile, generatedPassword, () => {});
            await startUploadInBackground();
            uploadComplete = true;
            isUploading = false;
            updateFinalizeButtonState();
            if (!isFinalizing) {
                statusText.textContent = 'Ready';
                statusText.style.color = 'var(--accent)';
            }
        } catch (error) {
            console.error('Upload pipeline failed:', error);
            uploadError = error.message;
            isUploading = false;
            uploadComplete = false;
            isFinalizing = false;
            updateFinalizeButtonState();
            stageProcessing.classList.add('hidden');
            stagePending.classList.remove('hidden');
            statusText.textContent = 'Upload Failed';
            statusText.style.color = 'var(--color-error)';
            showErrorBanner('Upload failed: ' + error.message);
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
            statusText.textContent = 'Finalizing...';
            await new Promise(r => setTimeout(r, intervalMs));
        }
        throw new Error('Assembly timed out');
    }

    async function runProtocol() {
        stageEntry.classList.add('hidden');
        stagePending.classList.add('hidden');
        stageOutput.classList.add('hidden');
        stageProcessing.classList.remove('hidden');
        statusText.textContent = 'Encrypting...';
        
        try {
             
            generatedPassword = await SecureCrypto.generatePassword();
            updateProgress(0, 'Scrambling data', 'Encrypting...');

             
            encryptedBlob = await SecureCrypto.encryptFile(
                selectedFile,
                generatedPassword,
                (progress, status) => {
                    updateProgress(progress * 0.5, status, 'Encrypting...');
                }
            );

            updateProgress(50, 'Up to the clouds', 'Uploading...');

             
            await startUpload();
            
        } catch (error) {
            console.error('Something failed:', error);
            showErrorBanner('Something failed: ' + error.message);
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

        pendingExpiresAt = completeResponse.pending_expires_at
            ? new Date(completeResponse.pending_expires_at).getTime()
            : null;
        startPendingCountdown();
    }

    async function startUpload() {
        if (isUploading || !encryptedBlob) return;

        isUploading = true;

        try {
             
            const initResponse = await initUpload();
            uploadSessionId = initResponse.session_id;

             
            await uploadChunks(initResponse);

             
            const completeResponse = await completeUpload();
            showPending(completeResponse);
            
        } catch (error) {
            console.error('Upload failed:', error);
            
             
            if (uploadSessionId) {
                try {
                    await cancelUpload();
                } catch (e) {
                    console.error('Failed to cancel upload:', e);
                }
            }

            showErrorBanner('Upload failed: ' + error.message);
            resetUpload();
        }

        isUploading = false;
    }

    async function uploadChunksInBackground(initResponse) {
        uploadedChunks = 0;
        await uploadChunksParallel(initResponse, () => {
            uploadedChunks++;
            updateUploadProgress();
        });
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
            if (attempt > 0) {
                await new Promise(r => setTimeout(r, 2000 * attempt));
            }

            try {
                const formData = new FormData();
                formData.append('session_id', sessionId);
                formData.append('chunk_index', chunkIndex.toString());
                formData.append('chunk', chunk);

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

    async function uploadChunksParallel(initResponse, onChunkUploaded) {
        const totalChunks = initResponse.total_chunks;
        const concurrency = Math.max(1, Math.min(PARALLEL_CHUNK_UPLOADS, totalChunks));
        let nextChunkIndex = 0;

        const worker = async () => {
            while (true) {
                const chunkIndex = nextChunkIndex;
                nextChunkIndex++;

                if (chunkIndex >= totalChunks) {
                    return;
                }

                await uploadChunkWithRetry(initResponse.session_id, chunkIndex);
                if (onChunkUploaded) {
                    onChunkUploaded(chunkIndex, totalChunks);
                }
            }
        };

        const workers = Array.from({ length: concurrency }, () => worker());
        await Promise.all(workers);
    }

    async function initUpload() {
        const totalChunks = Math.ceil(encryptedBlob.size / CHUNK_SIZE);

        const response = await fetch('/api/upload/init', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'X-CSRF-Token': getCookieValue('csrf_token')
            },
            body: JSON.stringify({
                file_name: selectedFile.name,
                file_size: encryptedBlob.size,
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

    async function uploadChunks(initResponse) {
        const totalChunks = initResponse.total_chunks;
        let uploadedCount = 0;

        await uploadChunksParallel(initResponse, () => {
            uploadedCount++;
            const progress = 50 + (uploadedCount / totalChunks) * 45;
            updateProgress(progress, `Sending it high to the clouds`, 'Uploading...');
        });
    }

    async function completeUpload() {
        updateProgress(95, 'Making sure everything is okay', 'Finalizing...');

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

        updateProgress(100, 'Yippe', 'Complete!');
        return response.json();
    }

    function showPendingUI() {
        hideErrorBanner();
        stageEntry.classList.add('hidden');
        stageProcessing.classList.add('hidden');
        stagePending.classList.remove('hidden');
        stageOutput.classList.add('hidden');
        statusText.textContent = uploadError ? 'Upload Failed' : 'Pending Finalization';
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
        const checked = document.querySelector('input[name="expiration"]:checked');
        return checked ? checked.value : '24h';
    }

    function selectedDurationLabel() {
        const duration = selectedDuration();
        switch(duration) {
            case '24h': return '24 Hours';
            case '7d': return '7 Days';
            case '30d': return '30 Days';
            case '90d': return '3 Months';
            default: return '24 Hours';
        }
    }

    async function finalizeUpload() {
        statusText.textContent = 'Finalizing...';

        try {
            const response = await fetch('/api/upload/finalize', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': getCookieValue('csrf_token')
                },
                body: JSON.stringify({
                    session_id: uploadSessionId,
                    duration: selectedDuration(),
                    ...(finalizeEnvelopePayload || {})
                })
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.error || 'Failed to finalize upload');
            }

            const payload = await response.json();
            showSuccess(payload);
        } catch (error) {
            console.error('Finalize failed:', error);
            isFinalizing = false;
            updateFinalizeButtonState();
            stageProcessing.classList.add('hidden');
            stagePending.classList.remove('hidden');
            statusText.textContent = 'Ready';
            statusText.style.color = 'var(--accent)';
            showErrorBanner('Finalize failed: ' + error.message);
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
        outExpiryLabel.textContent = `Expiry: ${selectedDurationLabel()} retention.`;
        uploadSessionId = null;

        attemptAutoCopy(fullShareUrl);

        stageProcessing.classList.add('hidden');
        stagePending.classList.add('hidden');
        stageOutput.classList.remove('hidden');
        statusText.textContent = 'Secure';
        statusText.style.color = 'var(--accent)';

        if (AUTHENTICATED) {
            loadRecentUploads().catch(() => {});
        }
    }

    function showBanner(message, tone = 'error') {
        if (!errorBanner) return;
        if (errorBannerText) {
            errorBannerText.textContent = message;
        }
        errorBanner.classList.remove('banner-error', 'banner-info');
        errorBanner.classList.add(tone === 'info' ? 'banner-info' : 'banner-error');

        if (errorBannerHideTimer) {
            clearTimeout(errorBannerHideTimer);
            errorBannerHideTimer = null;
        }
        if (errorBannerCloseTimer) {
            clearTimeout(errorBannerCloseTimer);
            errorBannerCloseTimer = null;
        }

        errorBanner.classList.remove('hidden');
        requestAnimationFrame(() => {
            errorBanner.classList.add('visible');
        });

        errorBannerHideTimer = setTimeout(() => {
            hideErrorBanner();
        }, 4500);
    }

    function showErrorBanner(message) {
        showBanner(message, 'error');
    }

    function showInfoBanner(message) {
        showBanner(message, 'info');
    }

    function showRecoveryBanner(message) {
        isDeviceUntrusted = true;
        setRecoveryActionVisible(true);
        showInfoBanner(message);
    }

    function hideErrorBanner() {
        if (!errorBanner) return;

        if (errorBannerHideTimer) {
            clearTimeout(errorBannerHideTimer);
            errorBannerHideTimer = null;
        }
        if (errorBannerCloseTimer) {
            clearTimeout(errorBannerCloseTimer);
            errorBannerCloseTimer = null;
        }

        if (errorBanner.classList.contains('hidden')) {
            return;
        }

        errorBanner.classList.remove('visible');
        errorBannerCloseTimer = setTimeout(() => {
            if (!errorBanner.classList.contains('visible')) {
                errorBanner.classList.add('hidden');
            }
        }, 320);
    }

    function updateProgress(percent, sub, main) {
        progressVal.textContent = `${Math.floor(percent)}%`;
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
                    showToast('Copied to clipboard!');
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
                    showToast('Copied to clipboard!');
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

        showToast('Tap anywhere to retry copying the sharing link.');
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
                showToast('Copied to clipboard!');
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
        const banner = document.getElementById('share-banner');
        if (!banner) return;
        banner.classList.remove('hidden');
        banner.classList.add('visible');
        const closeBtn = document.getElementById('close-share-banner');
        if (closeBtn) {
            closeBtn.onclick = () => {
                banner.classList.remove('visible');
                setTimeout(() => banner.classList.add('hidden'), 350);
            };
        }
        setTimeout(() => {
            banner.classList.remove('visible');
            setTimeout(() => banner.classList.add('hidden'), 350);
        }, 3500);
    }

    function showToast(message) {
         
        const existingToast = document.querySelector('.toast');
        if (existingToast) {
            existingToast.remove();
        }

         
        const toast = document.createElement('div');
        toast.className = 'toast';
        toast.textContent = message;
        toast.style.cssText = `
            position: fixed;
            bottom: 20px;
            left: 50%;
            transform: translateX(-50%);
            background-color: #333;
            color: white;
            padding: 12px 24px;
            border-radius: 8px;
            z-index: 1000;
            animation: fadeIn 0.3s ease;
        `;

        document.body.appendChild(toast);

         
        setTimeout(() => {
            toast.style.animation = 'fadeOut 0.3s ease';
            setTimeout(() => toast.remove(), 300);
        }, 3000);
    }

    function resetUpload() {
        clearPendingCountdown();
        pendingAutoCopyText = null;
        pendingAutoCopyBanner = false;
        clearPendingAutoCopyListeners();
        hideErrorBanner();
        selectedFile = null;
        encryptedBlob = null;
        generatedPassword = null;
        const sessionToCancel = uploadSessionId;
        uploadSessionId = null;
        pendingExpiresAt = null;
        finalizeEnvelopePayload = null;
        isFinalizing = false;
        isUploading = false;
        uploadComplete = false;
        uploadError = null;

         
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
        statusText.textContent = 'Ready';
        statusText.style.color = 'var(--accent)';
        stageEntry.classList.remove('hidden');
        stageProcessing.classList.add('hidden');
        stagePending.classList.add('hidden');
        stageOutput.classList.add('hidden');
    }

    function startPendingCountdown() {
        clearPendingCountdown();
        if (!pendingExpiresAt) {
            pendingCountdown.textContent = '10:00';
            return;
        }

        const tick = () => {
            const remainingMs = pendingExpiresAt - Date.now();
            if (remainingMs <= 0) {
                clearPendingCountdown();
                pendingCountdown.textContent = '00:00';
                openActionModal({
                    title: 'Upload session expired',
                    description: 'Your upload session timed out. Please select the file again to continue.',
                    confirmText: 'Okay',
                    hideCancel: true,
                    kicker: 'Session'
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
        downloadActivityOverlay.classList.toggle('hidden', !show);
        downloadActivityOverlay.setAttribute('aria-hidden', show ? 'false' : 'true');
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
            title = 'Confirm action',
            description = '',
            confirmText = 'Continue',
            cancelText = 'Cancel',
            hideCancel = false,
            kicker = 'Heads up',
            tone = 'default'
        } = options || {};

        actionModal.classList.remove('action-tone-warning');
        if (tone === 'warning') {
            actionModal.classList.add('action-tone-warning');
        }

        actionModalTitle.textContent = title;
        actionModalDescription.textContent = description;
        actionModalKicker.textContent = kicker;
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