// Moves accounts from before identity keys onto them. Temporary: remove this
// file, its script tags and the SendlyIdentityMigration hooks in crypto.js
// together with handlers/identity_migration.go once every account is migrated.
//
// Everything happens in the browser; the server only stores wrapped keys. A
// device holding the account's legacy AES user key
//  - creates the identity key if the account has none yet, storing it
//    encrypted with the user key (the escrow) for the account's other legacy
//    devices, or else decrypts the escrow to get its own copy, and
//  - re-wraps the account's files from the user key to the identity key.
const SendlyIdentityMigration = (function () {
    'use strict';

    const t = (k, d) => window.CONFIG?.t?.[k] || d || k;
    const ESCROW_ALG = 'AES-GCM-UK-v1';
    let filesRun = null;

    async function getJSON(url, headers) {
        const response = await fetch(url, { headers });
        if (!response.ok) return null;
        return response.json().catch(() => null);
    }

    async function postJSON(url, headers, body) {
        return fetch(url, { method: 'POST', headers, body: JSON.stringify(body) });
    }

    // The legacy user key: this browser's stored copy, or this device's
    // server-side copy opened with the device key.
    async function legacyUserKey(userId, identity, headers) {
        const stored = SecureCrypto.getLegacyUserKeyRaw(userId);
        if (stored) return stored;
        const payload = await getJSON(`/api/me/identity-migration/legacy-key?device_id=${encodeURIComponent(identity.deviceId)}`, headers);
        if (!payload?.wrapped_uk_b64) return null;
        try {
            return await SecureCrypto.rsaOaepDecrypt(SecureCrypto.fromBase64(payload.wrapped_uk_b64), identity.privateKeyJWK);
        } catch (_) {
            return null;
        }
    }

    async function escrowIdentityKey(userKey, privateKeyJWK) {
        const nonce = crypto.getRandomValues(new Uint8Array(12));
        const key = await crypto.subtle.importKey('raw', userKey, { name: 'AES-GCM' }, false, ['encrypt']);
        const plaintext = new TextEncoder().encode(JSON.stringify(privateKeyJWK));
        const ciphertext = new Uint8Array(await crypto.subtle.encrypt({ name: 'AES-GCM', iv: nonce }, key, plaintext));
        return { ciphertext, nonce };
    }

    async function selfWrapFields(privateKeyJWK, identity) {
        return {
            wrapped_identity_private_key_b64: SecureCrypto.toBase64(await SecureCrypto.wrapIdentityKeyForDevice(privateKeyJWK, identity.publicKeyJWK)),
            identity_key_wrap_alg: 'RSA-OAEP-2048+AES-GCM-256-v1',
            identity_key_wrap_meta: { type: 'legacy-migration', device_id: identity.deviceId }
        };
    }

    // Gives this legacy device a copy of the account's identity key, creating
    // the key if the account has none yet. Returns true if the device should
    // register again (to pick the key up, or to adopt one created meanwhile).
    async function obtainIdentityKey({ userId, identity, payload, headers }) {
        if (!payload?.needs_identity_migration && !payload?.needs_enrollment) return false;
        const userKey = await legacyUserKey(userId, identity, headers);
        if (!userKey) return false;

        if (payload.needs_identity_migration) {
            const identityKey = await SecureCrypto.generateIdentityKeypair();
            const escrow = await escrowIdentityKey(userKey, identityKey.privateKeyJWK);
            const response = await postJSON('/api/me/identity-migration/start', headers, {
                device_id: identity.deviceId,
                identity_public_key_jwk: identityKey.publicKeyJWK,
                identity_key_algorithm: identityKey.keyAlgorithm,
                ...(await selfWrapFields(identityKey.privateKeyJWK, identity)),
                escrow_wrapped_private_key_b64: SecureCrypto.toBase64(escrow.ciphertext),
                escrow_nonce_b64: SecureCrypto.toBase64(escrow.nonce),
                escrow_wrap_alg: ESCROW_ALG
            });
            // 409: another legacy device created it first; registering again
            // leads here with needs_enrollment, and this device adopts it.
            return response.ok || response.status === 409;
        }

        const escrow = await getJSON(`/api/me/identity-migration/escrow?device_id=${encodeURIComponent(identity.deviceId)}`, headers);
        if (!escrow?.wrapped_private_key_b64) return false;
        let privateKeyJWK;
        try {
            const key = await crypto.subtle.importKey('raw', userKey, { name: 'AES-GCM' }, false, ['decrypt']);
            const plaintext = await crypto.subtle.decrypt(
                { name: 'AES-GCM', iv: SecureCrypto.fromBase64(escrow.wrap_nonce_b64) },
                key,
                SecureCrypto.fromBase64(escrow.wrapped_private_key_b64)
            );
            privateKeyJWK = JSON.parse(new TextDecoder().decode(plaintext));
        } catch (_) {
            return false;
        }
        // Only adopt a key that belongs to the account's active public key.
        const active = payload.identity_public_key;
        if (!active || active.key_version !== escrow.identity_key_version || !SecureCrypto.isSameRsaKey(privateKeyJWK, active.public_key_jwk)) {
            return false;
        }
        const response = await postJSON('/api/me/identity-migration/adopt', headers, {
            device_id: identity.deviceId,
            identity_key_version: escrow.identity_key_version,
            ...(await selfWrapFields(privateKeyJWK, identity))
        });
        return response.ok;
    }

    function showNotice(text, done = false) {
        SendlyToast.show(text, {
            id: 'identity-migration',
            type: done ? 'success' : 'info',
            duration: done ? undefined : 0
        });
    }

    // Re-wraps the account's files from the legacy user key to the identity
    // key, page by page, showing progress. Runs once per page load.
    async function migrateFiles({ userId, identity, identityKey, headers }) {
        const userKey = await legacyUserKey(userId, identity, headers);
        if (!userKey) return;
        let after = '';
        let done = 0;
        for (;;) {
            const page = await getJSON(`/api/me/identity-migration/files?device_id=${encodeURIComponent(identity.deviceId)}&after=${encodeURIComponent(after)}`, headers);
            if (!page?.items?.length) break;
            const total = done + page.items.length + (page.remaining || 0);
            showNotice(t('identity_migration_progress').replace('{done}', done).replace('{total}', total));

            const items = [];
            for (const item of page.items) {
                try {
                    const dek = await SecureCrypto.unwrapWithLegacyUserKey(
                        SecureCrypto.fromBase64(item.wrapped_dek_b64),
                        SecureCrypto.fromBase64(item.dek_wrap_nonce_b64),
                        userKey
                    );
                    items.push({
                        file_id: item.file_id,
                        identity_wrapped_dek_b64: SecureCrypto.toBase64(await SecureCrypto.wrapFileDEKForIdentity(dek, identityKey.publicKeyJWK)),
                        identity_dek_wrap_alg: 'RSA-OAEP-2048-v1'
                    });
                } catch (error) {
                    // Wrapped with another user key (e.g. a stale one); leave it locked.
                    console.warn('Could not migrate file', item.file_id, error);
                }
            }
            if (items.length) {
                const response = await postJSON('/api/me/identity-migration/files', headers, {
                    device_id: identity.deviceId,
                    identity_key_version: identityKey.keyVersion,
                    items
                });
                if (!response.ok) return;
            }
            done += page.items.length;
            after = page.items[page.items.length - 1].file_id;
        }
        if (done) showNotice(t('identity_migration_done'), true);
    }

    function migrateFilesInBackground(options) {
        if (!filesRun) {
            filesRun = migrateFiles(options).catch((error) => console.warn('File migration failed:', error));
        }
        return filesRun;
    }

    return { obtainIdentityKey, migrateFilesInBackground };
})();

window.SendlyIdentityMigration = SendlyIdentityMigration;
