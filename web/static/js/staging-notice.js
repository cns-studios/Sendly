/*
 * staging notice: a floating warning shown on every host except production.
 * dismissing it is remembered in this browser, so it never comes back.
 * self-contained (injects its own styles), no dependencies.
 */
(function () {
  'use strict';

  // the production domain: this host and all of its subdomains are "live".
  var PRODUCTION_DOMAIN = 'sendly.zip';
  // where people on a pre-release copy should go instead.
  var STABLE_URL = 'https://sendly.zip';
  var DISMISSED_KEY = 'cns_staging_notice_dismissed';

  var TEXT = {
    en: {
      badge: 'Pre-release',
      title: 'This service is not intended for use!',
      body: 'Only proceed using this service if you’re a developer. This service may break, lose data unexpectedly or change. Please close this tab and stop using it. Any legal bindings are NOT in effect and DO NOT apply to this pre-release service. No terms of service, privacy policy, or service-level guarantees are valid or enforceable until on this service. Use is entirely at your own risk, and no warranty of any kind, express or implied, is given for availability, data integrity, or fitness for any purpose.',
      stable: 'Please use the stable version',
      link: 'here',
      dismiss: 'I’m a developer, dismiss',
      close: 'Dismiss notice'
    },
    de: {
      badge: 'Vorabversion',
      title: 'Dieser Dienst ist nicht zur Nutzung vorgesehen!',
      body: 'Nutze diesen Dienst nur, wenn du Entwickler:in bist. Dieser Dienst kann ausfallen, unerwartet Daten verlieren oder sich ändern. Bitte schließe diesen Tab und nutze ihn nicht weiter. Rechtliche Bindungen sind NICHT in Kraft und gelten NICHT für diese Vorabversion. Weder Nutzungsbedingungen, Datenschutzerklärung noch Service-Level-Zusagen sind für diesen Dienst gültig oder durchsetzbar. Die Nutzung erfolgt ausschließlich auf eigenes Risiko; es wird keinerlei Gewährleistung übernommen, weder ausdrücklich noch stillschweigend, insbesondere nicht für Verfügbarkeit, Datenintegrität oder Eignung für einen bestimmten Zweck.',
      stable: 'Bitte nutze die stabile Version',
      link: 'hier',
      dismiss: 'Ich bin Entwickler:in, ausblenden',
      close: 'Hinweis schließen'
    }
  };

  /** true on the production domain and any of its subdomains; everything else is pre-release. */
  function isProductionHost(hostname) {
    var host = String(hostname || '').toLowerCase().replace(/\.$/, '');
    return host === PRODUCTION_DOMAIN || host.slice(-(PRODUCTION_DOMAIN.length + 1)) === '.' + PRODUCTION_DOMAIN;
  }

  // loaded by the unit tests under node: expose the host check and stop there.
  if (typeof document === 'undefined') {
    if (typeof module !== 'undefined' && module.exports) module.exports = { isProductionHost: isProductionHost };
    return;
  }

  function isDismissed() {
    try {
      return window.localStorage.getItem(DISMISSED_KEY) !== null;
    } catch (e) {
      return false; // storage blocked: still warn, rather than hide the warning.
    }
  }

  function remember() {
    try {
      window.localStorage.setItem(DISMISSED_KEY, new Date().toISOString());
    } catch (e) {
      /* can't remember it; it will show again next time. */
    }
  }

  if (isProductionHost(window.location.hostname) || isDismissed()) return;

  var CSS = [
    '.cns-staging{all:initial;box-sizing:border-box;position:fixed;z-index:2147483000;left:12px;right:12px;bottom:12px;max-height:calc(100vh - 24px);max-height:calc(100dvh - 24px);overflow-y:auto;display:block;background:#ffd400;color:#000;border:2px solid #000;box-shadow:6px 6px 0 0 #000;font-family:system-ui,-apple-system,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;line-height:1.35;text-align:left;animation:cns-staging-in .3s ease-out}',
    '.cns-staging *{box-sizing:border-box;margin:0;letter-spacing:normal;text-transform:none;text-shadow:none}',
    '.cns-staging__stripe{height:16px;background:repeating-linear-gradient(-45deg,#000 0 12px,#ffd400 12px 24px)}',
    '.cns-staging__inner{display:flex;flex-direction:column;gap:16px;padding:20px}',
    '.cns-staging__head{display:flex;align-items:flex-start;gap:16px}',
    '.cns-staging__icon{flex:none;width:32px;height:32px;margin-top:4px}',
    '.cns-staging__titles{flex:1;min-width:0}',
    '.cns-staging__badge{display:block;font:700 12px ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;letter-spacing:.18em!important;text-transform:uppercase!important}',
    '.cns-staging__title{display:block;font-size:24px;line-height:1.15;font-weight:800}',
    '.cns-staging__x{flex:none;appearance:none;-webkit-appearance:none;display:grid;place-items:center;width:40px;height:40px;margin:-4px -4px 0 0;padding:0;background:transparent;color:#000;border:2px solid transparent;border-radius:0;cursor:pointer}',
    '.cns-staging__x:hover,.cns-staging__x:focus-visible{border-color:#000;outline:none}',
    '.cns-staging__x svg{width:24px;height:24px}',
    '.cns-staging__body{font-size:16px;font-weight:700}',
    '.cns-staging__stable{font-size:16px;font-weight:500}',
    '.cns-staging__stable a{color:#000;font-weight:800;text-decoration:underline;text-underline-offset:2px}',
    '.cns-staging__stable a:hover,.cns-staging__stable a:focus-visible{background:#000;color:#ffd400;outline:none}',
    '.cns-staging__btn{appearance:none;-webkit-appearance:none;display:block;width:100%;padding:12px 16px;background:#000;color:#ffd400;border:2px solid #000;border-radius:0;font:700 14px ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;letter-spacing:.04em;text-transform:uppercase;cursor:pointer}',
    '.cns-staging__btn:hover{background:transparent;color:#000}',
    '.cns-staging__btn:focus-visible{outline:2px solid #000;outline-offset:2px}',
    '@media (min-width:640px){',
    '.cns-staging{left:24px;right:24px;bottom:24px;max-width:56rem;margin:0 auto}',
    '.cns-staging__inner{padding:32px}',
    '.cns-staging__icon{width:40px;height:40px}',
    '.cns-staging__badge{font-size:14px}',
    '.cns-staging__title{font-size:36px}',
    '.cns-staging__body,.cns-staging__stable{font-size:18px}',
    '}',
    '@keyframes cns-staging-in{from{opacity:0;transform:translateY(16px)}to{opacity:1;transform:none}}',
    '@media (prefers-reduced-motion:reduce){.cns-staging{animation:none}}'
  ].join('\n');

  var SVG_NS = 'http://www.w3.org/2000/svg';

  function svg(paths, strokeWidth) {
    var el = document.createElementNS(SVG_NS, 'svg');
    el.setAttribute('viewBox', '0 0 24 24');
    el.setAttribute('fill', 'none');
    el.setAttribute('stroke', 'currentColor');
    el.setAttribute('stroke-width', String(strokeWidth));
    el.setAttribute('stroke-linecap', 'round');
    el.setAttribute('stroke-linejoin', 'round');
    el.setAttribute('aria-hidden', 'true');
    paths.forEach(function (d) {
      var p = document.createElementNS(SVG_NS, 'path');
      p.setAttribute('d', d);
      el.appendChild(p);
    });
    return el;
  }

  function el(tag, className, text) {
    var node = document.createElement(tag);
    if (className) node.className = className;
    if (text) node.textContent = text;
    return node;
  }

  function build() {
    var lang = (document.documentElement.lang || navigator.language || 'en').slice(0, 2).toLowerCase();
    var t = TEXT[lang] || TEXT.en;

    var style = document.createElement('style');
    style.textContent = CSS;

    var root = el('aside', 'cns-staging');
    root.setAttribute('role', 'alertdialog');
    root.setAttribute('aria-labelledby', 'cns-staging-title');
    root.setAttribute('aria-describedby', 'cns-staging-body');
    root.setAttribute('lang', TEXT[lang] ? lang : 'en');

    root.appendChild(el('div', 'cns-staging__stripe')).setAttribute('aria-hidden', 'true');

    var inner = el('div', 'cns-staging__inner');
    var head = el('div', 'cns-staging__head');

    var icon = svg(['M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z', 'M12 9v4', 'M12 17h.01'], 2.5);
    icon.setAttribute('class', 'cns-staging__icon');
    head.appendChild(icon);

    var titles = el('div', 'cns-staging__titles');
    titles.appendChild(el('span', 'cns-staging__badge', t.badge));
    var title = el('div', 'cns-staging__title', t.title);
    title.id = 'cns-staging-title';
    title.setAttribute('role', 'heading');
    title.setAttribute('aria-level', '2');
    titles.appendChild(title);
    head.appendChild(titles);

    var x = el('button', 'cns-staging__x');
    x.type = 'button';
    x.setAttribute('aria-label', t.close);
    x.appendChild(svg(['M18 6 6 18', 'm6 6 12 12'], 3));
    head.appendChild(x);
    inner.appendChild(head);

    var body = el('p', 'cns-staging__body', t.body);
    body.id = 'cns-staging-body';
    inner.appendChild(body);

    var stable = el('p', 'cns-staging__stable', t.stable + ' ');
    var link = el('a', '', t.link);
    link.href = STABLE_URL;
    stable.appendChild(link);
    inner.appendChild(stable);

    var button = el('button', 'cns-staging__btn', t.dismiss);
    button.type = 'button';
    inner.appendChild(button);

    root.appendChild(inner);

    function close() {
      remember();
      root.remove();
      style.remove();
      window.removeEventListener('storage', onStorage);
    }
    // dismissed in another tab or window of this site: hide it here too.
    function onStorage(event) {
      if (event.key === DISMISSED_KEY || event.key === null) {
        if (isDismissed()) close();
      }
    }
    x.addEventListener('click', close);
    button.addEventListener('click', close);
    window.addEventListener('storage', onStorage);

    document.head.appendChild(style);
    document.body.appendChild(root);
  }

  if (document.body) build();
  else document.addEventListener('DOMContentLoaded', build);
})();
