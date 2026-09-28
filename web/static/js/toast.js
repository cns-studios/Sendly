// Toast notifications shared by every page: short messages that slide in at
// the top, stack, close on their own, and can be closed with the button or
// swiped away.
//
// Errors go through SendlyToast.fail(error, text): a message that is already
// meant for people (one of the translated strings, or a known server error
// code) is shown as is; anything technical becomes the short text plus a
// reference like "#4823", with the details in the console. The reference is
// derived from the error, so the same problem always shows the same number.
const SendlyToast = (function () {
    'use strict';

    const t = (k, d) => window.CONFIG?.t?.[k] || d || k;
    const MAX_VISIBLE = 3;
    const DURATION = { info: 4000, success: 3000, error: 6000 };
    const SWIPE_DISTANCE = 70;

    // Server error codes with a plain-language message.
    const CODE_MESSAGES = {
        RATE_LIMITED: 'toast_rate_limited',
        DOWNLOAD_RATE_LIMITED: 'toast_rate_limited',
        FILE_TOO_LARGE: 'toast_file_too_big',
        INVALID_CODE: 'toast_code_invalid',
        INVALID_CODE_FORMAT: 'toast_code_invalid',
        MISSING_CODE: 'toast_code_invalid',
        TUNNEL_NOT_AVAILABLE: 'toast_code_invalid',
        TUNNEL_EXPIRED: 'toast_code_invalid',
        TUNNEL_ALREADY_ACTIVE: 'toast_tunnel_already_started',
        FILE_NOT_FOUND: 'toast_file_gone',
        FILE_EXPIRED: 'toast_file_gone',
        FILE_DELETED: 'toast_file_gone',
        AUTH_REQUIRED: 'toast_signin_again',
        USER_NOT_FOUND: 'toast_user_not_found',
        DEVICE_NOT_TRUSTED: 'toast_device_approve',
        DEVICE_NOT_AUTHORIZED: 'toast_device_approve',
        RECIPIENT_NOT_READY: 'share_recipient_not_ready',
        ALREADY_REPORTED: 'toast_already_reported'
    };

    const ICONS = {
        info: '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>',
        success: '<circle cx="12" cy="12" r="10"/><path d="m9 12 2 2 4-4"/>',
        error: '<circle cx="12" cy="12" r="10"/><path d="M12 8v4"/><path d="M12 16h.01"/>'
    };

    let stack = null;
    const toasts = [];

    function ensureStack() {
        if (stack && document.body.contains(stack)) return stack;
        stack = document.createElement('section');
        stack.className = 'toast-stack';
        stack.setAttribute('aria-live', 'polite');
        stack.setAttribute('aria-label', t('toast_region', 'Notifications'));
        document.body.appendChild(stack);
        return stack;
    }

    function icon(type) {
        return `<svg class="toast-icon" viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICONS[type] || ICONS.info}</svg>`;
    }

    function build(entry) {
        const el = document.createElement('div');
        el.className = `toast toast-${entry.type}`;
        el.setAttribute('role', entry.type === 'error' ? 'alert' : 'status');
        el.innerHTML = `${icon(entry.type)}<div class="toast-body"><span class="toast-text"></span><span class="toast-ref"></span></div>`
            + `<button type="button" class="toast-close" aria-label="${t('toast_close', 'Close')}">`
            + '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" aria-hidden="true"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>'
            + '</button><span class="toast-timer" aria-hidden="true"></span>';
        el.querySelector('.toast-close').addEventListener('click', () => dismiss(entry));
        el.addEventListener('mouseenter', () => pause(entry));
        el.addEventListener('mouseleave', () => resume(entry));
        el.addEventListener('focusin', () => pause(entry));
        el.addEventListener('focusout', () => resume(entry));
        enableSwipe(entry, el);
        return el;
    }

    function render(entry) {
        entry.el.querySelector('.toast-text').textContent = entry.message;
        const ref = entry.el.querySelector('.toast-ref');
        ref.textContent = entry.ref ? `#${entry.ref}` : '';
        ref.hidden = !entry.ref;
    }

    // ── Timing: paused while hovered, focused or held ──
    function schedule(entry) {
        clearTimeout(entry.timer);
        const timer = entry.el.querySelector('.toast-timer');
        if (!entry.duration) {
            timer.style.display = 'none';
            return;
        }
        entry.remaining = entry.duration;
        timer.style.display = '';
        timer.style.animation = 'none';
        void timer.offsetWidth;
        timer.style.animation = `toast-timer ${entry.duration}ms linear forwards`;
        entry.startedAt = Date.now();
        entry.timer = setTimeout(() => dismiss(entry), entry.remaining);
    }

    function pause(entry) {
        if (!entry.duration || entry.paused) return;
        entry.paused = true;
        clearTimeout(entry.timer);
        entry.remaining = Math.max(800, entry.remaining - (Date.now() - entry.startedAt));
        entry.el.classList.add('is-paused');
    }

    function resume(entry) {
        if (!entry.duration || !entry.paused) return;
        entry.paused = false;
        entry.startedAt = Date.now();
        entry.el.classList.remove('is-paused');
        entry.timer = setTimeout(() => dismiss(entry), entry.remaining);
    }

    // ── Swipe to dismiss: sideways or up ──
    function enableSwipe(entry, el) {
        let startX = 0, startY = 0, dx = 0, dy = 0, dragging = false, pointerId = null, startedAt = 0;

        el.addEventListener('pointerdown', (e) => {
            if (e.button !== 0 || e.target.closest('.toast-close')) return;
            dragging = true;
            pointerId = e.pointerId;
            startX = e.clientX;
            startY = e.clientY;
            dx = dy = 0;
            startedAt = Date.now();
            pause(entry);
        });
        el.addEventListener('pointermove', (e) => {
            if (!dragging || e.pointerId !== pointerId) return;
            dx = e.clientX - startX;
            dy = Math.min(0, e.clientY - startY);
            if (Math.abs(dx) > 6 || dy < -6) {
                if (!el.hasPointerCapture(pointerId)) el.setPointerCapture(pointerId);
                el.classList.add('is-dragging');
                const distance = Math.max(Math.abs(dx), Math.abs(dy));
                el.style.transform = `translate(${dx}px, ${dy}px)`;
                el.style.opacity = String(Math.max(0.2, 1 - distance / 220));
            }
        });
        const end = (e) => {
            if (!dragging || e.pointerId !== pointerId) return;
            dragging = false;
            el.classList.remove('is-dragging');
            const elapsed = Math.max(1, Date.now() - startedAt);
            const fast = Math.max(Math.abs(dx), Math.abs(dy)) / elapsed > 0.5;
            if (Math.abs(dx) > SWIPE_DISTANCE || dy < -SWIPE_DISTANCE / 2 || (fast && (Math.abs(dx) > 20 || dy < -20))) {
                entry.swiped = true;
                dismiss(entry, Math.abs(dx) > Math.abs(dy) ? (dx > 0 ? 'right' : 'left') : 'up');
                return;
            }
            el.style.transform = '';
            el.style.opacity = '';
            resume(entry);
        };
        el.addEventListener('pointerup', end);
        el.addEventListener('pointercancel', end);
    }

    function dismiss(entry, direction = 'up') {
        if (!entry || entry.closing) return;
        entry.closing = true;
        clearTimeout(entry.timer);
        const index = toasts.indexOf(entry);
        if (index !== -1) toasts.splice(index, 1);
        const el = entry.el;
        // A swiped toast leaves from where it was let go.
        if (!entry.swiped) {
            el.style.transform = '';
            el.style.opacity = '';
        }
        // Let the stack close the gap once the toast has slid out.
        el.style.setProperty('--toast-h', `${el.offsetHeight}px`);
        el.style.maxHeight = `${el.offsetHeight}px`;
        el.classList.add('is-leaving', `leave-${direction}`);
        const remove = () => el.remove();
        el.addEventListener('animationend', remove, { once: true });
        setTimeout(remove, 600);
    }

    function show(message, options = {}) {
        if (!message) return null;
        const type = ICONS[options.type] ? options.type : 'info';
        const duration = options.duration ?? DURATION[type];
        const container = ensureStack();

        // The same message again (or the same id) refreshes the visible toast.
        const existing = toasts.find((entry) => (options.id && entry.id === options.id)
            || (!options.id && entry.message === message && entry.type === type));
        if (existing) {
            existing.message = message;
            existing.ref = options.ref || '';
            existing.duration = duration;
            if (existing.type !== type) {
                existing.el.classList.replace(`toast-${existing.type}`, `toast-${type}`);
                existing.el.querySelector('.toast-icon').outerHTML = icon(type);
                existing.type = type;
            }
            render(existing);
            if (!options.id) {
                existing.el.classList.remove('is-bumped');
                void existing.el.offsetWidth;
                existing.el.classList.add('is-bumped');
            }
            schedule(existing);
            return handle(existing);
        }

        const entry = { id: options.id || '', message, type, ref: options.ref || '', duration };
        entry.el = build(entry);
        render(entry);
        toasts.push(entry);
        container.appendChild(entry.el);
        schedule(entry);
        while (toasts.length > MAX_VISIBLE) dismiss(toasts[0]);
        return handle(entry);
    }

    function handle(entry) {
        return {
            update: (message, options = {}) => show(message, { ...options, id: entry.id || options.id, type: options.type || entry.type }),
            dismiss: () => dismiss(entry)
        };
    }

    // ── Errors ──
    function isPeopleFacing(message) {
        const strings = window.CONFIG?.t;
        if (!message || !strings) return false;
        return Object.values(strings).includes(message);
    }

    function isNetworkError(error, message) {
        if (typeof navigator !== 'undefined' && navigator.onLine === false) return true;
        return error instanceof TypeError && /fetch|network|load failed/i.test(message);
    }

    // A stable four-digit reference for an error (FNV-1a over its text).
    function referenceFor(text) {
        let hash = 0x811c9dc5;
        for (let i = 0; i < text.length; i++) {
            hash ^= text.charCodeAt(i);
            hash = Math.imul(hash, 0x01000193);
        }
        return String(1000 + ((hash >>> 0) % 9000));
    }

    function describe(error, text) {
        const message = typeof error === 'string' ? error : (error?.message || '');
        if (error?.friendly || isPeopleFacing(message)) return { message };
        const codeKey = CODE_MESSAGES[error?.code];
        if (codeKey) return { message: t(codeKey) };
        if (isNetworkError(error, message)) return { message: t('toast_offline') };
        const ref = referenceFor(`${error?.code || ''}|${message || String(error)}`);
        return { message: text || t('toast_error_generic'), ref };
    }

    function fail(error, text) {
        const described = describe(error, text);
        if (described.ref) {
            console.error(`[Sendly #${described.ref}]`, text || '', error);
        }
        return show(described.message, { type: 'error', ref: described.ref });
    }

    // An Error carrying the server's error code, for fail() to recognise.
    function apiError(payload, fallback) {
        const error = new Error(payload?.error || fallback || 'Request failed');
        if (payload?.code) error.code = payload.code;
        return error;
    }

    // An Error whose message is meant for people and is shown as is.
    function userError(message) {
        const error = new Error(message);
        error.friendly = true;
        return error;
    }

    return {
        show,
        info: (message, options) => show(message, { ...options, type: 'info' }),
        success: (message, options) => show(message, { ...options, type: 'success' }),
        error: (message, options) => show(message, { ...options, type: 'error' }),
        fail,
        apiError,
        userError,
        dismiss: (id) => dismiss(toasts.find((entry) => entry.id === id))
    };
})();

window.SendlyToast = SendlyToast;
