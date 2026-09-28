(function() {
    'use strict';

    const t = (k, d) => window.CONFIG?.t?.[k] || d || k;
    const tpl = (k, vars) => { let s = t(k); if (vars) for (const [key, val] of Object.entries(vars)) s = s.replace(`{${key}}`, val); return s; };

     
    const CHUNK_SIZE = 5 * 1024 * 1024;  
    const AUTHENTICATED = window.CONFIG?.authenticated || false;
    const CNS_USER_ID = window.CONFIG?.cnsUserId || 0;
    const CNS_USERNAME = window.CONFIG?.cnsUsername || '';
    const TOS_VERSION = window.CONFIG?.tosVersion || '2026-09-25';
    const TOS_COOKIE_NAME = 'sendly_tos_accepted';
    const MAX_FILE_SIZE = AUTHENTICATED ? (1.5 * 1024 * 1024 * 1024) : 786432000;
    const RETENTION = AUTHENTICATED ? '90d' : '7d';
    const RETENTION_LABEL = AUTHENTICATED ? '90 Days' : '7 Days';
    const PARALLEL_CHUNK_UPLOADS = window.CONFIG?.parallelChunkUploads || 6;
    const MAX_CHUNK_UPLOAD_RETRIES = 5;
    const RECENT_UPLOADS_PER_PAGE = 10;
    const RECENT_SEARCH_DEBOUNCE_MS = 180;
    const PAGE_LOCALE = document.documentElement.lang || undefined;

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
    let finalizeEnvelopePayload = null;
    let ephemeralKeyPair = null;

    let authDeviceIdentity = null;
    let authIdentityKey = null;
    let recentSearchQuery = '';
    let recentCurrentPage = 1;
    let recentTotalPages = 0;
    let recentLoadToken = 0;
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
    const recentSearchInput = document.getElementById('recent-search-input');
    const recentRecoverDevice = document.getElementById('recent-recover-device');
    const recentDeviceNotice = document.getElementById('recent-device-notice');
    const recentMore = document.getElementById('recent-more');
    const recentRetry = document.getElementById('recent-retry');
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
    let initialDeviceReady = null;
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
            initialDeviceReady = ensureDeviceReady().catch(() => false);
            loadRecentUploads().catch(() => {});
            loadPendingEnrollments().catch(() => {});
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
        const payload = await registerCurrentDevice();
        if (payload?.needs_identity_migration) {
            // The account predates identity keys; a device holding its legacy
            // user key has to migrate it before this device can be trusted.
            isDeviceUntrusted = true;
            return false;
        }
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

            if (!authIdentityKey) {
                showErrorBanner(t('toast_approval_key_setup_failed'));
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

    async function registerCurrentDevice(endpoint = '/api/me/devices/register') {
        const result = await SecureCrypto.registerAuthenticatedDevice({
            endpoint,
            userId: CNS_USER_ID,
            username: CNS_USERNAME,
            csrfToken: getCookieValue('csrf_token')
        });
        authDeviceIdentity = result.identity;
        authIdentityKey = result.identityKey;
        const payload = result.payload;
        isDeviceUntrusted = !authIdentityKey;
        setRecoveryActionVisible(!!payload.needs_enrollment);
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
            throw SendlyToast.apiError(errorPayload, 'Failed to request device approval');
        } catch (error) {
            console.error('Failed to request enrollment:', error);
            return null;
        }
    }

    async function loadRecentUploads(page = 1) {
        if (!recentSection || !AUTHENTICATED) return;
        const append = page > 1;
        const token = ++recentLoadToken;
        if (append) {
            recentMore?.classList.add('is-loading');
            if (recentMore) recentMore.disabled = true;
        } else {
            setRecentState('loading');
        }

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
            if (token !== recentLoadToken) return;
            renderRecentUploads(payload, append);
            prefetchRecentLockStates(payload?.items || []).catch(() => {});
        } catch (error) {
            if (token !== recentLoadToken) return;
            console.error(error);
            if (append) {
                showErrorBanner(t('state_failed_load'));
            } else {
                setRecentState('error');
            }
        } finally {
            if (token === recentLoadToken && recentMore) {
                recentMore.classList.remove('is-loading');
                recentMore.disabled = false;
            }
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
            if (!authIdentityKey?.privateKeyJWK) {
                throw new Error('This device has no identity key to hand over');
            }

            const requestDevice = activePendingEnrollment.request_device || {};
            const requestPublicKey = requestDevice.public_key_jwk;
            if (!requestPublicKey) {
                throw new Error('Request device public key is missing');
            }

            const wrappedIdKey = await SecureCrypto.wrapIdentityKeyForDevice(authIdentityKey.privateKeyJWK, requestPublicKey);
            const approveBody = {
                approver_device_id: authDeviceIdentity.deviceId,
                verification_code: activePendingEnrollment.enrollment.verification_code,
                wrapped_identity_private_key_b64: SecureCrypto.toBase64(wrappedIdKey),
                identity_key_wrap_alg: 'RSA-OAEP-2048+AES-GCM-256-v1',
                identity_key_wrap_meta: {
                    type: 'enrollment-approval',
                    approver_device_id: authDeviceIdentity.deviceId,
                    request_device_id: requestDevice.id || activePendingEnrollment.enrollment.request_device_id
                },
                identity_key_version: authIdentityKey.keyVersion
            };

            const response = await fetch(`/api/me/devices/enrollments/${encodeURIComponent(activePendingEnrollment.enrollment.id)}/approve`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': getCookieValue('csrf_token')
                },
                body: JSON.stringify(approveBody)
            });

            if (!response.ok) {
                const errorPayload = await response.json().catch(() => ({}));
                throw SendlyToast.apiError(errorPayload, 'Failed to approve device');
            }

            isDeviceUntrusted = false;
            setRecoveryActionVisible(false);
            await loadPendingEnrollments();
            await loadRecentUploads();
        } catch (error) {
            console.error('Approve enrollment failed:', error);
            SendlyToast.fail(error, t('toast_approval_failed'));
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
                throw SendlyToast.apiError(errorPayload, 'Failed to decline device');
            }

            await loadPendingEnrollments();
        } catch (error) {
            console.error('Reject enrollment failed:', error);
            SendlyToast.fail(error, t('toast_decline_failed'));
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
            const payload = await registerCurrentDevice('/api/me/devices/recover');
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
            SendlyToast.fail(error, t('toast_recovery_failed'));
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
            await registerCurrentDevice();
            if (!authIdentityKey) {
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
        // The same socket carries transfer events for the account menu.
        if (!eventType.startsWith('device_enrollment_')) {
            return;
        }
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
        if (recentMore && state !== 'ready') {
            recentMore.classList.add('hidden');
        }
    }

    const FILE_KIND_ICONS = [
        [/^(png|jpe?g|gif|webp|svg|heic|heif|avif|bmp|tiff?|ico)$/, 'file-image'],
        [/^(mp4|mov|mkv|webm|avi|m4v|wmv|flv)$/, 'file-video-camera'],
        [/^(mp3|wav|flac|ogg|m4a|aac|opus|wma)$/, 'file-music'],
        [/^(zip|rar|7z|tar|gz|tgz|bz2|xz|zst|iso|dmg)$/, 'file-archive'],
        [/^(xlsx?|csv|ods|numbers|tsv)$/, 'file-spreadsheet'],
        [/^(js|ts|jsx|tsx|go|py|rb|rs|java|kt|c|cc|cpp|h|cs|php|sh|json|ya?ml|toml|xml|html?|css|sql)$/, 'file-code'],
        [/^(pdf|docx?|odt|rtf|txt|md|pages|pptx?|key|odp|epub)$/, 'file-text'],
    ];

    function fileExtension(name) {
        const match = /\.([a-z0-9]{1,8})$/i.exec(String(name || ''));
        return match ? match[1].toLowerCase() : '';
    }

    function fileKindIcon(ext) {
        const hit = FILE_KIND_ICONS.find(([pattern]) => pattern.test(ext));
        return hit ? hit[1] : 'file';
    }

    function formatExpiryRelative(dateStr) {
        const msLeft = new Date(dateStr) - new Date();
        if (!(msLeft > 0)) return { label: t('format_expired'), soon: true };
        const hours = msLeft / 3600000;
        if (hours < 24) {
            return { label: tpl('uploaded_expires_hours', { n: Math.max(1, Math.ceil(hours)) }), soon: true };
        }
        const days = Math.ceil(hours / 24);
        return {
            label: days === 1 ? tpl('uploaded_expires_days_one', { n: days }) : tpl('uploaded_expires_days', { n: days }),
            soon: days <= 3
        };
    }

    function renderRecentCard(item, index = 0) {
        const locked = recentFileStates.get(item.file_id)?.locked;
        const expiry = formatExpiryRelative(item.expires_at);
        const expiresAbs = new Date(item.expires_at).toLocaleString(PAGE_LOCALE, { dateStyle: 'medium', timeStyle: 'short' });
        const name = escapeHtml(item.filename);
        return `
            <article class="uploaded-card${locked ? ' is-locked' : ''}" style="--i:${Math.min(index, 12)}" data-file-id="${escapeHtml(item.file_id)}" data-file-name="${name}" data-share-url="${escapeHtml(item.share_url)}" data-expires-at="${escapeHtml(item.expires_at)}">
                <div class="uploaded-card-top">
                    <div class="uploaded-file-icon" aria-hidden="true"><i data-lucide="${fileKindIcon(fileExtension(item.filename))}"></i></div>
                    <div class="uploaded-file-info">
                        <p class="uploaded-file-name" title="${name}">${name}</p>
                        <p class="uploaded-file-meta">
                            <span>${SecureCrypto.formatFileSize(item.size_bytes)}</span>
                            <span class="uploaded-meta-dot" aria-hidden="true"></span>
                            <span class="uploaded-meta-date">${escapeHtml(formatUploadDate(item.created_at))}</span>
                            <span class="uploaded-meta-dot" aria-hidden="true"></span>
                            <span class="uploaded-expiry${expiry.soon ? ' is-soon' : ''}" title="${escapeHtml(t('label_expires') + expiresAbs)}">${escapeHtml(expiry.label)}</span>
                            <span class="uploaded-locked" title="${escapeHtml(LOCKED_FILE_INFO)}">${t('uploaded_locked')}</span>
                            <span class="uploaded-progress-text" aria-live="polite"></span>
                        </p>
                    </div>
                    <div class="uploaded-actions">
                        <button type="button" class="uploaded-action" data-action="copy" aria-label="${t('label_copy_share')}" title="${t('label_copy_share')}" ${locked ? 'disabled' : ''}>
                            <i data-lucide="link" class="uploaded-ic" aria-hidden="true"></i><i data-lucide="check" class="uploaded-ic-done" aria-hidden="true"></i>
                        </button>
                        <button type="button" class="uploaded-action uploaded-action-primary" data-action="download" aria-label="${t('label_download_file')}" title="${t('label_download_file')}" ${locked ? 'disabled' : ''}>
                            <i data-lucide="download" class="uploaded-ic" aria-hidden="true"></i>
                        </button>
                    </div>
                </div>
                <div class="download-bar-track"><div class="download-bar-fill"></div></div>
            </article>
        `;
    }

    function renderRecentUploads(payload, append = false) {
        if (!recentList) return;
        const items = payload?.items || [];
        recentCurrentPage = payload?.page || 1;
        recentTotalPages = payload?.total_pages || 0;
        const totalItems = payload?.total || 0;

        if (recentCount) {
            recentCount.textContent = `${totalItems} ${t(totalItems === 1 ? 'label_file' : 'label_files')}`;
            recentCount.classList.toggle('hidden', !totalItems && !recentSearchQuery);
        }

        if (!append && !items.length) {
            setRecentState('empty');
            const emptyTitle = document.getElementById('recent-empty-title');
            const emptyDesc = document.getElementById('recent-empty-desc');
            const emptyCta = document.getElementById('recent-empty-cta');
            if (emptyTitle) emptyTitle.textContent = recentSearchQuery ? t('state_no_search_results') : t('uploaded_empty_title');
            if (emptyDesc) emptyDesc.classList.toggle('hidden', !!recentSearchQuery);
            if (emptyCta) emptyCta.classList.toggle('hidden', !!recentSearchQuery);
            return;
        }

        setRecentState('ready');
        const html = items.map((item, index) => renderRecentCard(item, index)).join('');
        if (append) {
            recentList.insertAdjacentHTML('beforeend', html);
        } else {
            recentList.innerHTML = html;
        }
        if (window.lucide?.createIcons) {
            window.lucide.createIcons();
        }
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
            SendlyToast.fail(error, t('toast_tunnel_download_failed'));
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
                authDeviceIdentity = await SecureCrypto.getOrCreateDeviceIdentity(CNS_USER_ID);
            } catch (error) {
                SendlyToast.fail(error, t('toast_device_identity_failed'));
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
            throw SendlyToast.apiError(error, 'Failed to start tunnel');
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
                authDeviceIdentity = await SecureCrypto.getOrCreateDeviceIdentity(CNS_USER_ID);
            } catch (error) {
                SendlyToast.fail(error, t('toast_device_identity_failed'));
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
            throw SendlyToast.apiError(error, 'Failed to join tunnel');
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
            throw SendlyToast.apiError(error, 'Failed to confirm tunnel');
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
            throw SendlyToast.apiError(error, 'Failed to end tunnel');
        }

        clearTunnelState();
        showInfoBanner(t('toast_tunnel_ended'));
    }

    async function prefetchRecentLockStates(items) {
        if (!Array.isArray(items) || !items.length) {
            return;
        }
        // Let the initial device registration finish first, otherwise every item
        // would kick off its own registration request.
        await initialDeviceReady;
        if (isDeviceUntrusted) return;

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
        if (!recentMore) return;
        recentMore.classList.toggle('hidden', recentCurrentPage >= recentTotalPages);
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
        const button = event.target.closest('.uploaded-action');
        if (!button || button.disabled) return;
        const item = button.closest('.uploaded-card');
        if (!item || item.classList.contains('is-locked')) return;

        const fileId = item.dataset.fileId;
        const fileName = item.dataset.fileName;
        const action = button.dataset.action;

        try {
            if (action === 'download') {
                await downloadOwnedFile(fileId, fileName, '', item);
            } else if (action === 'copy') {
                await copyRecentShareLink(item, button);
            }
        } catch (error) {
            console.error(error);
            if (isLockedFileError(error)) {
                markRecentFileLocked(fileId, error.message);
                SendlyToast.error(t('toast_file_locked'));
                return;
            }

            SendlyToast.fail(error, t('toast_action_failed'));
        }
    }

    async function copyRecentShareLink(item, button) {
        const fileId = item.dataset.fileId;
        const shareUrl = item.dataset.shareUrl;
        if (!fileId || !shareUrl) return;

        // Clipboard writes must happen close to the click. If the key is not cached yet,
        // hand the clipboard a promise (Safari/Chrome) instead of awaiting network first.
        const linkPromise = getOwnedFilePassphrase(fileId).then((passphrase) => `${shareUrl}#${passphrase}`);
        let copied = false;
        if (!SecureCrypto.getCachedFileKey(fileId) && window.ClipboardItem && navigator.clipboard?.write) {
            try {
                await navigator.clipboard.write([
                    new ClipboardItem({ 'text/plain': linkPromise.then((link) => new Blob([link], { type: 'text/plain' })) })
                ]);
                copied = true;
            } catch (error) {
                await linkPromise;
            }
        }
        if (!copied) {
            copied = await copyToClipboard(await linkPromise, true);
        }
        if (!copied) {
            showToast(t('toast_copy_failed'));
            return;
        }

        showToast(t('toast_link_copied'));
        clearTimeout(button._copiedTimer);
        button.classList.add('is-copied');
        button._copiedTimer = setTimeout(() => button.classList.remove('is-copied'), 1800);
    }

    function setRecentCardProgress(cardEl, state, pct = 0, text = '') {
        cardEl.classList.remove('active', 'done', 'error');
        if (state) cardEl.classList.add(state);
        const fill = cardEl.querySelector('.download-bar-fill');
        if (fill) fill.style.width = `${Math.min(100, Math.max(0, pct))}%`;
        const textEl = cardEl.querySelector('.uploaded-progress-text');
        if (textEl) textEl.textContent = text;
    }

    async function downloadOwnedFile(fileId, fileName, tunnelId = '', cardEl = null) {
        if (activeDownloads.has(fileId)) return;
        const isUploadedCard = !!cardEl?.classList.contains('uploaded-card');

        let progressFill = null;
        let downloadBtn = null;
        let originalBtnHtml = '';

        activeDownloads.add(fileId);
        if (isUploadedCard) {
            downloadBtn = cardEl.querySelector('.uploaded-action[data-action="download"]');
            if (downloadBtn) downloadBtn.disabled = true;
            setRecentCardProgress(cardEl, 'active', 0, t('status_downloading'));
        } else if (cardEl) {
            const progressBar = document.createElement('div');
            progressBar.className = 'file-download-progress';
            progressFill = document.createElement('div');
            progressFill.className = 'file-download-progress-bar';
            progressBar.appendChild(progressFill);
            cardEl.appendChild(progressBar);

            downloadBtn = cardEl.querySelector('.recent-action[data-action="download"]');
            if (downloadBtn && !downloadBtn.disabled) {
                originalBtnHtml = downloadBtn.innerHTML;
                downloadBtn.innerHTML = '<div class="downloading-spinner" style="width:16px;height:16px;border-width:2px;"></div>';
                downloadBtn.disabled = true;
            }
        }

        const updateProgress = (pct, text) => {
            if (isUploadedCard) {
                setRecentCardProgress(cardEl, 'active', pct, `${text} ${Math.floor(pct)}%`);
            } else if (progressFill) {
                progressFill.style.width = `${Math.min(100, Math.max(0, pct))}%`;
            }
        };

        let succeeded = false;
        try {
            const passphrase = await getOwnedFilePassphrase(fileId, tunnelId);
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
                    updateProgress((received / total) * 80, t('status_downloading'));
                }
            }

            const encryptedBlob = new Blob(chunks);
            let decrypted;
            try {
                decrypted = await SecureCrypto.decryptBlob(encryptedBlob, passphrase, (progress) => {
                    updateProgress(80 + progress * 0.2, t('status_decrypting'));
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
            setTimeout(() => URL.revokeObjectURL(url), 10000);

            succeeded = true;
            if (isUploadedCard) {
                setRecentCardProgress(cardEl, 'done', 100, t('shared_complete'));
            } else {
                updateProgress(100);
            }
            await new Promise((resolve) => setTimeout(resolve, isUploadedCard ? 2200 : 600));
        } finally {
            activeDownloads.delete(fileId);
            if (isUploadedCard) {
                if (succeeded || !cardEl.classList.contains('is-locked')) {
                    setRecentCardProgress(cardEl, succeeded ? '' : 'error', 0, succeeded ? '' : t('toast_download_failed'));
                }
                if (!succeeded) {
                    setTimeout(() => {
                        if (cardEl.classList.contains('error')) setRecentCardProgress(cardEl, '', 0, '');
                    }, 4000);
                }
                if (downloadBtn && !cardEl.classList.contains('is-locked')) downloadBtn.disabled = false;
            }
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

    function lockedFileError() {
        const error = new Error('This file is locked. This could happen if you recovered your account after you uploaded this file.');
        error.code = 'FILE_LOCKED';
        return error;
    }

    // Returns a file's passphrase: the signed-in owner's (or an accepted
    // transfer's) copy, opened with this device's identity key, or, for a
    // quick share guest's upload, the copy wrapped for this page's throwaway key.
    async function getOwnedFilePassphrase(fileId, tunnelId = '') {
        const cached = SecureCrypto.getCachedFileKey(fileId);
        if (cached) {
            return cached;
        }

        if (initialDeviceReady) {
            await initialDeviceReady;
        }

        if (tunnelId) {
            const response = await fetch(`/api/tunnels/${encodeURIComponent(tunnelId)}/files/${encodeURIComponent(fileId)}/access`, {
                headers: { 'X-CSRF-Token': getCookieValue('csrf_token') }
            });
            if (!response.ok) {
                const errorPayload = await response.json().catch(() => ({}));
                throw SendlyToast.apiError(errorPayload, 'Unable to access decryption key for this file.');
            }
            const payload = await response.json();
            const dekBytes = await SecureCrypto.unwrapFileDEK(payload.file_key_envelope, {
                authenticated: false,
                ephemeralPrivateKey: ephemeralKeyPair?.privateKey
            });
            const passphrase = new TextDecoder().decode(dekBytes);
            SecureCrypto.cacheFileKey(fileId, passphrase);
            return passphrase;
        }

        if (!AUTHENTICATED) {
            throw new Error('A tunnel is required to access this file.');
        }
        if (!authIdentityKey) {
            const ready = await ensureDeviceReady();
            if (!ready) {
                throw new Error('Approve this device from a trusted device to access your files.');
            }
        }

        const response = await fetch(`/api/me/files/${encodeURIComponent(fileId)}/access`, {
            headers: { 'X-CSRF-Token': getCookieValue('csrf_token') }
        });
        if (response.status === 404) {
            // Listed as ours, but there is no key for our identity: it was
            // wrapped before the account's last recovery or migration.
            throw lockedFileError();
        }
        if (!response.ok) {
            const errorPayload = await response.json().catch(() => ({}));
            throw SendlyToast.apiError(errorPayload, 'Unable to access decryption key for this file.');
        }

        const payload = await response.json();
        if (payload.identity_key_version !== authIdentityKey.keyVersion) {
            throw lockedFileError();
        }
        let dekBytes;
        try {
            dekBytes = await SecureCrypto.unwrapFileDEK(payload.file_access_key_envelope, {
                authenticated: true,
                identityPrivateKeyJWK: authIdentityKey.privateKeyJWK
            });
        } catch (error) {
            throw lockedFileError();
        }
        const passphrase = new TextDecoder().decode(dekBytes);
        SecureCrypto.cacheFileKey(fileId, passphrase);
        return passphrase;
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
        const item = recentList?.querySelector(`.uploaded-card[data-file-id="${CSS.escape(fileId)}"]`);
        if (!item) return;

        item.classList.add('is-locked');
        item.setAttribute('title', LOCKED_FILE_INFO);
        item.querySelectorAll('.uploaded-action').forEach((btn) => {
            btn.disabled = true;
        });
    }

    function formatUploadDate(dateStr) {
        const date = new Date(dateStr);
        const now = new Date();
        const todayStart = new Date(now.getFullYear(), now.getMonth(), now.getDate());
        const dateStart = new Date(date.getFullYear(), date.getMonth(), date.getDate());
        const dayDiff = Math.round((dateStart - todayStart) / 86400000);
        const time = date.toLocaleTimeString(PAGE_LOCALE, { hour: '2-digit', minute: '2-digit' });

        if (dayDiff === 0) return `${t('format_today')} ${time}`;
        if (dayDiff === -1) return `${t('format_yesterday')} ${time}`;
        return date.toLocaleString(PAGE_LOCALE, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
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

        recentSearchInput?.addEventListener('input', handleRecentSearchInput);
        recentList?.addEventListener('click', handleRecentAction);
        recentMore?.addEventListener('click', () => {
            if (recentCurrentPage < recentTotalPages) {
                loadRecentUploads(recentCurrentPage + 1);
            }
        });
        recentRetry?.addEventListener('click', () => loadRecentUploads(1));

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
                SendlyToast.fail(error, t('toast_tunnel_failed_start'));
            }
        });
        tunnelJoinBtn?.addEventListener('click', async () => {
            try {
                await handleJoinTunnel();
            } catch (error) {
                SendlyToast.fail(error, t('toast_tunnel_failed_join'));
            }
        });
        tunnelConfirmBtn?.addEventListener('click', async () => {
            try {
                await handleConfirmTunnel();
                await refreshTunnelState();
            } catch (error) {
                SendlyToast.fail(error, t('toast_tunnel_failed_confirm'));
            }
        });
        tunnelEndBtn?.addEventListener('click', async () => {
            try {
                await handleEndTunnel();
            } catch (error) {
                SendlyToast.fail(error, t('toast_tunnel_failed_end'));
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
            SendlyToast.fail(uploadError, t('toast_upload_failed'));
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
                    SendlyToast.fail(uploadError, t('toast_upload_failed'));
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
                if (!authIdentityKey) {
                    await ensureDeviceReady();
                }
                if (!authIdentityKey) {
                    throw new Error('Approve this device from a trusted device before uploading as an authenticated user');
                }
                finalizeEnvelopePayload = await SecureCrypto.buildOwnerEnvelope(dekBytes, authIdentityKey);
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
            SendlyToast.fail(error, t('toast_upload_failed'));
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
            SendlyToast.fail(error, t('toast_something_failed'));
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
                    throw SendlyToast.apiError(error, `Failed to upload chunk ${chunkIndex + 1}`);
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
            throw SendlyToast.apiError(error, 'Failed to initialize upload');
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
            throw SendlyToast.apiError(error, 'Failed to complete upload');
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
                throw SendlyToast.apiError(error, 'Failed to finalize upload');
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
            SendlyToast.fail(error, t('toast_finalize_failed'));
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
        SendlyToast.show(message, { type });
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


    function updateProgress(percent, sub, main) {
        const boundedPercent = Math.floor(Math.min(100, Math.max(0, percent)));
        progressVal.textContent = `${boundedPercent}%`;
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
        SendlyToast.success(message);
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
        (recentDeviceNotice || recentRecoverDevice)?.classList.toggle('hidden', !(visible && isDeviceUntrusted));
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