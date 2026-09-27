(function () {
    'use strict';

    var menu = document.getElementById('mobile-menu');
    var openBtn = document.getElementById('mobile-menu-open');
    if (!menu || !openBtn) return;

    var reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
    var closeTimer = null;

    function setOpen(open) {
        clearTimeout(closeTimer);
        if (open) {
            menu.classList.remove('closing');
            menu.classList.add('open');
        } else if (menu.classList.contains('open')) {
            menu.classList.add('closing');
            closeTimer = setTimeout(function () {
                menu.classList.remove('open', 'closing');
            }, reduceMotion.matches ? 0 : 200);
        }
        document.body.classList.toggle('mobile-menu-open', open);
        openBtn.setAttribute('aria-expanded', open ? 'true' : 'false');
        if (open) {
            var closeBtn = menu.querySelector('[data-mobile-menu-close]');
            if (closeBtn) closeBtn.focus();
        } else if (menu.contains(document.activeElement)) {
            openBtn.focus();
        }
    }

    openBtn.addEventListener('click', function () { setOpen(true); });
    menu.querySelectorAll('[data-mobile-menu-close]').forEach(function (el) {
        el.addEventListener('click', function () { setOpen(false); });
    });
    document.addEventListener('keydown', function (e) {
        if (e.key === 'Escape' && menu.classList.contains('open')) setOpen(false);
    });
    window.matchMedia('(min-width: 769px)').addEventListener('change', function (e) {
        if (e.matches) setOpen(false);
    });
}());
