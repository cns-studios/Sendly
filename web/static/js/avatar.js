(function () {
    'use strict';

    window.buildInitialsAvatar = function (username) {
        username = (username || '?').trim();
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
        span.style.cssText = 'display:flex;align-items:center;justify-content:center;width:24px;height:24px;border-radius:50%;font-size:11px;font-weight:600;color:#fff;background:' + palette[Math.abs(hash) % palette.length] + ';';
        return span;
    };
}());
