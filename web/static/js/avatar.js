(function () {
    'use strict';

    window.buildInitialsAvatar = function (username, size) {
        username = (username || '?').trim();
        size = size || 24;
        var initials = username.split(/\s+/).map(function (word) {
            return word[0];
        }).join('').toUpperCase().slice(0, 2);
        var hash = 0;
        for (var i = 0; i < username.length; i++) {
            hash = username.charCodeAt(i) + ((hash << 5) - hash);
        }
        var palette = [
            '#004AAD', '#006B3F', '#8B0000', '#6B3A00', '#4B0082',
            '#800040', '#005A7A', '#2E6B2E', '#6B4000', '#4A006B'
        ];
        var span = document.createElement('span');
        span.textContent = initials;
        span.style.cssText = 'display:flex;align-items:center;justify-content:center;width:' + size + 'px;height:' + size + 'px;border-radius:50%;font-size:' + Math.round(size * 0.44) + 'px;font-weight:600;color:#fff;flex-shrink:0;background:' + palette[Math.abs(hash) % palette.length] + ';';
        return span;
    };

    // buildUserAvatar renders a user's avatar image, falling back to the
    // initials avatar when there is no URL or the image fails to load.
    window.buildUserAvatar = function (username, avatarUrl, size) {
        size = size || 24;
        if (!avatarUrl) return window.buildInitialsAvatar(username, size);
        var img = document.createElement('img');
        img.src = avatarUrl;
        img.alt = '';
        img.width = size;
        img.height = size;
        img.loading = 'lazy';
        img.style.cssText = 'width:' + size + 'px;height:' + size + 'px;border-radius:50%;object-fit:cover;display:block;flex-shrink:0;';
        img.addEventListener('error', function () {
            img.replaceWith(window.buildInitialsAvatar(username, size));
        }, { once: true });
        return img;
    };
}());
