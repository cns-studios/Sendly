// fetch() that waits out short outages instead of failing the operation.
//
// Sendly runs as several replicas that are replaced one at a time on every
// deploy, and moved when a node is drained. A request that lands on a replica
// as it goes away fails with a network error or a 502/503/504 from the proxy;
// the same request a moment later succeeds on another replica. The upload
// and download steps are safe to repeat (the server treats a repeated chunk,
// complete or finalize as the same request), so they go through
// SendlyNet.fetch and the person using the page notices nothing but a pause.
const SendlyNet = (function () {
    'use strict';

    // Statuses that mean "not now" rather than "no".
    const TRANSIENT = new Set([408, 425, 429, 499, 500, 502, 503, 504]);
    const DEFAULT_PATIENCE_MS = 5 * 60 * 1000;
    const MAX_DELAY_MS = 10 * 1000;

    const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

    // fetch(url, init) retried on network errors and transient statuses for
    // up to options.patienceMs. Any other answer, success or not, is returned
    // as is. options.onWait(attempt) is called before each retry.
    async function resilientFetch(url, init, options = {}) {
        const patience = options.patienceMs ?? DEFAULT_PATIENCE_MS;
        const deadline = Date.now() + patience;
        let attempt = 0;

        for (;;) {
            let response = null;
            let error = null;
            try {
                response = await fetch(url, init);
            } catch (err) {
                if (err?.name === 'AbortError') throw err;
                error = err;
            }
            if (response && !TRANSIENT.has(response.status)) return response;

            attempt++;
            const retryAfter = Number(response?.headers?.get('Retry-After')) * 1000;
            const delay = Math.min(MAX_DELAY_MS, retryAfter || 500 * 2 ** Math.min(attempt - 1, 5));
            if (Date.now() + delay > deadline) {
                if (response) return response;
                throw error;
            }
            options.onWait?.(attempt);
            await sleep(delay * (0.75 + Math.random() * 0.5));
        }
    }

    return { fetch: resilientFetch };
})();
