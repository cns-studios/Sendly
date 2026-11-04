(function() {
    'use strict';

    const t = (k, d) => window.CONFIG?.t?.[k] || d || k;
    const tpl = (k, vars) => { let s = t(k); if (vars) for (const [key, val] of Object.entries(vars)) s = s.replace(`{${key}}`, val); return s; };

     
    const AUTHENTICATED = window.CONFIG?.authenticated || false;
    const CNS_USER_ID = window.CONFIG?.cnsUserId || 0;
    const CNS_USERNAME = window.CONFIG?.cnsUsername || '';
    const TOS_VERSION = window.CONFIG?.tosVersion || '2026-09-25';
    const TOS_COOKIE_NAME = 'sendly_tos_accepted';
    const RECENT_UPLOADS_PER_PAGE = 10;
    const RECENT_SEARCH_DEBOUNCE_MS = 180;
    const PAGE_LOCALE = document.documentElement.lang || undefined;

    let authDeviceIdentity = null;
    let authIdentityKey = null;
    let recentSearchQuery = '';
    let recentCurrentPage = 1;
    let recentTotalPages = 0;
    let recentLoadToken = 0;
    let recentSearchDebounceTimer = null;

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
    const deviceApprovalModal = document.getElementById('device-approval-modal');
    const deviceApprovalTitle = document.getElementById('device-approval-title');
    const deviceApprovalMessage = document.getElementById('device-approval-message');
    const deviceApprovalMeta = document.getElementById('device-approval-meta');
    const deviceApprovalCount = document.getElementById('device-approval-count');
    const deviceApprovalWaiting = document.getElementById('device-approval-waiting');
    const deviceApprovalDecline = document.getElementById('device-approval-decline');
    const deviceApprovalApprove = document.getElementById('device-approval-approve');
    const deviceApprovalRecover = document.getElementById('device-approval-recover');
    const deviceApprovalClose = document.getElementById('device-approval-close');
    const tosOverlay = document.getElementById('tos-overlay');
    const tosAcceptBtn = document.getElementById('tos-accept-btn');
    const tosDeclineBtn = document.getElementById('tos-decline-btn');
    const actionModal = document.getElementById('action-modal');
    const actionModalTitle = document.getElementById('action-modal-title');
    const actionModalDescription = document.getElementById('action-modal-description');
    const actionModalCancel = document.getElementById('action-modal-cancel');
    const actionModalConfirm = document.getElementById('action-modal-confirm');

    let pendingEnrollmentItems = [];
    let activePendingEnrollment = null;
    let pendingEnrollmentBusy = false;
    let pendingEnrollmentMode = 'idle';
    // closed by the user, so they can use the rest of the page (log out, say). the waiting
    // modal stays closed until something that needs this device trusted opens it again; a
    // closed request from another device stays closed until a new one arrives.
    let waitingModalDismissed = false;
    const dismissedApprovalIds = new Set();
    let pendingEnrollmentSocket = null;
    let pendingEnrollmentSocketRetryTimer = null;
    let pendingEnrollmentRefreshTimer = null;
    let pendingEnrollmentSocketEverOpened = false;
    let isDeviceUntrusted = false;
    // the account predates identity keys and this device can't migrate it:
    // it waits for approval from an older device, which migrates the account
    // when it next opens sendly.
    let awaitingLegacyApproval = false;
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
            window.location.href = 'https://cns.ad';
        });

        return false;
    }

    async function init() {
        setupTOSGate();
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

    async function ensureDeviceReady({ userAction = false } = {}) {
        if (userAction) waitingModalDismissed = false;
        try {
        const payload = await registerCurrentDevice();
        awaitingLegacyApproval = !!payload?.needs_identity_migration && !authIdentityKey;
        if (payload?.needs_enrollment || awaitingLegacyApproval) {
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
        setRecoveryActionVisible(!!payload.needs_enrollment || (!!payload.needs_identity_migration && !authIdentityKey));
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

            const approvalItems = (currentDeviceId
                ? pendingEnrollmentItems.filter((item) => {
                    const requestDeviceId = item?.request_device?.id || item?.enrollment?.request_device_id || '';
                    return requestDeviceId !== currentDeviceId;
                })
                : pendingEnrollmentItems).filter((item) => !dismissedApprovalIds.has(item?.enrollment?.id));

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

    function renderDeviceApprovalMeta(rows) {
        if (!deviceApprovalMeta) return;
        deviceApprovalMeta.innerHTML = rows.map(([label, value]) =>
            `<div class="device-approval-row"><dt>${escapeHtml(String(label).replace(/\s*:\s*$/, ''))}</dt><dd>${escapeHtml(value)}</dd></div>`
        ).join('');
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
        renderDeviceApprovalMeta([
            [t('device_connect_device'), deviceName],
            [t('device_connect_device_id'), deviceId],
            [t('device_connect_key'), keyAlgorithm],
            [t('device_connect_requested'), requestedAt]
        ]);

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
        // still waiting (and finishing once approved), just not in the user's way.
        if (waitingModalDismissed) return;
        if (!deviceApprovalTitle || !deviceApprovalMessage || !deviceApprovalMeta || !deviceApprovalModal) {
            return;
        }

        const device = item?.request_device || {};
        const enrollment = item?.enrollment || {};
        const deviceName = device.device_label || device.id || 'This device';
        const deviceId = device.id || enrollment.request_device_id || 'unknown';
        const requestedAt = enrollment.created_at ? formatUploadDate(enrollment.created_at) : 'just now';

        deviceApprovalTitle.textContent = t('device_pending_title');
        deviceApprovalMessage.textContent = t(awaitingLegacyApproval ? 'device_pending_message_legacy' : 'device_pending_message');
        renderDeviceApprovalMeta([
            [t('device_connect_device'), deviceName],
            [t('device_connect_device_id'), deviceId],
            [t('device_connect_requested'), requestedAt]
        ]);

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

    function dismissDeviceApprovalModal() {
        if (!deviceApprovalModal || deviceApprovalModal.classList.contains('hidden') || pendingEnrollmentBusy) return;
        if (pendingEnrollmentMode === 'waiting') {
            waitingModalDismissed = true;
        } else if (activePendingEnrollment?.enrollment?.id) {
            dismissedApprovalIds.add(activePendingEnrollment.enrollment.id);
            pendingEnrollmentMode = 'idle';
            activePendingEnrollment = null;
        }
        deviceApprovalModal.classList.add('hidden');
        deviceApprovalModal.setAttribute('aria-hidden', 'true');
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
            // A reconnect (the server restarted or moved): events sent while
            // the socket was down are gone, so reload the current state.
            if (pendingEnrollmentSocketEverOpened) {
                loadPendingEnrollments();
            }
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
        if (eventType === 'signed_out') {
            // the account was disconnected from sendly in CNS. the cookies are
            // dead already; a reload shows the signed-out page.
            window.location.reload();
            return;
        }
        // the same socket carries transfer events for the account menu.
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
            <article class="uploaded-card${locked ? ' is-locked' : ''}" style="--i:${Math.min(index, 12)}" data-locked-tip="${escapeHtml(t('uploaded_locked_tooltip'))}" data-file-id="${escapeHtml(item.file_id)}" data-file-name="${name}" data-share-url="${escapeHtml(item.share_url)}" data-expires-at="${escapeHtml(item.expires_at)}">
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
                            <span class="uploaded-locked" tabindex="0" aria-label="${escapeHtml(t('uploaded_locked') + ': ' + t('uploaded_locked_tooltip'))}">${t('uploaded_locked')}<i data-lucide="info" aria-hidden="true"></i></span>
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

    async function prefetchRecentLockStates(items) {
        if (!Array.isArray(items) || !items.length) {
            return;
        }
        // let the initial device registration finish first, otherwise every item
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
                await downloadOwnedFile(fileId, fileName, item);
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

        // clipboard writes must happen close to the click. if the key is not cached yet,
        // hand the clipboard a promise (safari/chrome) instead of awaiting network first.
        const linkPromise = getOwnedFilePassphrase(fileId).then((secret) => SecureCrypto.shareLinkFor(fileId, shareUrl, secret));
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
            copied = await copyToClipboard(await linkPromise);
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

    async function downloadOwnedFile(fileId, fileName, cardEl) {
        if (activeDownloads.has(fileId)) return;

        activeDownloads.add(fileId);
        const downloadBtn = cardEl.querySelector('.uploaded-action[data-action="download"]');
        if (downloadBtn) downloadBtn.disabled = true;
        setRecentCardProgress(cardEl, 'active', 0, t('status_downloading'));

        const updateProgress = (pct, text) => {
            setRecentCardProgress(cardEl, 'active', pct, `${text} ${Math.floor(pct)}%`);
        };

        let succeeded = false;
        try {
            const passphrase = await getOwnedFilePassphrase(fileId);
            const response = await SendlyNet.fetch(`/api/file/${fileId}/download`);
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
            setRecentCardProgress(cardEl, 'done', 100, t('shared_complete'));
            await new Promise((resolve) => setTimeout(resolve, 2200));
        } finally {
            activeDownloads.delete(fileId);
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
    }

    function lockedFileError() {
        const error = new Error('This file is locked. This could happen if you recovered your account after you uploaded this file.');
        error.code = 'FILE_LOCKED';
        return error;
    }

    // returns a file's passphrase: the signed-in owner's (or an accepted
    // transfer's) copy, opened with this device's identity key.
    async function getOwnedFilePassphrase(fileId) {
        const cached = SecureCrypto.getCachedFileKey(fileId);
        if (cached) {
            return cached;
        }

        if (initialDeviceReady) {
            await initialDeviceReady;
        }

        if (!AUTHENTICATED) {
            throw new Error('Sign in to access this file.');
        }
        if (!authIdentityKey) {
            const ready = await ensureDeviceReady({ userAction: true });
            if (!ready) {
                throw new Error('Approve this device from a trusted device to access your files.');
            }
        }

        const response = await fetch(`/api/me/files/${encodeURIComponent(fileId)}/access`, {
            headers: { 'X-CSRF-Token': getCookieValue('csrf_token') }
        });
        if (response.status === 404) {
            // listed as ours, but there is no key for our identity: it was
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

    function escapeHtml(value) {
        return String(value || '')
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#39;');
    }

     

    function setupEventListeners() {
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
        deviceApprovalClose?.addEventListener('click', dismissDeviceApprovalModal);
        deviceApprovalModal?.addEventListener('click', (event) => {
            if (event.target === deviceApprovalModal) dismissDeviceApprovalModal();
        });
        document.addEventListener('keydown', (event) => {
            if (event.key === 'Escape') dismissDeviceApprovalModal();
        });
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


    async function copyToClipboard(text) {
        try {
            await navigator.clipboard.writeText(text);
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

            return copiedWithFallback;
        }
    }

    function showToast(message) {
        SendlyToast.success(message);
    }

    function setRecoveryActionVisible(visible) {
        (recentDeviceNotice || recentRecoverDevice)?.classList.toggle('hidden', !(visible && isDeviceUntrusted));
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