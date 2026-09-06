(function() {
    'use strict';

    const t = (k, d) => window.CONFIG?.t?.[k] || d || k;
    const tpl = (k, vars) => { let s = t(k); if (vars) for (const [key, val] of Object.entries(vars)) s = s.replace(`{${key}}`, val); return s; };

    // Function to generate avatar initials as fallback when image fails to load
    window.buildInitialsAvatar = function(username) {
        if (!username) return document.createElement('span');
        var initials = username.trim().split(/\s+/).map(function(w) { return w[0]; }).join('').toUpperCase().slice(0, 2);
        // Deterministic color from username - WCAG AA compliant (≥4.5:1 against white)
        var hash = 0;
        for (var i = 0; i < username.length; i++) {
            hash = username.charCodeAt(i) + ((hash << 5) - hash);
        }
        var palette = [
            '#004AAD', '#006B3F', '#8B0000', '#6B3A00', '#4B0082',
            '#800040', '#005A7A', '#2E6B2E', '#6B4000', '#4A006B'
        ];
        var color = palette[Math.abs(hash) % palette.length];
        var span = document.createElement('span');
        span.textContent = initials;
        span.style.cssText = 'display:flex;align-items:center;justify-content:center;width:24px;height:24px;border-radius:50%;font-size:11px;font-weight:600;color:#fff;background:' + color + ';';
        return span;
    }

    // Hydrate initials placeholders on page load
    document.addEventListener('DOMContentLoaded', function() {
        document.querySelectorAll('.account-menu-avatar-placeholder').forEach(function(el) {
            var username = el.closest('[data-username]')?.dataset.username || '';
            if (username) {
                var avatar = buildInitialsAvatar(username);
                el.replaceWith(avatar);
            }
        });
    });


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