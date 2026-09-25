// Header account menu: open/close, keyboard navigation and menu actions.
// Markup comes from the "account_menu" template partial.
(function () {
    'use strict';

    var trigger = document.getElementById('account-menu-trigger');
    var menu = document.getElementById('account-menu');
    if (!trigger || !menu) return;

    var items = Array.prototype.slice.call(menu.querySelectorAll('[role="menuitem"]'));
    var isOpen = false;
    var focusIndex = -1;

    function openMenu() {
        if (isOpen) return;
        isOpen = true;
        menu.style.display = 'block';
        trigger.setAttribute('aria-expanded', 'true');
        focusIndex = -1;
    }

    function closeMenu() {
        if (!isOpen) return;
        isOpen = false;
        menu.style.display = '';
        trigger.setAttribute('aria-expanded', 'false');
        focusIndex = -1;
    }

    function focusItem(index) {
        if (!items.length) return;
        focusIndex = (index + items.length) % items.length;
        items[focusIndex].focus();
    }

    trigger.addEventListener('click', function (e) {
        e.stopPropagation();
        if (isOpen) closeMenu(); else openMenu();
    });

    trigger.addEventListener('keydown', function (e) {
        if (e.key === 'Escape') {
            closeMenu();
        } else if (e.key === 'ArrowDown') {
            e.preventDefault();
            openMenu();
            focusItem(focusIndex + 1);
        } else if (e.key === 'ArrowUp' && isOpen) {
            e.preventDefault();
            focusItem(focusIndex - 1);
        }
    });

    items.forEach(function (item, i) {
        item.addEventListener('keydown', function (e) {
            if (e.key === 'Escape') {
                closeMenu();
                trigger.focus();
            } else if (e.key === 'ArrowDown') {
                e.preventDefault();
                focusItem(i + 1);
            } else if (e.key === 'ArrowUp') {
                e.preventDefault();
                focusItem(i - 1);
            } else if (e.key === 'Tab') {
                closeMenu();
            }
        });
    });

    document.addEventListener('click', function (e) {
        if (isOpen && !trigger.contains(e.target) && !menu.contains(e.target)) closeMenu();
    });

    // "Uploaded files" opens the uploads popup in place when the current
    // page provides it (the index page registers SendlyOpenUploadedFiles);
    // elsewhere the link navigates to /#uploaded-files, which opens it on load.
    var uploadedFiles = menu.querySelector('[data-menu-action="uploaded-files"]');
    if (uploadedFiles) {
        uploadedFiles.addEventListener('click', function (e) {
            if (typeof window.SendlyOpenUploadedFiles !== 'function') return;
            e.preventDefault();
            closeMenu();
            window.SendlyOpenUploadedFiles();
        });
    }

    // ── Transfers badge ──
    // Pending (unanswered) transfers show as a red counter on the Transfers
    // item and a dot on the avatar. The count arrives live over the existing
    // per-user device socket (transfers_updated events) and is re-broadcast
    // as a 'sendly:transfers-updated' window event for the transfers page;
    // sent_transfers_updated events become 'sendly:sent-transfers-updated'.
    var badge = document.getElementById('account-menu-transfers-badge');
    var dot = document.getElementById('account-menu-dot');
    var baseLabel = trigger.getAttribute('aria-label') || '';
    var labelTemplate = (window.CONFIG && window.CONFIG.t && window.CONFIG.t.menu_transfers_pending_label) || '{count} new transfers';

    function setTransferCount(count) {
        count = Math.max(0, count | 0);
        if (badge) {
            badge.textContent = count > 99 ? '99+' : String(count);
            badge.hidden = count === 0;
        }
        if (dot) dot.hidden = count === 0;
        trigger.setAttribute('aria-label', count ? baseLabel + ', ' + labelTemplate.replace('{count}', count) : baseLabel);
        window.dispatchEvent(new CustomEvent('sendly:transfers-updated', { detail: { count: count } }));
    }

    function refreshCount() {
        return fetch('/api/me/transfers/pending-count', { credentials: 'same-origin' })
            .then(function (res) { return res.ok ? res.json() : null; })
            .then(function (payload) { if (payload && typeof payload.count === 'number') setTransferCount(payload.count); })
            .catch(function () {});
    }

    var socket = null;
    var retryDelay = 2000;
    function connectSocket() {
        if (!('WebSocket' in window)) return;
        var scheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        try {
            socket = new WebSocket(scheme + '//' + window.location.host + '/api/me/devices/ws');
        } catch (e) {
            return;
        }
        socket.addEventListener('open', function () { retryDelay = 2000; });
        socket.addEventListener('message', function (event) {
            var data;
            try { data = JSON.parse(event.data); } catch (e) { return; }
            if (data && data.type === 'transfers_updated' && typeof data.pending_count === 'number') {
                setTransferCount(data.pending_count);
            } else if (data && data.type === 'sent_transfers_updated') {
                // A transfer this user sent was created or answered; no badge
                // change, but an open transfers page refreshes its history.
                window.dispatchEvent(new CustomEvent('sendly:sent-transfers-updated', { detail: { fileId: data.file_id, status: data.status } }));
            }
        });
        socket.addEventListener('close', function () {
            // Reconnect with backoff, and resync in case we missed an event.
            setTimeout(function () { connectSocket(); refreshCount(); }, retryDelay);
            retryDelay = Math.min(retryDelay * 2, 30000);
        });
    }

    document.addEventListener('visibilitychange', function () {
        if (document.visibilityState === 'visible') refreshCount();
    });

    window.SendlyTransfers = { refreshCount: refreshCount };
    refreshCount();
    connectSocket();
}());
