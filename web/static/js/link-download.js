(function() {
    'use strict';

    const t = (k, d) => window.CONFIG?.t?.[k] || d || k;
    const tpl = (k, vars) => { let s = t(k); if (vars) for (const [key, val] of Object.entries(vars)) s = s.replace(`{${key}}`, val); return s; };

    const fileId = window.CONFIG?.fileID;
    let fileMetadata = null;
    let currentPassword = null;

    const downloadState = {
        promise: null,
        blob: null,
        chunks: [],
        received: 0,
        total: 0
    };
    const DOWNLOAD_CHUNK_SIZE = 32 * 1024 * 1024;
    const DOWNLOAD_CONCURRENCY = 4;
    const MAX_CHUNK_DOWNLOAD_RETRIES = 5;
    let isProcessing = false;
    let downloadStartedAt = 0;

    const loadingSection = document.getElementById('loading-section');
    const passwordSection = document.getElementById('password-section');
    const autoDecryptSection = document.getElementById('auto-decrypt-section');
    const noPasswordSection = document.getElementById('no-password-section');
    const progressSection = document.getElementById('progress-section');

    const fileNameEl = document.getElementById('file-name');
    const fileSizeEl = document.getElementById('file-size');
    const fileCreatedEl = document.getElementById('file-created');
    const fileExpiresEl = document.getElementById('file-expires');

    const downloadAutoBtn = document.getElementById('download-auto-btn');

    const progressTitle = document.getElementById('progress-title');
    const progressText = document.getElementById('progress-text');

    const reportBtn = document.getElementById('report-btn');
    const reportModal = document.getElementById('report-modal');
    const reportCancel = document.getElementById('report-cancel');
    const reportConfirm = document.getElementById('report-confirm');
    const downloadCard = document.getElementById('download-card');
    const downloadPercent = document.getElementById('download-percent');
    const downloadBarFill = document.getElementById('download-bar-fill');
    const downloadStatusRow = document.getElementById('download-status-row');

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
        return getCookieValue('sendly_tos_accepted') === (window.CONFIG?.tosVersion || '2026-09-25');
    }

    function setupTOSGate() {
        if (!tosOverlay) return;
        if (hasAcceptedCurrentTOS()) { tosOverlay.classList.add('hidden'); return; }
        tosOverlay.classList.remove('hidden');
        tosAcceptBtn?.addEventListener('click', () => { setCookie('sendly_tos_accepted', window.CONFIG?.tosVersion || '2026-09-25', 31536000); tosOverlay.classList.add('hidden'); });
        tosDeclineBtn?.addEventListener('click', () => { window.location.href = 'https://cns-studios.com'; });
    }

    function setupEventListeners() {
        downloadAutoBtn?.addEventListener('click', () => downloadAndDecrypt(currentPassword));
        reportBtn?.addEventListener('click', () => { reportModal.classList.remove('hidden'); });
        reportCancel?.addEventListener('click', () => { reportModal.classList.add('hidden'); });
        reportConfirm?.addEventListener('click', submitReport);
        reportModal?.addEventListener('click', (e) => { if (e.target === reportModal) reportModal.classList.add('hidden'); });
    }

    async function loadFileMetadata(fileID) {
        try {
            const response = await fetch(`/api/file/${fileID}`);
            if (!response.ok) {
                const error = await response.json();
                handleAPIError(error);
                return;
            }

            fileMetadata = await response.json();
            displayFileMetadata();
            resetDownloadCard();

            const hashPassword = SecureCrypto.getPasswordFromHash();
            if (hashPassword) {
                const validation = SecureCrypto.validatePassword(hashPassword);
                if (validation.valid) {
                    currentPassword = hashPassword;
                    autoDecryptSection.classList.remove('hidden');
                    if (noPasswordSection) noPasswordSection.classList.add('hidden');
                }
            } else {
                if (noPasswordSection) noPasswordSection.classList.remove('hidden');
            }

            loadingSection.classList.add('hidden');
            passwordSection.classList.remove('hidden');

        } catch (error) {
            console.error('Failed to load file metadata:', error);
            showFileError('cloud-alert', t('error_connection_title'), t('error_connection_desc'));
        }
    }

    function displayFileMetadata() {
        fileNameEl.textContent = fileMetadata.original_name;
        fileSizeEl.textContent = SecureCrypto.formatFileSize(fileMetadata.size_bytes);
        const uploadedDate = new Date(fileMetadata.created_at).toLocaleDateString();
        fileCreatedEl.textContent = ` · ${t('shared_uploaded')} ${uploadedDate}`;
        fileExpiresEl.textContent = SecureCrypto.getTimeRemaining(fileMetadata.expires_at);
    }

    function updateDownloadCard(percent, status, state = 'active') {
        if (!downloadCard) return;
        downloadCard.classList.remove('active', 'done', 'error');
        if (state) downloadCard.classList.add(state);
        if (downloadPercent) downloadPercent.textContent = `${Math.round(percent)}%`;
        if (downloadBarFill) downloadBarFill.style.width = `${percent}%`;
        if (downloadStatusRow) {
            const label = downloadStatusRow.querySelector('span');
            if (label) label.textContent = status;
        }
    }

    function resetDownloadCard() {
        downloadCard?.classList.remove('active', 'done', 'error');
        if (downloadPercent) downloadPercent.textContent = '';
        if (downloadBarFill) downloadBarFill.style.width = '0%';
        if (downloadStatusRow) {
            const label = downloadStatusRow.querySelector('span');
            if (label) label.textContent = t('shared_ready');
        }
    }

    async function downloadAndDecrypt(password) {
        if (isProcessing) return;
        isProcessing = true;

        const validation = SecureCrypto.validatePassword(password);
        if (!validation.valid) {
            isProcessing = false;
            showNotification(validation.error, 'error');
            return;
        }

        currentPassword = password;

        loadingSection.classList.add('hidden');
        autoDecryptSection.classList.add('hidden');
        progressSection.classList.add('hidden');

        await new Promise((resolve) => requestAnimationFrame(resolve));

        try {
            downloadStartedAt = Date.now();
            updateDownloadCard(0, t('shared_downloading_status'));
            const iconEl = document.getElementById('progress-icon');
            if (iconEl) {
                iconEl.innerHTML = '';
                iconEl.appendChild(createProgressCircle(36));
            }

            updateProgress(getEncryptedDownloadProgress(), t('status_downloading'));
            const encryptedBlob = await downloadEncryptedFile();

            updateProgress(80, t('status_downloading'));
            const decryptedData = await SecureCrypto.decryptFileChunked(
                encryptedBlob,
                password,
                fileMetadata.size_bytes,
                (progress) => {
                    updateProgress(80 + (progress * 0.2), t('status_downloading'));
                }
            );

            triggerDownload(decryptedData, fileMetadata.original_name);

            updateDownloadCard(100, t('shared_complete'), 'done');
            showDownloadedState();

            await new Promise((resolve) => setTimeout(resolve, 3000));

            resetDownloadBox();
            if (currentPassword) {
                autoDecryptSection.classList.remove('hidden');
            }
        } catch (error) {
            console.error('Download/decrypt failed:', error);
            updateDownloadCard(0, t('shared_retry'), 'error');

            showNotification(t('toast_download_failed'), 'error');
            if (currentPassword) {
                autoDecryptSection.classList.remove('hidden');
            }
        } finally {
            isProcessing = false;
        }
    }

    function getEncryptedDownloadProgress() {
        if (downloadState.blob) return 80;
        if (!downloadState.total) return 0;
        return Math.min(80, (downloadState.received / downloadState.total) * 80);
    }

    async function downloadEncryptedFile() {
        if (downloadState.blob) return downloadState.blob;
        if (!downloadState.promise) {
            downloadState.promise = streamEncryptedFile();
        }
        try {
            await downloadState.promise;
            return downloadState.blob;
        } catch (error) {
            downloadState.promise = null;
            downloadState.chunks = [];
            downloadState.received = 0;
            downloadState.total = 0;
            throw error;
        }
    }

    async function streamEncryptedFile() {
        if (!downloadStartedAt) downloadStartedAt = Date.now();
        const firstEnd = DOWNLOAD_CHUNK_SIZE - 1;
        const firstResponse = await fetch(`/api/file/${fileMetadata.id}/download`, {
            headers: { Range: `bytes=0-${firstEnd}` }
        });
        if (!firstResponse.ok) {
            const error = await firstResponse.json().catch(() => ({}));
            throw new Error(error.error || t('toast_download_failed'));
        }

        const contentRange = firstResponse.headers.get('Content-Range') || '';
        const rangeMatch = contentRange.match(/bytes\s+\d+-\d+\/(\d+)/i);
        const total = rangeMatch ? Number(rangeMatch[1]) : Number(firstResponse.headers.get('Content-Length'));
        if (!Number.isFinite(total) || total <= 0) throw new Error(t('toast_download_failed'));

        downloadState.total = total;
        downloadState.received = 0;
        const chunkCount = Math.ceil(total / DOWNLOAD_CHUNK_SIZE);
        downloadState.chunks = new Array(chunkCount);
        if (firstResponse.status === 200) {
            const data = new Uint8Array(await firstResponse.arrayBuffer());
            if (data.length !== total) throw new Error(t('toast_download_failed'));
            downloadState.chunks = [data];
            downloadState.received = data.length;
            updateDownloadCard(100, t('shared_downloading_status'));
            downloadState.blob = new Blob(downloadState.chunks);
            return downloadState.blob;
        }

        const firstChunk = new Uint8Array(await firstResponse.arrayBuffer());
        const expectedFirstLength = Math.min(DOWNLOAD_CHUNK_SIZE, total);
        if (firstChunk.length !== expectedFirstLength) throw new Error(t('toast_download_failed'));
        downloadState.chunks[0] = firstChunk;
        downloadState.received = firstChunk.length;
        updateDownloadCard((downloadState.received / total) * 100, t('shared_downloading_status'));
        let nextChunk = 1;

        async function worker() {
            while (true) {
                const index = nextChunk++;
                if (index >= chunkCount) return;
                const start = index * DOWNLOAD_CHUNK_SIZE;
                const end = Math.min(total - 1, start + DOWNLOAD_CHUNK_SIZE - 1);
                downloadState.chunks[index] = await downloadRangeWithRetry(start, end);
                downloadState.received += downloadState.chunks[index].length;
                updateProgress((downloadState.received / total) * 80, t('status_downloading'));
                const percent = (downloadState.received / total) * 100;
                const elapsed = Math.max(0.1, (Date.now() - downloadStartedAt) / 1000);
                const bytesPerSecond = downloadState.received / elapsed;
                const remaining = bytesPerSecond > 0 ? Math.ceil((total - downloadState.received) / bytesPerSecond) : 0;
                updateDownloadCard(percent, `${SecureCrypto.formatFileSize(bytesPerSecond)}/s · ${tpl('shared_eta', { time: formatDuration(remaining) })}`);
            }

            function formatDuration(seconds) {
                if (seconds < 60) return `${Math.max(1, seconds)}s`;
                return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
            }
        }
        await Promise.all(Array.from({ length: Math.min(DOWNLOAD_CONCURRENCY, chunkCount) }, worker));
        downloadState.blob = new Blob(downloadState.chunks);
        return downloadState.blob;
    }

    async function downloadRangeWithRetry(start, end) {
        let lastError;
        for (let attempt = 0; attempt < MAX_CHUNK_DOWNLOAD_RETRIES; attempt++) {
            if (attempt > 0) await new Promise(resolve => setTimeout(resolve, 500 * 2 ** (attempt - 1)));
            try {
                const response = await fetch(`/api/file/${fileMetadata.id}/download`, {
                    headers: { Range: `bytes=${start}-${end}` }
                });
                if (!response.ok && response.status !== 206) {
                    const error = await response.json().catch(() => ({}));
                    throw new Error(error.error || `Download chunk ${start} failed`);
                }
                const data = new Uint8Array(await response.arrayBuffer());
                if (data.length !== end - start + 1) throw new Error(`Incomplete download chunk ${start}`);
                return data;
            } catch (error) {
                lastError = error;
                console.warn(`Download chunk ${start}-${end} attempt ${attempt + 1} failed:`, error.message);
            }
        }
        throw lastError;
    }

    function triggerDownload(data, filename) {
        const blob = new Blob([data], { type: 'application/octet-stream' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        a.style.display = 'none';
        document.body.appendChild(a);
        a.click();
        setTimeout(() => {
            document.body.removeChild(a);
            URL.revokeObjectURL(url);
        }, 100);
    }

    async function submitReport() {
        reportConfirm.disabled = true;
        reportConfirm.textContent = t('report_btn_loading');
        try {
            const response = await fetch(`/api/file/${fileMetadata.id}/report`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': getCookieValue('csrf_token')
                }
            });

            const result = await response.json();

            if (response.ok) {
                reportModal.classList.add('hidden');
                showNotification(t('toast_report_submitted'), 'info');
            } else {
                showNotification(result.error || t('toast_report_failed'), 'error');
            }
        } catch (error) {
            console.error('Report failed:', error);
            showNotification(t('toast_report_failed_retry'), 'error');
        } finally {
            reportConfirm.disabled = false;
            reportConfirm.textContent = t('report_btn');
        }
    }

    function createProgressCircle(size) {
        const svgNS = 'http://www.w3.org/2000/svg';
        const svg = document.createElementNS(svgNS, 'svg');
        svg.setAttribute('viewBox', '0 0 36 36');
        svg.setAttribute('width', size);
        svg.setAttribute('height', size);
        svg.classList.add('progress-circle');
        const bg = document.createElementNS(svgNS, 'circle');
        bg.setAttribute('cx', '18');
        bg.setAttribute('cy', '18');
        bg.setAttribute('r', '15');
        bg.setAttribute('fill', 'none');
        bg.setAttribute('stroke', '#E4E3E3');
        bg.setAttribute('stroke-width', '3');
        const fill = document.createElementNS(svgNS, 'circle');
        fill.setAttribute('cx', '18');
        fill.setAttribute('cy', '18');
        fill.setAttribute('r', '15');
        fill.setAttribute('fill', 'none');
        fill.setAttribute('stroke', '#007AFF');
        fill.setAttribute('stroke-width', '3');
        fill.setAttribute('stroke-linecap', 'round');
        fill.setAttribute('stroke-dasharray', '94.25');
        fill.setAttribute('stroke-dashoffset', '94.25');
        fill.setAttribute('transform', 'rotate(-90 18 18)');
        fill.classList.add('progress-circle-fill');
        svg.appendChild(bg);
        svg.appendChild(fill);
        return svg;
    }

    function updateProgress(percent, text) {
        if (progressText) progressText.textContent = `${Math.round(Math.min(100, Math.max(0, percent)))}%`;
        if (text && progressTitle) progressTitle.textContent = text;
        const iconEl = document.getElementById('progress-icon');
        const fill = iconEl?.querySelector('.progress-circle-fill');
        if (fill) {
            const circumference = 94.25;
            fill.setAttribute('stroke-dashoffset', circumference * (1 - Math.min(1, Math.max(0, percent / 100))));
        }
    }

    function showDownloadedState() {
        if (progressTitle) progressTitle.textContent = t('status_complete');
        if (progressText) progressText.textContent = t('state_download_automatically');

        const iconEl = document.getElementById('progress-icon');
        if (iconEl) {
            iconEl.innerHTML = '';
            iconEl.className = '';
            const icon = document.createElement('i');
            icon.setAttribute('data-lucide', 'check-circle');
            icon.style.cssText = 'width: 36px; height: 36px; color: #007AFF;';
            iconEl.appendChild(icon);
            if (window.lucide && lucide.createIcons) {
                lucide.createIcons();
            }
        }
    }

    function resetDownloadBox() {
        if (progressTitle) progressTitle.textContent = t('status_downloading');
        if (progressText) progressText.textContent = t('app_0_pct');

        downloadState.promise = null;
        downloadState.blob = null;
        downloadState.chunks = [];
        downloadState.received = 0;
        downloadState.total = 0;

        const iconEl = document.getElementById('progress-icon');
        if (iconEl) {
            iconEl.innerHTML = '';
            iconEl.appendChild(createProgressCircle(36));
        }
    }

    let notificationTimer = null;

    function showFileError(icon, title, subtitle) {
        loadingSection.classList.add('hidden');
        passwordSection.classList.add('hidden');
        progressSection.classList.add('hidden');

        const section = document.getElementById('error-section');
        const iconEl = document.getElementById('error-icon');
        const titleEl = document.getElementById('error-title');
        const subtitleEl = document.getElementById('error-subtitle');
        if (!section || !titleEl || !subtitleEl) return;

        if (iconEl) {
            iconEl.innerHTML = `<i data-lucide="${icon}" style="width:48px;height:48px;"></i>`;
        }
        titleEl.textContent = title;
        subtitleEl.textContent = subtitle;

        if (window.lucide?.createIcons) {
            window.lucide.createIcons();
        }

        section.classList.remove('hidden');
    }

    function handleAPIError(error) {
        const code = error.code || '';
        switch (code) {
            case 'INVALID_FILE_ID':
            case 'MISSING_FILE_ID':
                showFileError('cloud-alert', t('error_invalid_link_title'), t('error_invalid_link_desc'));

                break;
            case 'FILE_NOT_FOUND':
            case 'FILE_NOT_ON_DISK':
                showFileError('file-question-mark', t('error_file_not_found_title'), t('error_file_not_found_desc'));
                break;
            case 'FILE_DELETED':
                showFileError('shredder', t('error_file_removed_title'), t('error_file_removed_desc'));
                break;
            case 'FILE_EXPIRED':
                showFileError('file-question-mark', t('error_file_expired_title'), t('error_file_expired_desc'));
                break;
            default:
                showNotification(error.error || t('toast_unexpected_error'), 'error');
        }
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

    async function init() {
        setupTOSGate();
        try { await SecureCrypto.loadWordList(); } catch (error) { console.error('Word list failed:', error); }
        setupEventListeners();

        if (!fileId) {
            showFileError('cloud-alert', t('error_invalid_link_title'), t('error_no_file_id'));
            return;
        }

        await loadFileMetadata(fileId);
    }

    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
    else init();
})();
