// Shared page bootstrap. Replaces the inline scripts that previously lived in
// every template so the site can run under a strict script-src 'self' CSP.
// Must be loaded (synchronously) at the end of <body>, before app scripts, so
// window.CONFIG is available to them.
(function () {
    'use strict';

    var cfgEl = document.getElementById('page-config');
    if (cfgEl) {
        try {
            window.CONFIG = JSON.parse(cfgEl.textContent);
        } catch (e) {
            window.CONFIG = {};
        }
    }

    function initIcons() {
        if (window.lucide && window.lucide.createIcons) {
            window.lucide.createIcons();
        } else if (window.lucide && window.lucide.replace) {
            window.lucide.replace();
        }
    }

    function fitHeader() {
        if (window.innerWidth <= 768) return;
        var h = document.querySelector('.header');
        if (h && h.scrollWidth > h.clientWidth) {
            h.style.width = (h.scrollWidth + 28) + 'px';
            h.style.minWidth = 'auto';
        }
    }

    function initQuickshareCard() {
        var card = document.getElementById('quickshare-card');
        if (!card) return;
        function toggle(open) {
            card.classList.toggle('expanded', open);
            card.setAttribute('aria-expanded', open ? 'true' : 'false');
        }
        card.addEventListener('click', function (e) {
            if (e.target.closest('.quick-share-actions')) return;
            if (card.classList.contains('expanded')) { toggle(false); return; }
            e.preventDefault();
            toggle(true);
        });
        card.addEventListener('keydown', function (e) {
            if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                toggle(!card.classList.contains('expanded'));
            }
        });
    }

    function showOldBrowserNudge() {
        var nudge = document.getElementById('browser-old-nudge');
        if (!nudge) return;
        var ua = navigator.userAgent, old = false, m;
        if (/MSIE|Trident/.test(ua)) old = true;
        else if (!/Edg/.test(ua) && (m = /Chrome\/(\d+)/.exec(ua)) && parseInt(m[1], 10) < 80) old = true;
        else if ((m = /Firefox\/(\d+)/.exec(ua)) && parseInt(m[1], 10) < 74) old = true;
        else if ((m = /Version\/(\d+\.?\d*).*Safari/.exec(ua)) && parseFloat(m[1]) < 13.1) old = true;
        if (old) nudge.classList.remove('hidden');
    }

    // Replaces the inline onerror handlers on avatar images: error events do
    // not bubble, so listen in the capture phase.
    document.addEventListener('error', function (e) {
        var img = e.target;
        if (!img || img.tagName !== 'IMG' || !img.hasAttribute('data-avatar-fallback')) return;
        if (typeof window.buildInitialsAvatar !== 'function') return;
        var size = parseInt(img.getAttribute('width'), 10) || 24;
        img.replaceWith(window.buildInitialsAvatar(img.getAttribute('data-username'), size));
    }, true);

    function hydrateAvatarPlaceholders() {
        if (typeof window.buildInitialsAvatar !== 'function') return;
        document.querySelectorAll('.account-menu-avatar-placeholder').forEach(function (el) {
            var holder = el.closest('[data-username]');
            var username = holder ? holder.dataset.username : '';
            if (username) el.replaceWith(window.buildInitialsAvatar(username));
        });
    }

    function onReady() {
        initIcons();
        hydrateAvatarPlaceholders();
    }
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', onReady);
    } else {
        onReady();
    }

    fitHeader();
    initQuickshareCard();
    showOldBrowserNudge();
})();
