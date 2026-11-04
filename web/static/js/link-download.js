(function() {
    'use strict';

    const t = (k, d) => window.CONFIG?.t?.[k] || d || k;
    const tpl = (k, vars) => { let s = t(k); if (vars) for (const [key, val] of Object.entries(vars)) s = s.replace(`{${key}}`, val); return s; };

    let fileMetadata = null;
    let linkSecret = null;

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

    const loadingSection = document.getElementById('loading-section');
    const fileSection = document.getElementById('file-section');
    const autoDecryptSection = document.getElementById('auto-decrypt-section');
    const missingKeySection = document.getElementById('missing-key-section');

    const fileNameEl = document.getElementById('file-name');
    const fileMetaEl = document.getElementById('file-meta');
    const fileIconEl = document.getElementById('file-icon');

    const downloadAutoBtn = document.getElementById('download-auto-btn');
    const downloadStatus = document.getElementById('download-status');
    const downloadWords = autoDecryptSection ? autoDecryptSection.querySelectorAll('.share-word') : [];
    const downloadNums = autoDecryptSection ? autoDecryptSection.querySelectorAll('.share-num-digits') : [];

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
        return getCookieValue('sendly_tos_accepted') === (window.CONFIG?.tosVersion || '2026-09-25');
    }

    function setupTOSGate() {
        if (!tosOverlay) return;
        if (hasAcceptedCurrentTOS()) { tosOverlay.classList.add('hidden'); return; }
        tosOverlay.classList.remove('hidden');
        tosAcceptBtn?.addEventListener('click', () => { setCookie('sendly_tos_accepted', window.CONFIG?.tosVersion || '2026-09-25', 31536000); tosOverlay.classList.add('hidden'); });
        tosDeclineBtn?.addEventListener('click', () => { window.location.href = 'https://cns.ad'; });
    }

    function setupEventListeners() {
        downloadAutoBtn?.addEventListener('click', downloadAndDecrypt);
        reportBtn?.addEventListener('click', () => { reportModal.classList.remove('hidden'); });
        reportCancel?.addEventListener('click', () => { reportModal.classList.add('hidden'); });
        reportConfirm?.addEventListener('click', submitReport);
        reportModal?.addEventListener('click', (e) => { if (e.target === reportModal) reportModal.classList.add('hidden'); });
    }

    async function loadFileMetadata(fileID) {
        try {
            const response = await SendlyNet.fetch(`/api/file/${fileID}`);
            if (!response.ok) {
                const error = await response.json();
                handleAPIError(error);
                return;
            }

            fileMetadata = await response.json();
            displayFileMetadata();
            setDownloadState('idle');

            autoDecryptSection.classList.toggle('hidden', !linkSecret);
            missingKeySection?.classList.toggle('hidden', !!linkSecret);

            loadingSection.classList.add('hidden');
            fileSection.classList.remove('hidden');

        } catch (error) {
            console.error('Failed to load file metadata:', error);
            showFileError('cloud-alert', t('error_connection_title'), t('error_connection_desc'));
        }
    }

    const FILE_ICONS = [
        [/\.(png|jpe?g|gif|webp|avif|heic|bmp|svg|tiff?)$/i, 'file-image'],
        [/\.(mp4|mov|mkv|webm|avi|m4v)$/i, 'file-video'],
        [/\.(mp3|wav|flac|aac|ogg|m4a|opus)$/i, 'file-music'],
        [/\.(zip|rar|7z|tar|gz|tgz|bz2|xz|zst)$/i, 'file-archive'],
        [/\.(pdf|docx?|odt|rtf|txt|md|pages)$/i, 'file-text'],
        [/\.(xlsx?|ods|csv|numbers)$/i, 'file-spreadsheet'],
        [/\.(js|ts|go|py|rs|java|c|cpp|h|json|html|css|sh|ya?ml)$/i, 'file-code']
    ];

    function iconForFile(name) {
        const match = FILE_ICONS.find(([re]) => re.test(name || ''));
        return match ? match[1] : 'file';
    }

    function displayFileMetadata() {
        document.title = `${fileMetadata.original_name} · ${SecureCrypto.formatFileSize(fileMetadata.size_bytes)} ➤ Sendly`;
        fileNameEl.textContent = fileMetadata.original_name;

        const parts = [
            SecureCrypto.formatFileSize(fileMetadata.size_bytes),
            `${t('shared_uploaded')} ${new Date(fileMetadata.created_at).toLocaleDateString()}`
        ];
        if (fileMetadata.expires_at) parts.push(SecureCrypto.getTimeRemaining(fileMetadata.expires_at));
        fileMetaEl.replaceChildren();
        parts.forEach((text, i) => {
            if (i > 0) {
                const dot = document.createElement('span');
                dot.className = i === 2 ? 'uploaded-meta-dot share-meta-date' : 'uploaded-meta-dot';
                dot.setAttribute('aria-hidden', 'true');
                fileMetaEl.appendChild(dot);
            }
            const span = document.createElement('span');
            if (i === 1) span.className = 'share-meta-date';
            span.textContent = text;
            fileMetaEl.appendChild(span);
        });

        if (fileIconEl) {
            fileIconEl.innerHTML = `<i data-lucide="${iconForFile(fileMetadata.original_name)}" aria-hidden="true"></i>`;
            if (window.lucide?.createIcons) window.lucide.createIcons();
        }
    }

    const DOWNLOAD_SHARE = 94;
    const CREEP_HEADROOM = 5;
    const reduceMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;

    const meter = {
        shown: 0,
        target: 0,
        cap: DOWNLOAD_SHARE - 0.5,
        finishing: false,
        raf: 0,
        last: 0,
        lastDigits: -1,
        lastDigitsAt: 0,
        onFinished: null
    };

    function startMeter() {
        cancelAnimationFrame(meter.raf);
        Object.assign(meter, { shown: 0, target: 0, cap: DOWNLOAD_SHARE - 0.5, finishing: false, last: 0, lastDigits: -1, lastDigitsAt: 0, onFinished: null });
        renderMeter(0, true);
        meter.raf = requestAnimationFrame(tickMeter);
    }

    function stopMeter() {
        cancelAnimationFrame(meter.raf);
        meter.raf = 0;
        meter.onFinished = null;
    }

    function setMeterTarget(percent, cap) {
        meter.target = Math.max(meter.target, Math.min(100, percent));
        if (cap !== undefined) meter.cap = Math.max(meter.cap, cap);
    }

    function finishMeter() {
        meter.finishing = true;
        meter.target = 100;
        meter.cap = 100;
        return new Promise((resolve) => { meter.onFinished = resolve; });
    }

    function tickMeter(now) {
        const dt = meter.last ? Math.min(0.1, (now - meter.last) / 1000) : 0;
        meter.last = now;

        if (meter.shown < meter.target) {
            const tau = meter.finishing ? 0.16 : 0.45;
            meter.shown += (meter.target - meter.shown) * (1 - Math.exp(-dt / tau)) + dt * 0.6;
            meter.shown = Math.min(meter.shown, meter.target);
        } else if (!meter.finishing) {
            const creepGoal = Math.min(meter.cap, meter.target + CREEP_HEADROOM);
            if (creepGoal > meter.shown) meter.shown += (creepGoal - meter.shown) * (1 - Math.exp(-dt / 3.5));
        }

        if (meter.finishing && meter.shown >= 99.6) {
            meter.shown = 100;
            renderMeter(100, true);
            const done = meter.onFinished;
            stopMeter();
            done?.();
            return;
        }

        renderMeter(meter.shown, false);
        meter.raf = requestAnimationFrame(tickMeter);
    }

    function renderMeter(value, force) {
        downloadAutoBtn?.style.setProperty('--p', `${value.toFixed(2)}%`);
        const digits = Math.floor(value);
        const now = performance.now();
        if (digits === meter.lastDigits) return;
        if (!force && now - meter.lastDigitsAt < 90) return;
        meter.lastDigits = digits;
        meter.lastDigitsAt = now;
        downloadNums.forEach((el) => rollText(el, String(digits)));
    }

    function rollText(container, text) {
        const slots = Array.from(container.children);
        while (slots.length < text.length) {
            const slot = document.createElement('span');
            slot.className = 'share-slot';
            container.prepend(slot);
            slots.unshift(slot);
        }
        while (slots.length > text.length) slots.shift().remove();
        slots.forEach((slot, i) => {
            const char = text[i];
            const current = slot.querySelector('.share-char:not(.share-out)');
            if (current && current.textContent === char) return;
            slot.querySelectorAll('.share-out').forEach((el) => el.remove());
            if (current) {
                if (reduceMotion) current.remove();
                else {
                    current.classList.add('share-out');
                    current.addEventListener('animationend', () => current.remove(), { once: true });
                }
            }
            const next = document.createElement('span');
            next.className = current && !reduceMotion ? 'share-char share-in' : 'share-char';
            next.textContent = char;
            slot.appendChild(next);
        });
    }

    function swapWord(text) {
        downloadWords.forEach((el) => {
            const current = el.querySelector('.share-word-text:not(.share-out)');
            if (current && current.textContent === text) return;
            el.querySelectorAll('.share-out').forEach((node) => node.remove());
            if (current) {
                if (reduceMotion) current.remove();
                else {
                    current.classList.add('share-out');
                    current.addEventListener('animationend', () => current.remove(), { once: true });
                }
            }
            const next = document.createElement('span');
            next.className = current && !reduceMotion ? 'share-word-text share-in' : 'share-word-text';
            next.textContent = text;
            el.appendChild(next);
        });
    }

    function setDownloadState(state, status = '') {
        if (!autoDecryptSection) return;
        autoDecryptSection.classList.remove('active', 'done', 'error');
        if (state !== 'idle') autoDecryptSection.classList.add(state);
        if (downloadAutoBtn) downloadAutoBtn.disabled = state === 'active';
        if (state !== 'active') downloadAutoBtn?.style.setProperty('--p', state === 'done' ? '100%' : '0%');
        swapWord({
            idle: t('shared_download'),
            active: t('shared_downloading_label'),
            done: t('shared_complete'),
            error: t('shared_retry')
        }[state]);
        setStatus(status);
    }

    function setStatus(text) {
        if (downloadStatus && downloadStatus.textContent !== text) downloadStatus.textContent = text;
    }

    const speed = { bytesPerSecond: 0, lastBytes: 0, lastAt: 0, lastRender: 0 };

    function trackSpeed() {
        const now = performance.now();
        if (!speed.lastAt) {
            Object.assign(speed, { lastAt: now, lastBytes: downloadState.received, lastRender: now });
            return;
        }
        const dt = (now - speed.lastAt) / 1000;
        if (dt < 0.25) return;
        const instant = Math.max(0, downloadState.received - speed.lastBytes) / dt;
        speed.bytesPerSecond = speed.bytesPerSecond ? speed.bytesPerSecond * 0.75 + instant * 0.25 : instant;
        speed.lastAt = now;
        speed.lastBytes = downloadState.received;
        if (now - speed.lastRender < 700 || !speed.bytesPerSecond || !downloadState.total) return;
        speed.lastRender = now;
        const remaining = Math.ceil((downloadState.total - downloadState.received) / speed.bytesPerSecond);
        setStatus(`${SecureCrypto.formatFileSize(speed.bytesPerSecond)}/s · ${tpl('shared_eta', { time: formatDuration(remaining) })}`);
    }

    function formatDuration(seconds) {
        if (seconds < 60) return `${Math.max(1, seconds)}s`;
        return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
    }

    function onBytesReceived(count) {
        downloadState.received = Math.max(0, downloadState.received + count);
        if (downloadState.total) setMeterTarget((downloadState.received / downloadState.total) * DOWNLOAD_SHARE);
        trackSpeed();
    }

    async function downloadAndDecrypt() {
        if (isProcessing || !linkSecret) return;
        isProcessing = true;

        try {
            Object.assign(speed, { bytesPerSecond: 0, lastBytes: 0, lastAt: 0, lastRender: 0 });
            setDownloadState('active');
            startMeter();
            const encryptedBlob = await downloadEncryptedFile();

            setMeterTarget(DOWNLOAD_SHARE, 99);
            const decryptedData = await SecureCrypto.decryptFileChunked(
                encryptedBlob,
                linkSecret,
                fileMetadata.size_bytes,
                (progress) => setMeterTarget(DOWNLOAD_SHARE + (progress / 100) * (100 - DOWNLOAD_SHARE))
            );

            await finishMeter();
            triggerDownload(decryptedData, fileMetadata.original_name);
            setDownloadState('done', t('state_download_automatically'));
            resetDownloadState();

            await new Promise((resolve) => setTimeout(resolve, 3200));
            setDownloadState('idle');
        } catch (error) {
            console.error('Download/decrypt failed:', error);
            stopMeter();
            setDownloadState('error', t('toast_download_failed'));
            showNotification(t('toast_download_failed'), 'error');
        } finally {
            isProcessing = false;
        }
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
            resetDownloadState();
            throw error;
        }
    }

    async function readBody(response, onBytes) {
        if (!response.body?.getReader) {
            const data = new Uint8Array(await response.arrayBuffer());
            onBytes(data.length);
            return data;
        }
        const reader = response.body.getReader();
        const parts = [];
        let size = 0;
        while (true) {
            const { done, value } = await reader.read();
            if (done) break;
            parts.push(value);
            size += value.length;
            onBytes(value.length);
        }
        const out = new Uint8Array(size);
        let offset = 0;
        for (const part of parts) {
            out.set(part, offset);
            offset += part.length;
        }
        return out;
    }

    async function readCounted(response) {
        let counted = 0;
        try {
            return await readBody(response, (n) => { counted += n; onBytesReceived(n); });
        } catch (error) {
            onBytesReceived(-counted);
            throw error;
        }
    }

    async function streamEncryptedFile() {
        const firstEnd = DOWNLOAD_CHUNK_SIZE - 1;
        const firstResponse = await SendlyNet.fetch(`/api/file/${fileMetadata.id}/download`, {
            headers: { Range: `bytes=0-${firstEnd}` }
        });
        if (!firstResponse.ok) {
            const error = await firstResponse.json().catch(() => ({}));
            throw SendlyToast.apiError(error, t('toast_download_failed'));
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
            const data = await readCounted(firstResponse);
            if (data.length !== total) throw new Error(t('toast_download_failed'));
            downloadState.chunks = [data];
            downloadState.blob = new Blob(downloadState.chunks);
            return downloadState.blob;
        }

        const firstChunk = await readCounted(firstResponse);
        const expectedFirstLength = Math.min(DOWNLOAD_CHUNK_SIZE, total);
        if (firstChunk.length !== expectedFirstLength) throw new Error(t('toast_download_failed'));
        downloadState.chunks[0] = firstChunk;
        let nextChunk = 1;

        async function worker() {
            while (true) {
                const index = nextChunk++;
                if (index >= chunkCount) return;
                const start = index * DOWNLOAD_CHUNK_SIZE;
                const end = Math.min(total - 1, start + DOWNLOAD_CHUNK_SIZE - 1);
                downloadState.chunks[index] = await downloadRangeWithRetry(start, end);
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
                const response = await SendlyNet.fetch(`/api/file/${fileMetadata.id}/download`, {
                    headers: { Range: `bytes=${start}-${end}` }
                });
                if (!response.ok && response.status !== 206) {
                    const error = await response.json().catch(() => ({}));
                    throw SendlyToast.apiError(error, `Download chunk ${start} failed`);
                }
                const data = await readCounted(response);
                if (data.length !== end - start + 1) {
                    onBytesReceived(-data.length);
                    throw new Error(`Incomplete download chunk ${start}`);
                }
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
                SendlyToast.success(t('toast_report_submitted'));
            } else {
                SendlyToast.fail(SendlyToast.apiError(result), t('toast_report_failed'));
            }
        } catch (error) {
            console.error('Report failed:', error);
            showNotification(t('toast_report_failed_retry'), 'error');
        } finally {
            reportConfirm.disabled = false;
            reportConfirm.textContent = t('report_btn');
        }
    }

    function resetDownloadState() {
        downloadState.promise = null;
        downloadState.blob = null;
        downloadState.chunks = [];
        downloadState.received = 0;
        downloadState.total = 0;
    }

    function showFileError(icon, title, subtitle) {
        loadingSection.classList.add('hidden');
        fileSection.classList.add('hidden');

        const section = document.getElementById('error-section');
        const iconEl = document.getElementById('error-icon');
        const titleEl = document.getElementById('error-title');
        const subtitleEl = document.getElementById('error-subtitle');
        if (!section || !titleEl || !subtitleEl) return;

        if (iconEl) {
            iconEl.innerHTML = `<i data-lucide="${icon}" aria-hidden="true"></i>`;
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
                SendlyToast.fail(SendlyToast.apiError(error), t('toast_unexpected_error'));
        }
    }

    function showNotification(message, type) {
        SendlyToast.show(message, { type });
    }

    async function resolveFileId() {
        if (window.CONFIG?.fileID) return window.CONFIG.fileID;
        if (!SecureCrypto.isLinkToken(linkSecret)) return '';
        return SecureCrypto.fileIdFromLinkToken(linkSecret);
    }

    async function init() {
        setupTOSGate();
        setupEventListeners();

        linkSecret = SecureCrypto.getLinkSecretFromHash();
        const fileId = await resolveFileId();
        if (!fileId) {
            showFileError('cloud-alert', t('error_invalid_link_title'), t('error_invalid_link_desc'));
            return;
        }

        await loadFileMetadata(fileId);
    }

    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
    else init();
})();
