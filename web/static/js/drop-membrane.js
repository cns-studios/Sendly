// drop zone · membrane
//
// A vanilla-JS port of the "drop zone · membrane" component from hairline UI
// (https://hairlineui.com/ui/drop-zone-membrane, MIT, © 2026 hairline UI),
// adapted to sendly's colours and to touch screens. styles live in
// /static/css/drop-membrane.css.
//
// the zone's border is a membrane: drag files over the page and the nearest
// stretch of line bends out to meet them, holds them, swallows them on drop and
// hands each one on to a row underneath that carries its own progress hairline.
//
//   const zone = SendlyDropMembrane.create(hostElement, {
//     title, hint,                 // resting copy
//     mobileTitle, mobileHint,     // resting copy on touch devices (tap, not drop)
//     multiple,                    // false: a new drop replaces the current file
//     validate(file),              // return a reason string to refuse a file
//     locked(),                    // true while the page can't take another file
//     onUpload(file, ctx),         // ctx: { progress(0..1), note(text), signal, row }
//     onDownload(entry, ctx),      // makes finished rows downloadable; ctx: { progress(0..1) }
//     formatSize(bytes),
//     t(key),                      // the page's translations: dropzone_release, dropzone_added, ...
//   });
//   zone.sync(entries); zone.clear();
//
// onUpload returns a promise: resolve when done, throw to show the error with
// retry. its signal is aborted when the person presses the row's X, and the
// upload code is expected to stop sending. the row handle (ctx.row) can also
// settle things from outside the promise: row.fail(message),
// row.setCancelable(false), row.adopt(fileId).
//
// zone.sync([{ id, name, size, type }]) turns a shared list of files (the same
// for everyone in a session) into rows below the zone. A local upload that has
// been given its file id with row.adopt() is the same row as its entry.
// with onDownload set, finished rows swap the X for a download button.
(function () {
    'use strict';

    // ── membrane physics ────────────────────────────────────────────────
    // the border is a ring of points round a rounded rectangle, each free to move along its outward
    // normal. every point is held to its goal by a spring and to its neighbours by tension, so a pull in
    // one place makes a smooth tent and a release sends a ripple both ways round the edge. one frame loop
    // integrates it and writes the path; it sleeps when the line is still, and a line at rest is drawn as
    // an exact rounded rectangle.

    const STEP = 1 / 240;
    const SPRING = 220;      // toward the goal
    const TENSION = 15000;   // toward the neighbours: a pull makes a tent, a release travels as a ripple
    const DRAG = 6.5;
    const VISCOSITY = 58;    // damping between neighbours, so short wobbles die before long ones
    const SPACING = 5;       // px of border per point
    const REACH = 200;       // how far out a file starts to pull
    const PULL = 44;         // goal at the line itself; tension spreads it, so the line reaches about half of it
    const SLIP = 32;         // how far a carried file drags the line before it slips off

    const round = (n) => Math.round(n * 100) / 100;

    // the exact rest shape, on half pixels so the line is crisp.
    function restPath(w, h, radius) {
        const x0 = 0.5, y0 = 0.5, x1 = w - 0.5, y1 = h - 0.5;
        const r = Math.max(0, Math.min(radius - 0.5, (x1 - x0) / 2, (y1 - y0) / 2));
        return `M${x0 + r},${y0}H${x1 - r}A${r},${r} 0 0 1 ${x1},${y0 + r}V${y1 - r}A${r},${r} 0 0 1 ${x1 - r},${y1}H${x0 + r}A${r},${r} 0 0 1 ${x0},${y1 - r}V${y0 + r}A${r},${r} 0 0 1 ${x0 + r},${y0}Z`;
    }

    // points spaced evenly along the rounded rectangle, with their outward normals.
    function buildRing(w, h, radius) {
        const x0 = 0.5, y0 = 0.5, x1 = w - 0.5, y1 = h - 0.5;
        const r = Math.max(0, Math.min(radius - 0.5, (x1 - x0) / 2, (y1 - y0) / 2));
        const sw = x1 - x0 - 2 * r, sh = y1 - y0 - 2 * r, arc = (Math.PI * r) / 2;
        const length = 2 * sw + 2 * sh + 4 * arc;
        const count = Math.max(48, Math.min(360, Math.round(length / SPACING)));
        const ring = {
            count, w, h, r, radius,
            bx: new Float32Array(count), by: new Float32Array(count),
            nx: new Float32Array(count), ny: new Float32Array(count),
            u: new Float32Array(count), v: new Float32Array(count),
            goal: new Float32Array(count), acc: new Float32Array(count),
        };
        const corner = (cx, cy, a) => {
            const c = Math.cos(a), s = Math.sin(a);
            return [cx + c * r, cy + s * r, c, s];
        };
        // edges and corners clockwise from the top-left end of the top edge.
        const parts = [
            { len: sw, at: (t) => [x0 + r + t * sw, y0, 0, -1] },
            { len: arc, at: (t) => corner(x1 - r, y0 + r, -Math.PI / 2 + (t * Math.PI) / 2) },
            { len: sh, at: (t) => [x1, y0 + r + t * sh, 1, 0] },
            { len: arc, at: (t) => corner(x1 - r, y1 - r, (t * Math.PI) / 2) },
            { len: sw, at: (t) => [x1 - r - t * sw, y1, 0, 1] },
            { len: arc, at: (t) => corner(x0 + r, y1 - r, Math.PI / 2 + (t * Math.PI) / 2) },
            { len: sh, at: (t) => [x0, y1 - r - t * sh, -1, 0] },
            { len: arc, at: (t) => corner(x0 + r, y0 + r, Math.PI + (t * Math.PI) / 2) },
        ];
        for (let i = 0; i < count; i++) {
            let s = (i / count) * length;
            let k = 0;
            while (k < parts.length - 1 && s > parts[k].len) s -= parts[k++].len;
            const [x, y, nx, ny] = parts[k].at(parts[k].len ? Math.min(1, s / parts[k].len) : 0);
            ring.bx[i] = x; ring.by[i] = y; ring.nx[i] = nx; ring.ny[i] = ny;
        }
        return ring;
    }

    function createMembrane({ getRadius, isReduced }) {
        let box = null, path = null, arrow = null, ring = null, observer = null;
        let frame = 0, last = 0, swellPx = 0;
        const forces = new Map();
        const turn = { angle: 0, lean: 0, x: 0, y: 0 };

        function draw(rest) {
            if (!ring || !path) return;
            if (rest) { path.setAttribute('d', restPath(ring.w, ring.h, ring.radius)); return; }
            const { count: n, bx, by, nx, ny, u } = ring;
            // quadratic curves through the midpoints: smooth, and straight where the line is straight.
            const px = (i) => bx[i] + nx[i] * u[i];
            const py = (i) => by[i] + ny[i] * u[i];
            let d = `M${round((px(n - 1) + px(0)) / 2)},${round((py(n - 1) + py(0)) / 2)}`;
            for (let i = 0; i < n; i++) {
                const j = i === n - 1 ? 0 : i + 1;
                d += `Q${round(px(i))},${round(py(i))} ${round((px(i) + px(j)) / 2)},${round((py(i) + py(j)) / 2)}`;
            }
            path.setAttribute('d', `${d}Z`);
        }

        function tick(now) {
            frame = 0;
            const g = ring;
            if (!g) return;
            const dt = last ? Math.min(0.05, (now - last) / 1000) : 1 / 60;
            last = now;
            const { count: n, w, h, bx, by, nx, ny, u, v, goal, acc } = g;

            // goals: each force bends the stretch of line nearest it; where two overlap the stronger wins.
            goal.fill(0);
            let pointer = null;
            for (const f of forces.values()) {
                if (f.kind !== 'carry') pointer = f;
                const sigma = f.spread || 38, two = 2 * sigma * sigma, far = 9 * sigma * sigma;
                const inside = f.x > 0 && f.x < w && f.y > 0 && f.y < h;
                for (let i = 0; i < n; i++) {
                    const dx = f.x - bx[i], dy = f.y - by[i];
                    const s = dx * nx[i] + dy * ny[i];
                    const lateral = dx * dx + dy * dy - s * s;
                    if (lateral > far || (!inside && s < -6)) continue;
                    const fall = Math.exp(-lateral / two);
                    let want;
                    if (f.kind === 'reach') want = inside ? 10 * Math.exp(s / 28) : s < REACH ? PULL * Math.pow(1 - Math.max(0, s) / REACH, 2) : 0;
                    else if (f.kind === 'recoil') want = inside ? -7 * Math.exp(s / 28) : s < REACH ? -12 * Math.pow(1 - Math.max(0, s) / REACH, 2) : 0;
                    else want = inside ? 18 * Math.exp(s / 22) : s < SLIP ? (s + 6) * 1.6 : 0;
                    want *= fall;
                    if (Math.abs(want) > Math.abs(goal[i])) goal[i] = want;
                }
                // crossing into the zone releases the stretched line: kick it back so it snaps rather than eases.
                if (f.kind === 'reach' && inside && !f.inside) {
                    for (let i = 0; i < n; i++) {
                        const dx = f.x - bx[i], dy = f.y - by[i];
                        v[i] -= 150 * Math.exp(-(dx * dx + dy * dy) / (2 * 60 * 60));
                    }
                }
                f.inside = inside;
            }
            if (swellPx) for (let i = 0; i < n; i++) goal[i] += swellPx;

            // integrate in fixed small steps; the tension is stiff.
            for (let t = 0; t < dt - 1e-6; t += STEP) {
                const h2 = Math.min(STEP, dt - t);
                for (let i = 0; i < n; i++) {
                    const l = i === 0 ? n - 1 : i - 1, r = i === n - 1 ? 0 : i + 1;
                    acc[i] = SPRING * (goal[i] - u[i]) + TENSION * (u[l] + u[r] - 2 * u[i]) - DRAG * v[i] + VISCOSITY * (v[l] + v[r] - 2 * v[i]);
                }
                for (let i = 0; i < n; i++) {
                    v[i] += acc[i] * h2;
                    u[i] += v[i] * h2;
                }
            }

            let moving = false, flat = true;
            for (let i = 0; i < n; i++) {
                if (Math.abs(u[i] - goal[i]) > 0.03 || Math.abs(v[i]) > 0.4) moving = true;
                if (Math.abs(goal[i]) > 0.001 || Math.abs(u[i]) > 0.03) flat = false;
            }
            if (!moving && flat) { u.fill(0); v.fill(0); }
            draw(!moving && flat);

            // the arrow watches a file outside, points into the zone once it is over it, and rests pointing up.
            let angle = 0, lean = 0;
            if (pointer) {
                const cx = w / 2, cy = h / 2, dx = pointer.x - cx, dy = pointer.y - cy;
                angle = pointer.inside ? (pointer.kind === 'reach' ? 180 : 0) : (Math.atan2(dy, dx) * 180) / Math.PI + 90;
                lean = pointer.inside ? 0 : Math.min(4, Math.hypot(dx, dy) / 40);
                turn.x = dx; turn.y = dy;
            }
            const k = 1 - Math.exp(-dt / 0.045);
            let delta = ((angle - turn.angle + 540) % 360) - 180;
            if (Math.abs(delta) < 0.05) delta = 0;
            turn.angle += delta * k;
            turn.lean += (lean - turn.lean) * k;
            if (Math.abs(delta) > 0.05 || Math.abs(lean - turn.lean) > 0.02) moving = true;
            if (arrow) {
                const m = Math.hypot(turn.x, turn.y) || 1;
                arrow.style.transform = `translate3d(${round((turn.x / m) * turn.lean)}px,${round((turn.y / m) * turn.lean)}px,0) rotate(${round(turn.angle)}deg)`;
            }

            if (moving) frame = requestAnimationFrame(tick);
            else last = 0;
        }

        function kick() {
            if (isReduced()) return;
            if (!frame) frame = requestAnimationFrame(tick);
        }

        // build the ring from the box's size and rebuild it when the box resizes.
        function measure() {
            if (!box) return;
            const w = box.offsetWidth, h = box.offsetHeight, radius = getRadius();
            if (!w || !h || (ring && ring.w === w && ring.h === h && ring.radius === radius)) return;
            ring = buildRing(w, h, radius);
            draw(true);
        }

        function impulse(x, y, base, peak, sigma) {
            if (!ring || isReduced()) return;
            for (let i = 0; i < ring.count; i++) {
                const dx = x - ring.bx[i], dy = y - ring.by[i];
                ring.v[i] += base + peak * Math.exp(-(dx * dx + dy * dy) / (2 * sigma * sigma));
            }
            kick();
        }

        return {
            attach(boxEl, pathEl, arrowEl) {
                box = boxEl; path = pathEl; arrow = arrowEl;
                measure();
                if (typeof ResizeObserver !== 'undefined') {
                    observer = new ResizeObserver(measure);
                    observer.observe(box);
                } else {
                    window.addEventListener('resize', measure);
                }
            },
            // reduced motion: no bending. settle everything where it is.
            settle() {
                forces.clear();
                swellPx = 0;
                if (ring) { ring.u.fill(0); ring.v.fill(0); }
                draw(true);
                if (arrow) arrow.style.transform = '';
            },
            // set, move or (with null) remove one force, in the box's own coordinates.
            set(key, force) {
                if (isReduced()) return;
                const prev = forces.get(key);
                if (force) {
                    const w = ring ? ring.w : 0, h = ring ? ring.h : 0;
                    forces.set(key, Object.assign({}, force, { inside: prev ? prev.inside : (force.x > 0 && force.y > 0 && force.x < w && force.y < h) }));
                } else if (prev) forces.delete(key);
                else return;
                kick();
            },
            // push the whole line out (positive) or in by a few pixels.
            swell(px) {
                if (swellPx === px) return;
                swellPx = px;
                kick();
            },
            gulp(x, y) { impulse(x, y, -70, -230, 90); },
            spit(x, y) { impulse(x, y, 0, 320, 64); },
        };
    }

    // ── text that changes the way it should ─────────────────────────────
    // ports of hairline UI's "text morph" and "number roll" (MIT), which the original drop zone uses for its
    // label and its percent. both keep the real text for screen readers and only draw the motion.

    const EASE_OUT = 'cubic-bezier(0.16, 1, 0.3, 1)';

    // keep the letters two texts share (longest common subsequence); everything else is new.
    function matchGlyphs(previous, text, nextKey) {
        const a = previous.map((g) => g.char);
        const b = Array.from(text);
        const fresh = () => b.map((char) => ({ key: nextKey(), char, el: null }));
        if (!a.length || !b.length) return fresh();

        const dp = Array.from({ length: a.length + 1 }, () => new Uint16Array(b.length + 1));
        for (let i = a.length - 1; i >= 0; i--) {
            for (let j = b.length - 1; j >= 0; j--) dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
        }
        // too little in common reads as two different words: crossfade them instead.
        if (dp[0][0] < Math.min(a.length, b.length) * 0.5) return fresh();

        const out = [];
        let i = 0, j = 0;
        while (j < b.length) {
            if (i < a.length && a[i] === b[j]) { out.push(previous[i++]); j++; }
            else if (i < a.length && dp[i + 1][j] >= dp[i][j + 1]) i++;
            else out.push({ key: nextKey(), char: b[j++], el: null });
        }
        return out;
    }

    /**
     * text that morphs: letters both texts share glide to their new place, new letters rise in out of a
     * blur one after another, letters that leave lift away and blur out where they stood, and the box
     * eases to its new width.
     */
    function createTextMorph(host, { duration = 460, rise = 0.35, isReduced }) {
        const root = document.createElement('span');
        root.className = 'dm-morph';
        const sr = document.createElement('span');
        sr.className = 'dm-sr';
        root.appendChild(sr);
        host.appendChild(root);

        let glyphs = [];
        let text = null;
        let keys = 0;
        const nextKey = () => ++keys;

        // words stay unbroken; lines may only wrap at spaces.
        function layout(list) {
            root.querySelectorAll('.dm-word').forEach((w) => w.remove());
            const words = [];
            for (const g of list) {
                if (!g.el) {
                    g.el = document.createElement('span');
                    g.el.className = 'dm-glyph';
                    g.el.setAttribute('aria-hidden', 'true');
                    g.el.textContent = g.char;
                }
                if (g.char === ' ' || !words.length || words[words.length - 1][0].char === ' ') words.push([g]);
                else words[words.length - 1].push(g);
            }
            for (const word of words) {
                const w = document.createElement('span');
                w.className = 'dm-word';
                w.setAttribute('aria-hidden', 'true');
                word.forEach((g) => w.appendChild(g.el));
                root.appendChild(w);
            }
        }

        function set(next) {
            if (next === text) return;
            const first = text === null;
            text = next;
            sr.textContent = next;
            if (first || isReduced() || !root.animate) {
                glyphs = Array.from(next).map((char) => ({ key: nextKey(), char, el: null }));
                root.querySelectorAll('.dm-glyph-out').forEach((g) => g.remove());
                layout(glyphs);
                return;
            }

            // 1. note where every letter stands, then work out which letters stay.
            const box = root.getBoundingClientRect();
            const at = new Map();
            glyphs.forEach((g) => {
                const r = g.el.getBoundingClientRect();
                at.set(g.key, { x: r.left - box.left, y: r.top - box.top });
            });
            const previousWidth = box.width;
            const following = matchGlyphs(glyphs, next, nextKey);
            const kept = new Set(following.map((g) => g.key));

            // 2. letters that go lift away and blur out where they stood.
            glyphs.filter((g) => !kept.has(g.key)).forEach((g) => {
                const pos = at.get(g.key) || { x: 0, y: 0 };
                const ghost = document.createElement('span');
                ghost.className = 'dm-glyph-out';
                ghost.setAttribute('aria-hidden', 'true');
                ghost.textContent = g.char;
                ghost.style.left = `${pos.x}px`;
                ghost.style.top = `${pos.y}px`;
                root.appendChild(ghost);
                const out = ghost.animate(
                    [{ opacity: 1, filter: 'blur(0px)', transform: 'none' }, { opacity: 0, filter: 'blur(4px)', transform: `translateY(${-rise}em)` }],
                    { duration: duration * 0.6, easing: EASE_OUT, fill: 'forwards' }
                );
                out.onfinish = () => ghost.remove();
            });

            // 3. the new letters go in place; the ones that stayed glide, the new ones rise out of a blur.
            following.forEach((g) => { if (g.el) g.el.getAnimations().forEach((a) => a.cancel()); });
            glyphs.filter((g) => !kept.has(g.key)).forEach((g) => g.el.remove());
            layout(following);
            glyphs = following;

            const after = root.getBoundingClientRect();
            let order = 0;
            for (const g of following) {
                const from = at.get(g.key);
                if (from) {
                    const r = g.el.getBoundingClientRect();
                    const dx = from.x - (r.left - after.left);
                    const dy = from.y - (r.top - after.top);
                    if (Math.abs(dx) > 0.5 || Math.abs(dy) > 0.5) g.el.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: 'none' }], { duration, easing: EASE_OUT });
                } else {
                    g.el.animate(
                        [{ opacity: 0, filter: 'blur(4px)', transform: `translateY(${rise}em)` }, { opacity: 1, filter: 'blur(0px)', transform: 'none' }],
                        { duration, easing: EASE_OUT, delay: Math.min(order++ * 16, 160), fill: 'backwards' }
                    );
                }
            }
            if (Math.abs(previousWidth - after.width) > 0.5) {
                // hold the words on one line while the box is narrower than them, or they would wrap mid-morph.
                root.style.whiteSpace = 'nowrap';
                const resize = root.animate([{ width: `${previousWidth}px` }, { width: `${after.width}px` }], { duration, easing: EASE_OUT });
                resize.onfinish = resize.oncancel = () => { root.style.whiteSpace = ''; };
            }
        }

        return { set };
    }

    const SETS = 5;                 // the strip repeats 0-9 five times so a roll never runs out of digits
    const MIDDLE = 20;
    const STRIP = Array.from({ length: SETS * 10 }, (_, i) => i % 10).join('\n');

    // digits are keyed by their place counted from the right, so the units column stays the units column;
    // a symbol is keyed by how many of the same symbol sit to its right.
    function columnsOf(text) {
        const chars = Array.from(text);
        const seen = new Map();
        let place = 0;
        const out = [];
        for (let i = chars.length - 1; i >= 0; i--) {
            const char = chars[i];
            if (/\d/.test(char)) out.push({ key: `d${place++}`, char, num: Number(char) });
            else {
                const n = seen.get(char) || 0;
                seen.set(char, n + 1);
                out.push({ key: `s${char}${n}`, char, num: null });
            }
        }
        return out.reverse();
    }

    // one digit: a column that rolls to its new value, in the direction the number moved. going up, a 9
    // rolls on to 0 instead of spinning back. the roll is blurred, most at the start, like a moving drum.
    function createDigit(digit, duration, isReduced) {
        const el = document.createElement('span');
        el.className = 'dm-digit';
        const ghost = document.createElement('span');
        ghost.className = 'dm-digit-ghost';
        ghost.textContent = String(digit);
        const strip = document.createElement('span');
        strip.className = 'dm-strip';
        strip.setAttribute('aria-hidden', 'true');
        strip.textContent = STRIP;
        el.append(ghost, strip);

        let pos = MIDDLE + digit;
        const place = (to, animate) => {
            strip.style.transition = animate ? `transform ${duration}ms ${EASE_OUT}` : 'none';
            strip.style.transform = `translateY(${(-to * 100) / (SETS * 10)}%)`;
        };
        place(pos, false);

        // after a roll, quietly re-centre on the middle set so the next roll always has room.
        strip.addEventListener('transitionend', (e) => {
            if (e.propertyName !== 'transform') return;
            pos = MIDDLE + (((pos % 10) + 10) % 10);
            place(pos, false);
        });

        function roll(next, trend) {
            const showing = ((pos % 10) + 10) % 10;
            if (showing === next) return;
            ghost.textContent = String(next);
            if (isReduced()) { pos = MIDDLE + next; place(pos, false); return; }
            const up = trend > 0 || (trend === 0 && next > showing);
            let target = up ? pos + ((next - showing + 10) % 10) : pos - ((showing - next + 10) % 10);
            // values arriving faster than a roll (a progress percent) never let it re-centre, so before it
            // runs off the strip, jump it back by whole sets mid-roll (it looks identical) and roll on from there.
            if (target < 10 || target > SETS * 10 - 11) {
                const shift = Math.round((target - MIDDLE) / 10) * 10;
                if (strip.offsetHeight) {
                    let visual = pos;
                    try { visual = -new DOMMatrixReadOnly(getComputedStyle(strip).transform).m42 / (strip.offsetHeight / (SETS * 10)); } catch (e) { /* keep the logical position */ }
                    strip.style.transition = 'none';
                    strip.style.transform = `translateY(${(-(visual - shift) * 100) / (SETS * 10)}%)`;
                    void strip.offsetHeight;
                }
                target -= shift;
            }
            pos = target;
            place(target, true);
            strip.getAnimations().filter((a) => a.id === 'dm-blur').forEach((a) => a.cancel());
            const blur = strip.animate(
                [{ filter: 'blur(0px)' }, { filter: 'blur(1.6px)', offset: 0.3 }, { filter: 'blur(0px)' }],
                { duration: Math.min(duration, 520), easing: 'linear' }
            );
            blur.id = 'dm-blur';
        }

        return { el, roll };
    }

    /**
     * A number that changes like an odometer: each digit is a column that rolls to its new value, new places
     * slide open from nothing and lost ones fold away.
     */
    function createNumberRoll(host, { suffix = '', duration = 900, isReduced }) {
        const root = document.createElement('span');
        root.className = 'dm-roll';
        const sr = document.createElement('span');
        sr.className = 'dm-sr';
        const view = document.createElement('span');
        view.className = 'dm-roll-cols';
        view.setAttribute('aria-hidden', 'true');
        root.append(sr, view);
        host.appendChild(root);

        let cols = [];
        let value = null;

        function column(spec, entering) {
            const place = document.createElement('span');
            place.className = 'dm-place';
            let roller = null;
            if (spec.num === null) place.textContent = spec.char;
            else {
                // A place that arrives after the first paint rolls in from 0.
                roller = createDigit(entering ? 0 : spec.num, duration, isReduced);
                place.appendChild(roller.el);
            }
            return Object.assign({ node: place, roller }, spec);
        }

        // A place that appears slides open from nothing; one that goes folds away where it stood.
        function open(node) {
            const width = node.offsetWidth;
            node.style.clipPath = 'inset(0)';
            const grow = node.animate(
                [{ width: '0px', opacity: 0, filter: 'blur(4px)' }, { width: `${width}px`, opacity: 1, filter: 'blur(0px)' }],
                { duration: 500, easing: EASE_OUT }
            );
            grow.onfinish = grow.oncancel = () => { node.style.clipPath = ''; };
        }

        function fold(col, before) {
            const ghost = col.node.cloneNode(true);
            ghost.style.clipPath = 'inset(0)';
            view.insertBefore(ghost, before);
            const out = ghost.animate(
                [{ width: `${col.width}px`, opacity: 1, filter: 'blur(0px)' }, { width: '0px', opacity: 0, filter: 'blur(4px)' }],
                { duration: 400, easing: EASE_OUT, fill: 'forwards' }
            );
            out.onfinish = () => ghost.remove();
        }

        function set(next) {
            if (next === value) return;
            const text = `${next}${suffix}`;
            sr.textContent = text;
            const specs = columnsOf(text);
            const first = value === null;
            const still = first || isReduced();
            const trend = first ? 0 : Math.sign(next - value);
            value = next;

            const before = new Map(cols.map((c) => [c.key, c]));
            const after = new Set(specs.map((s) => s.key));
            const gone = cols.filter((c) => !after.has(c.key));
            gone.forEach((c) => { c.width = c.node.offsetWidth; c.node.remove(); });

            const rolling = [];
            cols = specs.map((spec) => {
                const old = before.get(spec.key);
                if (old) {
                    if (spec.num === null) old.node.textContent = spec.char;
                    else rolling.push([old, spec.num]);
                    return Object.assign(old, spec);
                }
                const created = column(spec, !still);
                created.fresh = true;
                return created;
            });

            // only nodes that are new or out of place move: reparenting an element cancels its running roll.
            let ref = null;
            for (let i = cols.length - 1; i >= 0; i--) {
                const node = cols[i].node;
                if (node.parentNode !== view || node.nextSibling !== ref) view.insertBefore(node, ref);
                ref = node;
            }
            if (!still) {
                gone.forEach((c) => fold(c, view.firstChild));
                cols.forEach((c) => {
                    if (!c.fresh) return;
                    c.fresh = false;
                    open(c.node);
                    if (c.roller) c.roller.roll(c.num, trend);
                });
            }
            rolling.forEach(([c, d]) => c.roller.roll(d, trend));
        }

        return { set };
    }

    // ── component ────────────────────────────────────────────────────────

    const HOLD = 16;      // a file this close outside the zone is already held by the bulge, and drops into it
    const FLIGHT = 500;
    const ease = (t) => 1 - Math.pow(1 - t, 2.8);

    const fmt = (template, vars) => template.replace(/\{(\w+)\}/g, (m, k) => (vars && vars[k] !== undefined ? vars[k] : m));

    // lucide glyphs (ISC), inlined so rows can be built without re-running the icon replacer.
    const ICON = {
        arrowUp: '<path d="m5 12 7-7 7 7"/><path d="M12 19V5"/>',
        x: '<path d="M18 6 6 18"/><path d="m6 6 12 12"/>',
        alert: '<circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/>',
        download: '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" x2="12" y1="15" y2="3"/>',
        retry: '<path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/>',
        file: '<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/>',
        fileText: '<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M10 9H8"/><path d="M16 13H8"/><path d="M16 17H8"/>',
        fileImage: '<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><circle cx="10" cy="12" r="2"/><path d="m20 17-1.296-1.296a2.41 2.41 0 0 0-3.408 0L9 22"/>',
    };
    const icon = (inner, cls, stroke) =>
        `<svg class="${cls}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="${stroke || 1.75}" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${inner}</svg>`;
    const fileGlyph = (type, cls) => icon(type.indexOf('image/') === 0 ? ICON.fileImage : type === 'application/pdf' || type.indexOf('text/') === 0 ? ICON.fileText : ICON.file, cls);

    // the check draws itself: a 16-unit box so its path length can be normalised to 1.
    const drawnCheck = (cls) =>
        `<svg class="${cls}" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3.5 8.5l3 3 6-7" pathLength="1" stroke-dasharray="1"/></svg>`;

    // A shared list only knows names: pick an icon from the extension.
    const guessType = (name) => {
        const ext = (name.split('.').pop() || '').toLowerCase();
        if (/^(png|jpe?g|gif|webp|avif|svg|heic|heif|bmp)$/.test(ext)) return 'image/' + ext;
        if (ext === 'pdf') return 'application/pdf';
        if (/^(txt|md|csv|json|log|rtf)$/.test(ext)) return 'text/' + ext;
        return '';
    };

    let counter = 0;

    function create(host, options) {
        const o = Object.assign({
            hint: '', mobileTitle: '', mobileHint: '', multiple: true,
            validate: null, locked: null, onUpload: null, onDownload: null,
        }, options);
        // every string the zone says comes from the page's translations as dropzone_<key in snake_case>.
        const m = (key, vars) => fmt(o.t('dropzone_' + key.replace(/[A-Z]/g, (c) => '_' + c.toLowerCase())), vars);

        const reducedQuery = window.matchMedia('(prefers-reduced-motion: reduce)');
        const touchQuery = window.matchMedia('(hover: none) and (pointer: coarse)');
        let reduced = reducedQuery.matches;

        const uid = `dm${++counter}`;
        host.classList.add('dm');
        host.dataset.tone = 'rest';
        host.dataset.icon = 'arrow';
        host.innerHTML =
            '<button type="button" class="dm-zone">' +
                '<svg class="dm-line" aria-hidden="true"><path></path></svg>' +
                '<span class="dm-badge" aria-hidden="true">' +
                    `<span class="dm-ico" data-ico="arrow"><span class="dm-arrow">${icon(ICON.arrowUp, 'dm-svg')}</span></span>` +
                    `<span class="dm-ico" data-ico="refuse">${icon(ICON.x, 'dm-svg')}</span>` +
                    `<span class="dm-ico" data-ico="added">${drawnCheck('dm-svg dm-check')}</span>` +
                '</span>' +
                '<span class="dm-text">' +
                    '<span class="dm-label"></span>' +
                    `<span class="dm-hint" id="${uid}-hint"></span>` +
                '</span>' +
            '</button>' +
            '<input type="file" hidden>' +
            `<ul class="dm-rows" aria-label="${m('filesLabel')}"></ul>` +
            '<p class="dm-sr" role="status"></p>';

        const zone = host.querySelector('.dm-zone');
        const input = host.querySelector('input[type="file"]');
        const labelEl = host.querySelector('.dm-label');
        const hintEl = host.querySelector('.dm-hint');
        const list = host.querySelector('.dm-rows');
        const status = host.querySelector('.dm-sr');
        const arrowEl = host.querySelector('.dm-arrow');
        const icons = {
            arrow: host.querySelector('[data-ico="arrow"]'),
            refuse: host.querySelector('[data-ico="refuse"]'),
            added: host.querySelector('[data-ico="added"]'),
        };
        input.multiple = o.multiple;

        const membrane = createMembrane({
            getRadius: () => parseFloat(getComputedStyle(zone).getPropertyValue('--dm-radius')) || 18,
            isReduced: () => reduced,
        });
        membrane.attach(zone, zone.querySelector('path'), arrowEl);

        const rows = [];
        const chips = new Map();
        let drag = null;       // { count, held }
        let notice = null;     // { text, error }
        let noticeTimer = 0;

        const later = (fn, ms) => setTimeout(fn, ms);
        const isLocked = () => !!(o.locked && o.locked());
        const isVisible = () => zone.offsetWidth > 0 && zone.offsetHeight > 0;
        const say = (text) => { status.textContent = ''; status.textContent = text; };

        const label = createTextMorph(labelEl, { isReduced: () => reduced });

        // the zone looks locked while the page can't take a file; the page's state changes without telling us,
        // so this is looked at on every render and whenever a row changes.
        function syncLocked() {
            if (isLocked()) host.setAttribute('data-locked', ''); else host.removeAttribute('data-locked');
        }

        const restTitle = () => (touchQuery.matches && o.mobileTitle) || o.title;
        const restHint = () => (touchQuery.matches && o.mobileHint) || o.hint;

        function render() {
            const refusing = !!(notice && notice.error);
            let words;
            if (drag && drag.locked) {
                words = m('busy');
            } else if (drag) {
                words = drag.held
                    ? (drag.count > 1 ? m('releaseMany', { count: drag.count }) : m('release'))
                    : (drag.count > 1 ? m('dropMany', { count: drag.count }) : m('dropHere'));
            } else words = notice ? notice.text : restTitle();
            label.set(words);
            hintEl.textContent = restHint();
            hintEl.hidden = !restHint();
            if (restHint()) zone.setAttribute('aria-describedby', hintEl.id); else zone.removeAttribute('aria-describedby');

            const blocked = !!(drag && drag.locked);
            const added = !!(notice && !notice.error) && !drag;
            const shown = blocked || (!drag && refusing) ? 'refuse' : added ? 'added' : 'arrow';
            for (const k in icons) icons[k].toggleAttribute('data-on', k === shown);
            host.dataset.icon = shown;
            host.dataset.tone = blocked || (!drag && refusing) ? 'refuse' : drag ? (drag.held ? 'over' : 'armed') : 'rest';
            syncLocked();
        }

        function tell(text, error) {
            clearTimeout(noticeTimer);
            notice = { text, error };
            render();
            noticeTimer = setTimeout(() => { notice = null; render(); }, error ? 2800 : 1800);
        }

        const local = (cx, cy) => {
            const r = zone.getBoundingClientRect();
            return { x: cx - r.left, y: cy - r.top, w: r.width, h: r.height };
        };
        const centre = () => ({ x: zone.offsetWidth / 2, y: zone.offsetHeight / 2 });

        // follow a drag: bend the line toward the file and update the label only when that changes. A zone
        // that can't take a file right now (an upload is running) shrinks away and says why, instead of
        // ignoring the drag.
        function track(cx, cy, count) {
            if (!isVisible()) return;
            const blocked = isLocked();
            const p = local(cx, cy);
            const outside = Math.hypot(Math.max(-p.x, 0, p.x - p.w), Math.max(-p.y, 0, p.y - p.h));
            const isHeld = outside < HOLD;
            membrane.set('pointer', { x: p.x, y: p.y, kind: blocked ? 'recoil' : 'reach' });
            membrane.swell(blocked ? -2 : isHeld ? 3 : 1.5);
            if (!drag || drag.count !== count || drag.held !== isHeld || drag.locked !== blocked) {
                drag = { count, held: isHeld, locked: blocked };
                render();
            }
        }

        function release() {
            membrane.set('pointer', null);
            membrane.swell(0);
            if (drag) { drag = null; render(); }
        }

        // ── rows ──
        // A row is one file. its status is uploading, downloading, done or error. rows start as local uploads;
        // sync() adds and removes rows for files that live elsewhere (a shared list), and a local upload that
        // finishes is matched to its entry by id (row.adopt) so it isn't listed twice.
        function paint(row) {
            const li = row.el;
            li.dataset.status = row.status;
            row.pct.set(Math.round(row.progress * 100));
            row.refs.ring.style.strokeDashoffset = String(1 - row.progress);
            row.refs.fill.style.transform = `scaleX(${row.status === 'error' ? 1 : row.progress})`;
            row.size.set(row.status === 'error' ? m('failed') : o.formatSize(row.file.size));
            const sub = row.status === 'error' ? (row.error || '') : row.status === 'uploading' ? (row.note || '') : '';
            row.refs.sub.textContent = sub;
            row.refs.sub.title = sub;
            row.refs.sub.hidden = !sub;
            row.refs.remove.setAttribute('aria-label', m(row.status === 'uploading' ? 'cancel' : 'remove', { name: row.file.name }));
            syncLocked();
        }

        function setRow(row, patch) {
            Object.assign(row, patch);
            paint(row);
        }

        function newRow(file, extra) {
            return Object.assign({
                id: `f${++counter}`, file, status: 'uploading', progress: 0, note: '', error: '', last: 0,
                landed: reduced, closing: false, controller: null, el: null, refs: null, fileId: '', seen: false,
            }, extra);
        }

        function buildRow(row) {
            const li = document.createElement('li');
            li.className = 'dm-row';
            li.dataset.file = row.id;
            li.dataset.cancelable = 'true';
            li.dataset.dl = o.onDownload ? 'true' : 'false';
            li.innerHTML =
                '<div class="dm-row-clip"><div class="dm-row-inner">' +
                    '<span class="dm-lead" data-lead>' +
                        fileGlyph(row.file.type || guessType(row.file.name), 'dm-fileicon') +
                        '<span class="dm-lead-text"><span class="dm-name"></span><span class="dm-sub" hidden></span></span>' +
                    '</span>' +
                    '<span class="dm-meta">' +
                        '<span class="dm-info"><span class="dm-size"></span><span class="dm-pct-wrap"><span class="dm-pct-inner"><span class="dm-dot">·</span><span class="dm-pct"></span></span></span></span>' +
                        '<span class="dm-mark" aria-hidden="true">' +
                            '<svg class="dm-mark-ring" viewBox="0 0 16 16"><circle cx="8" cy="8" r="6" fill="none" stroke-width="1.5" class="dm-ring-track"/><circle cx="8" cy="8" r="6" fill="none" stroke-width="1.5" stroke-linecap="round" pathLength="1" stroke-dasharray="1" stroke-dashoffset="1" class="dm-ring-fill"/></svg>' +
                            `<span class="dm-mark-done">${drawnCheck('dm-svg dm-check')}</span>` +
                            `<span class="dm-mark-error">${icon(ICON.alert, 'dm-svg')}</span>` +
                        '</span>' +
                        `<button type="button" class="dm-btn dm-retry">${icon(ICON.retry, 'dm-svg', 1.75)}</button>` +
                        `<button type="button" class="dm-btn dm-download">${icon(ICON.download, 'dm-svg', 1.75)}</button>` +
                        `<button type="button" class="dm-btn dm-remove">${icon(ICON.x, 'dm-svg', 1.75)}</button>` +
                    '</span>' +
                    '<span class="dm-base" aria-hidden="true"></span><span class="dm-fill" aria-hidden="true"></span>' +
                '</div></div>';
            li.querySelector('.dm-name').textContent = row.file.name;
            row.el = li;
            row.refs = {
                size: li.querySelector('.dm-size'), pct: li.querySelector('.dm-pct'), sub: li.querySelector('.dm-sub'),
                ring: li.querySelector('.dm-ring-fill'), fill: li.querySelector('.dm-fill'),
                remove: li.querySelector('.dm-remove'), retry: li.querySelector('.dm-retry'), download: li.querySelector('.dm-download'),
            };
            row.size = createTextMorph(row.refs.size, { isReduced: () => reduced });
            row.pct = createNumberRoll(row.refs.pct, { suffix: '%', duration: 600, isReduced: () => reduced });
            row.refs.retry.setAttribute('aria-label', m('retry', { name: row.file.name }));
            row.refs.download.setAttribute('aria-label', m('download', { name: row.file.name }));
            row.refs.remove.addEventListener('click', () => remove(row.id));
            row.refs.retry.addEventListener('click', () => start(row));
            row.refs.download.addEventListener('click', () => download(row));
            if (reduced || row.landed) li.classList.add('dm-landed');
            if (reduced) li.classList.add('dm-open');
            list.appendChild(li);
            paint(row);
            if (!reduced) {
                let b = 0;
                const a = requestAnimationFrame(() => { b = requestAnimationFrame(() => li.classList.add('dm-open')); });
                row.cancelOpen = () => { cancelAnimationFrame(a); cancelAnimationFrame(b); };
            }
        }

        function makeHandle(row, controller) {
            const live = () => row.controller === controller;
            return {
                id: row.id,
                file: row.file,
                signal: controller.signal,
                progress: (f) => reportProgress(row, controller, f),
                note: (text) => { if (live()) setRow(row, { note: text || '' }); },
                fail: (message) => { if (live()) failed(row, message); },
                setCancelable: (on) => { row.el.dataset.cancelable = on ? 'true' : 'false'; },
                // the file now exists under this id in the shared list: its entry and this row are one.
                adopt: (fileId) => adopt(row, fileId),
            };
        }

        function adopt(row, fileId) {
            row.fileId = fileId;
            row.seen = false;
            // the poll may have listed the file before the upload call returned: keep this row, drop the twin.
            rows.filter((r) => r !== row && r.fileId === fileId && !r.closing).forEach((r) => dropRow(r));
        }

        function reportProgress(row, controller, f) {
            if (row.controller !== controller || controller.signal.aborted || row.status !== 'uploading') return;
            const v = Math.max(0, Math.min(1, Number.isFinite(f) ? f : 0));
            if (Math.abs(v - row.last) < 0.005 && v !== 1) return;
            row.last = v;
            setRow(row, { progress: v });
        }

        function succeed(row) {
            if (row.status === 'done' || row.closing) return;
            setRow(row, { status: 'done', progress: 1, note: '' });
            say(m('uploaded', { name: row.file.name }));
        }

        function failed(row, message) {
            if (row.closing) return;
            const text = message || m('uploadFailedShort');
            setRow(row, { status: 'error', error: text, note: '' });
            say(m('uploadFailed', { name: row.file.name, error: text }));
        }

        function start(row) {
            const run = o.onUpload;
            if (!run) { setRow(row, { status: 'done', progress: 1 }); return; }
            const controller = new AbortController();
            row.controller = controller;
            row.last = 0;
            row.el.dataset.cancelable = 'true';
            setRow(row, { status: 'uploading', progress: 0, error: '', note: '' });
            const handle = makeHandle(row, controller);
            Promise.resolve()
                .then(() => run(row.file, { progress: handle.progress, note: handle.note, signal: controller.signal, row: handle }))
                .then(
                    () => {
                        // A row that was already settled from outside (row.fail) stays as it is.
                        if (controller.signal.aborted || row.controller !== controller || row.status === 'error') return;
                        succeed(row);
                    },
                    (error) => {
                        if (controller.signal.aborted || row.controller !== controller) return;
                        failed(row, error && error.message ? error.message : '');
                    }
                );
        }

        // A finished file's download button: the row's hairline fills with the download, then it rests as done again.
        function download(row) {
            if (!o.onDownload || row.status !== 'done' || row.closing) return;
            let last = 0;
            setRow(row, { status: 'downloading', progress: 0 });
            const entry = { id: row.fileId, name: row.file.name, size: row.file.size, type: row.file.type || '' };
            const progress = (f) => {
                const v = Math.max(0, Math.min(1, Number.isFinite(f) ? f : 0));
                if (row.status !== 'downloading' || (Math.abs(v - last) < 0.005 && v !== 1)) return;
                last = v;
                setRow(row, { progress: v });
            };
            const settle = () => { if (!row.closing) setRow(row, { status: 'done', progress: 1 }); };
            Promise.resolve().then(() => o.onDownload(entry, { progress })).then(settle, settle);
        }

        function dropRow(row) {
            if (row.controller) row.controller.abort();
            if (row.cancelOpen) row.cancelOpen();
            const chip = chips.get(row.id);
            if (chip) chip.cancel();
            row.el.remove();
            const i = rows.indexOf(row);
            if (i >= 0) rows.splice(i, 1);
        }

        function remove(id) {
            const row = rows.find((r) => r.id === id);
            if (!row || row.closing) return;
            // cancelling an upload aborts it: the signal reaches the page's upload code, which stops sending.
            if (row.controller) row.controller.abort();
            row.closing = true;
            row.el.classList.add('dm-closing');
            later(() => dropRow(row), reduced ? 0 : 420);
        }

        // make the rows that stand for shared files match `entries` ({ id, name, size, type? }, in order).
        // rows for uploads still in flight are left alone; a file that disappears from the list leaves too.
        function sync(entries) {
            const list_ = Array.isArray(entries) ? entries : [];
            const ids = new Set(list_.map((e) => e.id));
            const ordered = [];
            list_.forEach((e) => {
                let row = rows.find((r) => r.fileId === e.id && !r.closing);
                if (!row) {
                    row = newRow({ name: e.name, size: e.size, type: e.type || '' }, { status: 'done', progress: 1, landed: true, fileId: e.id });
                    rows.push(row);
                    buildRow(row);
                }
                row.seen = true;
                ordered.push(row);
            });
            rows.filter((r) => r.seen && r.fileId && !ids.has(r.fileId) && !r.closing).forEach((r) => remove(r.id));
            // shared files in the list's order, then whatever is still local. only move nodes that are out of place.
            const want = ordered.concat(rows.filter((r) => !r.closing && ordered.indexOf(r) < 0));
            const have = Array.from(list.children);
            if (want.some((r, i) => have.filter((n) => !n.classList.contains('dm-closing'))[i] !== r.el)) {
                want.forEach((r) => list.appendChild(r.el));
            }
        }

        // ── files arriving ──
        function take(files, at) {
            if (!files.length) return;
            if (isLocked()) {
                membrane.spit(at.x, at.y);
                tell(m('busy'), true);
                say(m('busy'));
                return;
            }
            const current = rows.filter((r) => !r.closing);
            const accepted = [];
            const refused = [];
            for (const file of o.multiple ? files : files.slice(0, 1)) {
                const reason = o.validate ? o.validate(file) : '';
                if (reason) refused.push(reason);
                else accepted.push(file);
            }
            if (!o.multiple && files.length > 1) refused.push(m('oneAtATime'));
            if (!accepted.length) {
                membrane.spit(at.x, at.y);
                tell(refused[0] || m('nothing'), true);
                say(refused.join('. '));
                return;
            }
            membrane.gulp(at.x, at.y);
            if (!o.multiple) current.forEach((r) => remove(r.id));
            const zr = zone.getBoundingClientRect(), rr = host.getBoundingClientRect();
            const ox = zr.left - rr.left, oy = zr.top - rr.top;
            const added = accepted.map((file) => newRow(file));
            added.forEach((row, i) => {
                rows.push(row);
                buildRow(row);
                if (!reduced) flight(row, at.x + ox, at.y + oy, i * 80);
            });
            added.forEach(start);
            const summary = accepted.length === 1 ? m('added', { name: accepted[0].name }) : m('addedMany', { count: accepted.length });
            tell(refused.length ? (refused.length === 1 ? refused[0] : m('skippedMany', { count: refused.length })) : summary, refused.length > 0);
            say([summary].concat(refused).join('. '));
        }

        // A file in flight: from where it was dropped, down through the border, onto its row.
        function flight(row, startX, startY, delay) {
            const chip = document.createElement('div');
            chip.className = 'dm-chip';
            chip.setAttribute('aria-hidden', 'true');
            chip.innerHTML = fileGlyph(row.file.type || '', 'dm-fileicon') + '<span class="dm-chip-name"></span>';
            chip.querySelector('.dm-chip-name').textContent = row.file.name;
            host.appendChild(chip);
            const w = chip.offsetWidth, h = chip.offsetHeight;
            const begin = performance.now() + delay;
            let frame = 0;
            const finish = () => {
                cancelAnimationFrame(frame);
                membrane.set(row.id, null);
                chips.delete(row.id);
                chip.remove();
            };
            const step = (now) => {
                const t = Math.max(0, Math.min(1, (now - begin) / FLIGHT));
                const lead = row.el && row.el.querySelector('[data-lead]');
                const br = host.getBoundingClientRect();
                const lr = lead && lead.getBoundingClientRect();
                const tx = lr ? lr.left - br.left - 10 : startX - w / 2;
                const ty = lr ? lr.top - br.top + lr.height / 2 - h / 2 : startY - h / 2;
                const sx = startX - w / 2, sy = startY - h / 2;
                const e = ease(t);
                const x = sx + (tx - sx) * e, y = sy + (ty - sy) * e;
                chip.style.transform = `translate3d(${x}px,${y}px,0)`;
                const zr = zone.getBoundingClientRect();
                membrane.set(row.id, { x: x + w / 2 - (zr.left - br.left), y: y + h / 2 - (zr.top - br.top), kind: 'carry', spread: w / 3 });
                if (t < 1) { frame = requestAnimationFrame(step); return; }
                membrane.set(row.id, null);
                row.landed = true;
                row.el.classList.add('dm-landed');
                chip.classList.add('dm-chip-landed');
                later(() => { chips.delete(row.id); chip.remove(); }, 200);
            };
            chip.style.transform = `translate3d(${startX - w / 2}px,${startY - h / 2}px,0)`;
            if (chip.animate) chip.animate([{ opacity: 0, scale: '0.9' }, { opacity: 1, scale: '1' }], { duration: 180, easing: EASE_OUT });
            frame = requestAnimationFrame(step);
            chips.set(row.id, { cancel: finish });
        }

        // ── events ──
        zone.addEventListener('click', () => {
            if (isLocked()) { tell(m('busy'), true); return; }
            input.click();
        });
        zone.addEventListener('paste', (e) => {
            const files = Array.from((e.clipboardData && e.clipboardData.files) || []);
            if (!files.length) return;
            e.preventDefault();
            take(files, centre());
        });
        input.addEventListener('change', () => {
            const files = Array.from(input.files || []);
            input.value = '';
            take(files, centre());
        });

        // files dragged in from the system. the whole window is the target, so the line can reach out
        // before the file arrives and a drop anywhere on the page is taken. every file drag is claimed
        // (preventDefault) whether or not the zone can use it: an unclaimed drop makes the browser open the
        // file in a tab, which on a page with an upload running would also throw that upload away.
        //
        // the drag ends on the last dragleave, on drop or dragend. dragover repeats while the pointer moves
        // but not reliably while it is still (firefox sends it every ~350ms, or not at all), so silence
        // can't be taken to mean the drag left; it only arms a long watchdog for the case where the browser
        // loses the drag without telling us.
        let depth = 0;
        let watchdog = 0;
        const end = () => { depth = 0; clearTimeout(watchdog); release(); };
        const hasFiles = (e) => !!e.dataTransfer && Array.prototype.indexOf.call(e.dataTransfer.types || [], 'Files') >= 0;
        const follow = (e) => {
            clearTimeout(watchdog);
            watchdog = setTimeout(end, 4000);
            const items = Array.from(e.dataTransfer.items || []).filter((i) => i.kind === 'file');
            track(e.clientX, e.clientY, items.length || 1);
            e.dataTransfer.dropEffect = isLocked() || !isVisible() ? 'none' : 'copy';
        };
        const onEnter = (e) => {
            if (!hasFiles(e)) return;
            e.preventDefault();
            depth++;
            follow(e);
        };
        const onOver = (e) => {
            if (!hasFiles(e)) return;
            e.preventDefault();
            follow(e);
        };
        const onLeave = (e) => {
            if (!hasFiles(e)) return;
            depth = Math.max(0, depth - 1);
            const outside = !e.relatedTarget && (e.clientX <= 0 || e.clientY <= 0 || e.clientX >= window.innerWidth || e.clientY >= window.innerHeight);
            if (depth === 0 || outside) end();
        };
        const onDrop = (e) => {
            if (!hasFiles(e)) return;
            e.preventDefault();
            const files = Array.from(e.dataTransfer.files || []);
            const p = local(e.clientX, e.clientY);
            end();
            if (files.length && isVisible()) take(files, { x: Math.max(0, Math.min(p.w, p.x)), y: Math.max(0, Math.min(p.h, p.y)) });
        };
        window.addEventListener('dragenter', onEnter);
        window.addEventListener('dragover', onOver);
        window.addEventListener('dragleave', onLeave);
        window.addEventListener('drop', onDrop);
        window.addEventListener('dragend', end);

        const listen = (query, fn) => (query.addEventListener ? query.addEventListener('change', fn) : query.addListener(fn));
        listen(reducedQuery, () => { reduced = reducedQuery.matches; if (reduced) membrane.settle(); });
        listen(touchQuery, render);

        render();

        return {
            /** show the shared files in `entries` ({ id, name, size, type? }) as rows; see sync(). */
            sync,
            /** remove every row at once, aborting anything still uploading. */
            clear() {
                rows.slice().forEach(dropRow);
            },
        };
    }

    window.SendlyDropMembrane = { create };
})();
