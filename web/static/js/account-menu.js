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
}());
