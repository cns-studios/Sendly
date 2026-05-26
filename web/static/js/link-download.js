(function() {
    'use strict';

    const fileId = window.CONFIG?.fileID;
    let fileMetadata = null;
    let encryptedBlob = null;
    let currentPassword = null;

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
        return getCookieValue('shareit_tos_accepted') === (window.CONFIG?.tosVersion || '2026-04-05');
    }

    function setupTOSGate() {
        if (!tosOverlay) return;
        if (hasAcceptedCurrentTOS()) { tosOverlay.classList.add('hidden'); return; }
        tosOverlay.classList.remove('hidden');
        tosAcceptBtn?.addEventListener('click', () => { setCookie('shareit_tos_accepted', window.CONFIG?.tosVersion || '2026-04-05', 31536000); tosOverlay.classList.add('hidden'); });
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
            showFileError('cloud-alert', 'Connection error', 'Failed to connect to server. Please try again.');
        }
    }

    function displayFileMetadata() {
        fileNameEl.textContent = fileMetadata.original_name;
        fileSizeEl.textContent = SecureCrypto.formatFileSize(fileMetadata.size_bytes);
        fileCreatedEl.textContent = SecureCrypto.formatDate(fileMetadata.created_at);
        fileExpiresEl.textContent = SecureCrypto.getTimeRemaining(fileMetadata.expires_at);
    }

    async function downloadAndDecrypt(password) {
        const validation = SecureCrypto.validatePassword(password);
        if (!validation.valid) {
            showNotification(validation.error, 'error');
            return;
        }

        currentPassword = password;

        loadingSection.classList.add('hidden');
        passwordSection.classList.add('hidden');
        autoDecryptSection.classList.add('hidden');
        progressSection.classList.remove('hidden');

        await new Promise((resolve) => requestAnimationFrame(resolve));

        try {
            const iconEl = document.getElementById('progress-icon');
            if (iconEl) {
                iconEl.innerHTML = '';
                iconEl.appendChild(createProgressCircle(36));
            }

            updateProgress(0, 'Downloading...');
            encryptedBlob = await downloadEncryptedFile();

            updateProgress(80, 'Decrypting...');
            const decryptedData = await SecureCrypto.decryptBlob(
                encryptedBlob,
                password,
                (progress, status) => {
                    updateProgress(80 + (progress * 20), status);
                }
            );

            triggerDownload(decryptedData, fileMetadata.original_name);

            showDownloadedState();

            await new Promise((resolve) => setTimeout(resolve, 3000));

            resetDownloadBox();
            progressSection.classList.add('hidden');
            passwordSection.classList.remove('hidden');
            if (currentPassword) {
                autoDecryptSection.classList.remove('hidden');
            }
        } catch (error) {
            console.error('Download/decrypt failed:', error);
            progressSection.classList.add('hidden');

            showNotification('Download failed. Please try again.', 'error');
            passwordSection.classList.remove('hidden');
            if (currentPassword) {
                autoDecryptSection.classList.remove('hidden');
            }
        }
    }

    async function downloadEncryptedFile() {
        const response = await fetch(`/api/file/${fileMetadata.id}/download`);
        if (!response.ok) {
            const error = await response.json();
            throw new Error(error.error || 'Failed to download file');
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
                const progress = (received / total) * 80;
                updateProgress(progress, 'Downloading...');
            }
        }

        return new Blob(chunks);
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
        reportConfirm.textContent = 'Reporting...';

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
                showNotification('Report submitted. Thank you for helping keep our platform safe.', 'info');
            } else {
                showNotification(result.error || 'Failed to submit report', 'error');
            }
        } catch (error) {
            console.error('Report failed:', error);
            showNotification('Failed to submit report. Please try again.', 'error');
        } finally {
            reportConfirm.disabled = false;
            reportConfirm.textContent = 'Report';
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
        if (progressText) progressText.textContent = `${Math.round(percent)}%`;
        if (text && progressTitle) progressTitle.textContent = text;
        const iconEl = document.getElementById('progress-icon');
        const fill = iconEl?.querySelector('.progress-circle-fill');
        if (fill) {
            const circumference = 94.25;
            fill.setAttribute('stroke-dashoffset', circumference * (1 - Math.min(1, Math.max(0, percent / 100))));
        }
    }

    function showDownloadedState() {
        if (progressTitle) progressTitle.textContent = 'Downloaded';
        if (progressText) progressText.textContent = 'The download should start automatically';

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
        if (progressTitle) progressTitle.textContent = 'Downloading...';
        if (progressText) progressText.textContent = '0%';

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
                showFileError('cloud-alert', 'Invalid link', 'The link is invalid or the file ID format is incorrect. Please check the link and try again.');
                break;
            case 'FILE_NOT_FOUND':
            case 'FILE_NOT_ON_DISK':
                showFileError('file-question-mark', 'File not found', 'This file does not exist or has been moved');
                break;
            case 'FILE_DELETED':
                showFileError('shredder', 'File removed', 'This file has been removed');
                break;
            case 'FILE_EXPIRED':
                showFileError('file-question-mark', 'File expired', 'This file has expired and is no longer available. Ask the sender to upload it again.');
                break;
            default:
                showNotification(error.error || 'An unexpected error occurred.', 'error');
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
            showFileError('cloud-alert', 'Invalid link', 'No file ID provided. Please check the link and try again.');
            return;
        }

        await loadFileMetadata(fileId);
    }

    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
    else init();
})();
