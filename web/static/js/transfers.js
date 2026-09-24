// Transfers page: files other users sent to the signed-in user.
//
// End-to-end encryption: the server only ever returns the file key wrapped
// for this user's identity key, and only after the transfer is accepted.
// It is unwrapped here with the identity private key held on this device,
// then the encrypted blob is downloaded and decrypted in the browser.
(function () {
    'use strict';

    const t = (k, d) => window.CONFIG?.t?.[k] || d || k;
    const tpl = (k, vars) => { let s = t(k); if (vars) for (const [key, val] of Object.entries(vars)) s = s.replace(`{${key}}`, val); return s; };

    const USER_ID = window.CONFIG?.cnsUserId || 0;
    const USERNAME = window.CONFIG?.cnsUsername || '';
    const PER_PAGE = 20;
    const LOCALE = document.documentElement.lang || 'en';

    const pendingList = document.getElementById('transfers-pending-list');
    const historyList = document.getElementById('transfers-history-list');
    const historyMore = document.getElementById('transfers-history-more');
    const pendingCount = document.getElementById('transfers-pending-count');
    const deviceNotice = document.getElementById('transfers-device-notice');

    let pendingItems = [];
    let historyItems = [];
    let historyPage = 0;
    let historyTotalPages = 0;
    let device = null; // { deviceId, identityKey } once this device is ready
    const busy = new Set();
    // Accepted transfers whose file key can no longer be unwrapped here: they
    // were wrapped for an identity key this account replaced when it was
    // recovered on a device that never held the old key.
    const lockedFiles = new Set();
    const lockChecked = new Set();
    let notificationTimer = null;
    let reloadTimer = null;
    let pendingLoaded = false;

    function getCookieValue(name) {
        const parts = `; ${document.cookie}`.split(`; ${name}=`);
        return parts.length === 2 ? parts.pop().split(';').shift() : '';
    }

    async function api(path, options = {}) {
        const response = await fetch(path, {
            ...options,
            headers: { 'X-CSRF-Token': getCookieValue('csrf_token'), ...(options.headers || {}) }
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
            const error = new Error(payload.error || t('transfers_failed'));
            error.code = payload.code;
            error.status = response.status;
            throw error;
        }
        return payload;
    }

    // ── Notifications (same pill as the other pages) ──
    function notify(message, type = 'info') {
        const pill = document.getElementById('notification-pill');
        const icon = document.getElementById('notification-icon');
        const text = document.getElementById('notification-text');
        if (!pill || !text) return;
        clearTimeout(notificationTimer);
        text.textContent = message;
        if (icon) {
            icon.setAttribute('data-lucide', type === 'error' ? 'circle-x' : 'circle-check');
            icon.style.color = type === 'error' ? '#FF3B30' : '#00A36C';
            window.lucide?.createIcons?.();
        }
        pill.classList.remove('hidden');
        void pill.offsetHeight;
        pill.classList.add('visible');
        notificationTimer = setTimeout(() => {
            pill.classList.remove('visible');
            setTimeout(() => pill.classList.add('hidden'), 350);
        }, 3500);
    }

    // ── Formatting ──
    function relativeTime(date) {
        const seconds = (new Date(date).getTime() - Date.now()) / 1000;
        const units = [['year', 31536000], ['month', 2592000], ['week', 604800], ['day', 86400], ['hour', 3600], ['minute', 60]];
        const rtf = new Intl.RelativeTimeFormat(LOCALE, { numeric: 'auto' });
        for (const [unit, size] of units) {
            if (Math.abs(seconds) >= size) return rtf.format(Math.round(seconds / size), unit);
        }
        return rtf.format(0, 'minute');
    }

    function fileExtension(name) {
        const dot = (name || '').lastIndexOf('.');
        return dot > 0 && dot < name.length - 1 ? name.slice(dot + 1).toUpperCase().slice(0, 6) : 'FILE';
    }

    function el(tag, className, text) {
        const node = document.createElement(tag);
        if (className) node.className = className;
        if (text !== undefined) node.textContent = text;
        return node;
    }

    function icon(name, className) {
        const i = document.createElement('i');
        i.setAttribute('data-lucide', name);
        i.setAttribute('aria-hidden', 'true');
        if (className) i.className = className;
        return i;
    }

    // ── Rendering ──
    function skeleton(list, count) {
        list.setAttribute('aria-busy', 'true');
        list.replaceChildren(...Array.from({ length: count }, () => {
            const card = el('div', 'card transfer-card transfer-skeleton');
            card.innerHTML = '<div class="file-row"><div class="sk sk-icon"></div><div class="file-meta"><div class="sk sk-line"></div><div class="sk sk-line short"></div></div></div><div class="transfer-footer"><div class="sk sk-line medium"></div></div>';
            return card;
        }));
    }

    function emptyState(list, iconName, title, subtitle) {
        list.setAttribute('aria-busy', 'false');
        const box = el('div', 'transfers-empty');
        box.append(icon(iconName, 'transfers-empty-icon'), el('strong', '', title));
        if (subtitle) box.append(el('p', '', subtitle));
        list.replaceChildren(box);
        window.lucide?.createIcons?.();
    }

    function errorState(list, retry) {
        list.setAttribute('aria-busy', 'false');
        const box = el('div', 'transfers-empty is-error');
        const button = el('button', 'transfers-retry', t('transfers_retry'));
        button.type = 'button';
        button.addEventListener('click', retry);
        box.append(icon('cloud-alert', 'transfers-empty-icon'), el('strong', '', t('transfers_load_failed')), button);
        list.replaceChildren(box);
        window.lucide?.createIcons?.();
    }

    function statusPill(item) {
        if (item.status === 'pending') {
            return el('div', 'expiry-pill', SecureCrypto.getTimeRemaining(item.expires_at));
        }
        if (item.status === 'declined') return el('div', 'transfer-status is-declined', t('transfers_status_declined'));
        if (lockedFiles.has(item.file_id)) {
            const pill = el('div', 'transfer-status is-expired is-locked', t('transfers_status_locked'));
            pill.title = t('transfers_locked');
            return pill;
        }
        if (!item.available) return el('div', 'transfer-status is-expired', t('transfers_status_expired'));
        return el('div', 'transfer-status is-accepted', t('transfers_status_accepted'));
    }

    function buildCard(item) {
        const card = el('article', `card transfer-card is-${item.status}`);
        card.dataset.fileId = item.file_id;
        if (item.status !== 'pending' && (item.status === 'declined' || !item.available || lockedFiles.has(item.file_id))) card.classList.add('is-muted');

        const row = el('div', 'file-row');
        const fileIcon = el('div', 'file-icon');
        fileIcon.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>';
        const meta = el('div', 'file-meta');
        const name = el('div', 'file-meta-name', item.filename);
        name.title = item.filename;
        const sub = el('div', 'file-meta-sub');
        sub.append(el('span', '', SecureCrypto.formatFileSize(item.size_bytes)), el('span', 'dot', '•'), el('span', '', fileExtension(item.filename)));
        sub.querySelector('.dot').setAttribute('aria-hidden', 'true');
        meta.append(name, sub);
        row.append(fileIcon, meta, statusPill(item));

        const footer = el('div', 'transfer-footer');
        const sender = el('div', 'transfer-sender');
        const avatar = el('span', 'transfer-sender-avatar');
        avatar.appendChild(window.buildUserAvatar(item.sender_username || '?', item.sender_avatar_url, 22));
        const senderText = el('span', 'transfer-sender-text');
        const who = el('strong', '', `@${item.sender_username || '?'}`);
        const when = item.status === 'pending' || !item.responded_at ? item.sent_at : item.responded_at;
        const whenLabel = item.status === 'pending' ? relativeTime(when)
            : tpl(item.status === 'accepted' ? 'transfers_accepted_when' : 'transfers_declined_when', { time: relativeTime(when) });
        senderText.append(document.createTextNode(`${t('transfers_from')} `), who, document.createTextNode(` · ${whenLabel}`));
        sender.append(avatar, senderText);

        const actions = el('div', 'transfer-actions');
        if (item.status === 'pending') {
            const decline = actionButton('transfer-btn is-decline', 'x', t('transfers_decline'));
            decline.title = t('transfers_decline_hint');
            const accept = actionButton('transfer-btn is-accept', 'check', t('transfers_accept'));
            decline.addEventListener('click', () => handleDecline(item, card, decline));
            accept.addEventListener('click', () => handleAccept(item, card, accept));
            actions.append(decline, accept);
        } else if (item.status === 'accepted' && item.available && !lockedFiles.has(item.file_id)) {
            const download = actionButton('transfer-btn is-download', 'download', t('transfers_download'));
            download.addEventListener('click', () => handleDownload(item, card, download));
            actions.append(download);
        }
        footer.append(sender, actions);

        const progress = el('div', 'transfer-progress');
        progress.append(el('div', 'transfer-progress-fill'));
        card.append(row, footer, progress);
        return card;
    }

    function actionButton(className, iconName, label) {
        const button = el('button', className);
        button.type = 'button';
        const inner = el('span', 'transfer-btn-label');
        inner.append(icon(iconName), el('span', 'transfer-btn-text', label));
        const spinner = icon('loader-circle', 'transfer-btn-spinner');
        button.append(inner, spinner);
        return button;
    }

    function setButtonLoading(button, loading) {
        button.classList.toggle('is-loading', loading);
        button.disabled = loading;
    }

    function renderPending() {
        pendingCount.textContent = String(pendingItems.length);
        pendingCount.classList.toggle('hidden', pendingItems.length === 0);
        if (!pendingItems.length) {
            emptyState(pendingList, 'inbox', t('transfers_empty_pending'), t('transfers_empty_pending_sub'));
            return;
        }
        pendingList.setAttribute('aria-busy', 'false');
        pendingList.replaceChildren(...pendingItems.map(buildCard));
        window.lucide?.createIcons?.();
    }

    function renderHistory() {
        historyMore.classList.toggle('hidden', historyPage >= historyTotalPages);
        if (!historyItems.length) {
            emptyState(historyList, 'history', t('transfers_empty_history'));
            return;
        }
        historyList.setAttribute('aria-busy', 'false');
        historyList.replaceChildren(...historyItems.map(buildCard));
        window.lucide?.createIcons?.();
    }

    // ── Loading ──
    async function loadPending() {
        try {
            const payload = await api(`/api/me/transfers?view=pending&per_page=50`);
            pendingItems = payload.items || [];
            pendingLoaded = true;
            renderPending();
        } catch (error) {
            console.error('Failed to load pending transfers:', error);
            errorState(pendingList, () => { skeleton(pendingList, 2); loadPending(); });
        }
    }

    async function loadHistory(page = 1) {
        try {
            const payload = await api(`/api/me/transfers?view=history&page=${page}&per_page=${PER_PAGE}`);
            const items = payload.items || [];
            historyItems = page === 1 ? items : historyItems.concat(items.filter(i => !historyItems.some(h => h.file_id === i.file_id)));
            historyPage = payload.page || page;
            historyTotalPages = payload.total_pages || 0;
            renderHistory();
            checkLockedTransfers();
        } catch (error) {
            console.error('Failed to load transfer history:', error);
            if (page === 1) errorState(historyList, () => { skeleton(historyList, 2); loadHistory(1); });
            else notify(t('transfers_load_failed'), 'error');
        }
    }

    // Collapse a card out of the pending list before re-rendering.
    function removeCard(card) {
        return new Promise(resolve => {
            card.style.height = `${card.offsetHeight}px`;
            card.classList.add('is-leaving');
            requestAnimationFrame(() => { card.style.height = '0px'; });
            setTimeout(resolve, 280);
        });
    }

    function moveToHistory(item, status) {
        pendingItems = pendingItems.filter(p => p.file_id !== item.file_id);
        historyItems = [{ ...item, status, responded_at: new Date().toISOString() }, ...historyItems.filter(h => h.file_id !== item.file_id)];
        renderPending();
        renderHistory();
        window.SendlyTransfers?.refreshCount?.();
    }

    // ── Actions ──
    async function handleAccept(item, card, button) {
        if (busy.has(item.file_id)) return;
        busy.add(item.file_id);
        setButtonLoading(button, true);
        card.querySelectorAll('.transfer-btn').forEach(b => { b.disabled = true; });
        try {
            await api(`/api/me/transfers/${encodeURIComponent(item.file_id)}/accept`, { method: 'POST' });
            await removeCard(card);
            moveToHistory(item, 'accepted');
            notify(tpl('transfers_accepted_toast', { name: item.filename }));
        } catch (error) {
            notify(error.message, 'error');
            if (error.status === 409 || error.status === 410 || error.status === 404) refreshAll();
            else card.querySelectorAll('.transfer-btn').forEach(b => { b.disabled = false; });
        } finally {
            busy.delete(item.file_id);
            setButtonLoading(button, false);
        }
    }

    // Declining revokes the key for good, so it takes a second, confirming click.
    async function handleDecline(item, card, button) {
        if (busy.has(item.file_id)) return;
        if (!button.classList.contains('is-confirming')) {
            button.classList.add('is-confirming');
            button.querySelector('.transfer-btn-text').textContent = t('transfers_decline_confirm');
            clearTimeout(button._confirmTimer);
            button._confirmTimer = setTimeout(() => {
                button.classList.remove('is-confirming');
                button.querySelector('.transfer-btn-text').textContent = t('transfers_decline');
            }, 4000);
            return;
        }
        clearTimeout(button._confirmTimer);
        busy.add(item.file_id);
        setButtonLoading(button, true);
        card.querySelectorAll('.transfer-btn').forEach(b => { b.disabled = true; });
        try {
            await api(`/api/me/transfers/${encodeURIComponent(item.file_id)}/decline`, { method: 'POST' });
            SecureCrypto.removeCachedFileKey?.(item.file_id);
            await removeCard(card);
            moveToHistory(item, 'declined');
            notify(tpl('transfers_declined_toast', { name: item.filename }));
        } catch (error) {
            notify(error.message, 'error');
            if (error.status === 409 || error.status === 404) refreshAll();
            else card.querySelectorAll('.transfer-btn').forEach(b => { b.disabled = false; });
        } finally {
            busy.delete(item.file_id);
            setButtonLoading(button, false);
        }
    }

    // Register this device once, even if several callers ask at the same time.
    let devicePromise = null;
    function ensureDevice() {
        if (device) return Promise.resolve(device);
        if (!devicePromise) {
            devicePromise = registerDevice().catch((error) => {
                devicePromise = null;
                throw error;
            });
        }
        return devicePromise;
    }

    async function registerDevice() {
        const result = await SecureCrypto.registerAuthenticatedDevice({
            userId: USER_ID,
            username: USERNAME,
            csrfToken: getCookieValue('csrf_token'),
            includeBootstrapEnvelope: true
        });
        if (result.payload?.needs_enrollment) {
            deviceNotice.classList.remove('hidden');
            window.lucide?.createIcons?.();
            const error = new Error(t('transfers_device_untrusted_title'));
            error.code = 'DEVICE_NOT_TRUSTED';
            throw error;
        }
        const identityKey = result.identityKey || SecureCrypto.getIdentityKey(USER_ID);
        if (!identityKey?.privateKeyJWK) throw new Error(t('transfers_no_identity_key'));
        device = { deviceId: result.identity.deviceId, identityKey };
        return device;
    }

    function lockedError() {
        const error = new Error(t('transfers_locked'));
        error.code = 'FILE_LOCKED';
        return error;
    }

    // Unwrap the file key locally with this device's identity private key.
    async function fileKeyFor(item) {
        const cached = SecureCrypto.getCachedFileKey(item.file_id);
        if (cached) return cached;
        const { deviceId, identityKey } = await ensureDevice();
        const access = await api(`/api/me/files/${encodeURIComponent(item.file_id)}/access?device_id=${encodeURIComponent(deviceId)}`);
        if (!access.file_access_key_envelope?.wrapped_dek_b64) throw new Error(t('transfers_key_unavailable'));
        let dek;
        try {
            dek = await SecureCrypto.unwrapFileDEK(access.file_access_key_envelope, {
                authenticated: true,
                identityPrivateKeyJWK: identityKey.privateKeyJWK
            });
        } catch (_) {
            // Wrapped for an identity key this device does not hold.
            throw lockedError();
        }
        const passphrase = new TextDecoder().decode(dek);
        SecureCrypto.cacheFileKey(item.file_id, passphrase);
        return passphrase;
    }

    async function handleDownload(item, card, button) {
        if (busy.has(item.file_id)) return;
        busy.add(item.file_id);
        setButtonLoading(button, true);
        const fill = card.querySelector('.transfer-progress-fill');
        const setProgress = (pct) => { fill.style.width = `${Math.max(0, Math.min(100, pct))}%`; };
        card.classList.add('is-downloading');
        setProgress(2);
        try {
            const passphrase = await fileKeyFor(item);
            const response = await fetch(`/api/file/${encodeURIComponent(item.file_id)}/download`);
            if (!response.ok) throw new Error(t('transfers_download_failed_generic'));
            const total = parseInt(response.headers.get('Content-Length'), 10);
            const reader = response.body.getReader();
            const chunks = [];
            let received = 0;
            for (;;) {
                const { done, value } = await reader.read();
                if (done) break;
                chunks.push(value);
                received += value.length;
                if (total) setProgress((received / total) * 80);
            }
            let decrypted;
            try {
                decrypted = await SecureCrypto.decryptBlob(new Blob(chunks), passphrase, (p) => setProgress(80 + p * 0.2));
            } catch (_) {
                throw lockedError();
            }
            const url = URL.createObjectURL(new Blob([decrypted], { type: 'application/octet-stream' }));
            const a = document.createElement('a');
            a.href = url;
            a.download = item.filename || `${item.file_id}.bin`;
            document.body.appendChild(a);
            a.click();
            a.remove();
            setTimeout(() => URL.revokeObjectURL(url), 1000);
            setProgress(100);
        } catch (error) {
            console.error('Transfer download failed:', error);
            if (error.code === 'FILE_LOCKED') {
                markLocked(item);
                notify(error.message, 'error');
            } else if (error.code !== 'DEVICE_NOT_TRUSTED') notify(tpl('transfers_download_failed', { msg: error.message }), 'error');
            else notify(error.message, 'error');
        } finally {
            busy.delete(item.file_id);
            setButtonLoading(button, false);
            setTimeout(() => { card.classList.remove('is-downloading'); setProgress(0); }, 600);
        }
    }

    function markLocked(item) {
        if (lockedFiles.has(item.file_id)) return;
        lockedFiles.add(item.file_id);
        SecureCrypto.removeCachedFileKey?.(item.file_id);
        renderHistory();
    }

    // Find accepted transfers this device can no longer decrypt, so they show
    // as locked instead of offering a download that is bound to fail.
    async function checkLockedTransfers() {
        const candidates = historyItems.filter(item => item.status === 'accepted' && item.available
            && !lockChecked.has(item.file_id) && !lockedFiles.has(item.file_id));
        for (const item of candidates) {
            lockChecked.add(item.file_id);
            try {
                await fileKeyFor(item);
            } catch (error) {
                if (error.code === 'FILE_LOCKED') markLocked(item);
                else if (error.code === 'DEVICE_NOT_TRUSTED') return;
            }
        }
    }

    function refreshAll() {
        loadPending();
        loadHistory(1);
    }

    function init() {
        skeleton(pendingList, 2);
        skeleton(historyList, 2);
        refreshAll();
        historyMore.addEventListener('click', () => loadHistory(historyPage + 1));

        // The account menu owns the live socket and re-broadcasts count
        // changes; reload the pending list when a new transfer arrives.
        window.addEventListener('sendly:transfers-updated', (e) => {
            const count = e.detail?.count;
            if (!pendingLoaded || (typeof count === 'number' && count === pendingItems.length)) return;
            clearTimeout(reloadTimer);
            reloadTimer = setTimeout(refreshAll, 250);
        });
    }

    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
    else init();
})();
