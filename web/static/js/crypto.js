const SecureCrypto = (function() {
    'use strict';

    const t = (k, d) => window.CONFIG?.t?.[k] || d || k;
    const tpl = (k, vars) => { let s = t(k); if (vars) for (const [key, val] of Object.entries(vars)) s = s.replace(`{${key}}`, val); return s; };

     
    const CONFIG = {
        algorithm: 'AES-GCM',
        keyLength: 256,
        ivLength: 12,
        saltLength: 16,
        pbkdf2Iterations: 100000,
        wordCount: 5
    };

    const DEVICE_STORAGE_KEY = 'sendly_device_identity_v1';
    const USER_KEY_PREFIX = 'sendly_user_key_v1_';
    const IDENTITY_KEY_PREFIX = 'sendly_identity_key_v1_';
    const FILE_KEY_PREFIX = 'sendly_file_key_v1_';

     
    let wordList = null;

    async function loadWordList() {
        if (wordList) return wordList;
        
        try {
            const response = await fetch('/static/wordlist.txt');
            const text = await response.text();
            wordList = text.trim().split('\n').map(w => w.trim().toLowerCase());
            console.log(`Loaded ${wordList.length} words`);
            return wordList;
        } catch (error) {
            console.error('Failed to load word list:', error);
            throw new Error('Failed to load word list');
        }
    }

    async function generatePassword() {
        const words = await loadWordList();
        const selectedWords = [];
        const randomValues = new Uint32Array(CONFIG.wordCount);
        crypto.getRandomValues(randomValues);

        for (let i = 0; i < CONFIG.wordCount; i++) {
            const index = randomValues[i] % words.length;
            selectedWords.push(words[index]);
        }

        return selectedWords.join('-');
    }

    async function deriveKey(password, salt) {
        const encoder = new TextEncoder();
        const passwordBuffer = encoder.encode(password);

         
        const keyMaterial = await crypto.subtle.importKey(
            'raw',
            passwordBuffer,
            'PBKDF2',
            false,
            ['deriveKey']
        );

         
        const key = await crypto.subtle.deriveKey(
            {
                name: 'PBKDF2',
                salt: salt,
                iterations: CONFIG.pbkdf2Iterations,
                hash: 'SHA-256'
            },
            keyMaterial,
            {
                name: CONFIG.algorithm,
                length: CONFIG.keyLength
            },
            false,
            ['encrypt', 'decrypt']
        );

        return key;
    }


    function generateRandomBytes(length) {
        const bytes = new Uint8Array(length);
        crypto.getRandomValues(bytes);
        return bytes;
    }

    function toBase64(data) {
        const bytes = data instanceof Uint8Array ? data : new Uint8Array(data);
        let binary = '';
        for (let i = 0; i < bytes.length; i++) {
            binary += String.fromCharCode(bytes[i]);
        }
        return btoa(binary);
    }

    function fromBase64(value) {
        const binary = atob(value);
        const bytes = new Uint8Array(binary.length);
        for (let i = 0; i < binary.length; i++) {
            bytes[i] = binary.charCodeAt(i);
        }
        return bytes;
    }

    function userDeviceStorageKey(userId) {
        return `${DEVICE_STORAGE_KEY}_u${userId}`;
    }

    // The browser-wide identity is used unless this account already got its
    // own one because the browser-wide device id belongs to another account
    // (see registerAuthenticatedDevice).
    async function getOrCreateDeviceIdentity(userId = 0) {
        if (userId) {
            const scoped = localStorage.getItem(userDeviceStorageKey(userId));
            if (scoped) {
                return JSON.parse(scoped);
            }
        }
        const cached = localStorage.getItem(DEVICE_STORAGE_KEY);
        if (cached) {
            return JSON.parse(cached);
        }
        const identity = await generateDeviceIdentity();
        localStorage.setItem(DEVICE_STORAGE_KEY, JSON.stringify(identity));
        return identity;
    }

    async function createUserScopedDeviceIdentity(userId) {
        const identity = await generateDeviceIdentity();
        localStorage.setItem(userDeviceStorageKey(userId), JSON.stringify(identity));
        return identity;
    }

    async function generateDeviceIdentity() {
        const keyPair = await crypto.subtle.generateKey(
            {
                name: 'RSA-OAEP',
                modulusLength: 2048,
                publicExponent: new Uint8Array([1, 0, 1]),
                hash: 'SHA-256'
            },
            true,
            ['encrypt', 'decrypt']
        );
        const publicJWK = await crypto.subtle.exportKey('jwk', keyPair.publicKey);
        const privateJWK = await crypto.subtle.exportKey('jwk', keyPair.privateKey);
        const identity = {
            deviceId: crypto.randomUUID ? crypto.randomUUID() : 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, c => { const r = Math.random() * 16 | 0; return (c === 'x' ? r : (r & 0x3 | 0x8)).toString(16); }),
            keyAlgorithm: 'RSA-OAEP-2048',
            keyVersion: 1,
            publicKeyJWK: publicJWK,
            privateKeyJWK: privateJWK
        };
        return identity;
    }

    function identityKeyStorageKey(userId) {
        return `${IDENTITY_KEY_PREFIX}${userId || 'guest'}`;
    }

    // Stores the account's current identity key. Private keys of earlier
    // versions are kept under `retired`: after a recovery they are the only
    // way to re-wrap files and transfers sent to those versions.
    function saveIdentityKey(userId, keyData) {
        if (!keyData) return;
        const stored = getIdentityKey(userId);
        const retired = (stored?.retired || []).filter((key) => key.keyVersion !== keyData.keyVersion);
        if (stored?.privateKeyJWK && stored.keyVersion !== keyData.keyVersion) {
            retired.push({ keyVersion: stored.keyVersion, privateKeyJWK: stored.privateKeyJWK });
        }
        localStorage.setItem(identityKeyStorageKey(userId), JSON.stringify({ ...keyData, retired }));
    }

    function getIdentityKey(userId) {
        const value = localStorage.getItem(identityKeyStorageKey(userId));
        if (!value) return null;
        try {
            return JSON.parse(value);
        } catch (_) {
            return null;
        }
    }

    function cacheFileKey(fileId, keyString) {
        if (!fileId || !keyString) return;
        sessionStorage.setItem(`${FILE_KEY_PREFIX}${fileId}`, keyString);
    }

    function getCachedFileKey(fileId) {
        if (!fileId) return null;
        return sessionStorage.getItem(`${FILE_KEY_PREFIX}${fileId}`);
    }

    function removeCachedFileKey(fileId) {
        if (!fileId) return;
        sessionStorage.removeItem(`${FILE_KEY_PREFIX}${fileId}`);
    }

    async function rsaOaepEncrypt(plaintextBytes, publicKeyJWK) {
        const publicKey = await crypto.subtle.importKey(
            'jwk',
            publicKeyJWK,
            { name: 'RSA-OAEP', hash: 'SHA-256' },
            false,
            ['encrypt']
        );
        const wrapped = await crypto.subtle.encrypt({ name: 'RSA-OAEP' }, publicKey, plaintextBytes);
        return new Uint8Array(wrapped);
    }

    async function rsaOaepDecrypt(wrappedBytes, privateKeyJWK) {
        const privateKey = await crypto.subtle.importKey(
            'jwk',
            privateKeyJWK,
            { name: 'RSA-OAEP', hash: 'SHA-256' },
            false,
            ['decrypt']
        );
        const raw = await crypto.subtle.decrypt({ name: 'RSA-OAEP' }, privateKey, wrappedBytes);
        return new Uint8Array(raw);
    }

    async function generateIdentityKeypair() {
        const keyPair = await crypto.subtle.generateKey(
            {
                name: 'RSA-OAEP',
                modulusLength: 2048,
                publicExponent: new Uint8Array([1, 0, 1]),
                hash: 'SHA-256'
            },
            true,
            ['encrypt', 'decrypt']
        );
        const publicJWK = await crypto.subtle.exportKey('jwk', keyPair.publicKey);
        const privateJWK = await crypto.subtle.exportKey('jwk', keyPair.privateKey);
        return {
            keyAlgorithm: 'RSA-OAEP-2048',
            keyVersion: 1,
            publicKeyJWK: publicJWK,
            privateKeyJWK: privateJWK
        };
    }

    async function wrapIdentityKeyForDevice(identityPrivateKeyJWK, devicePublicKeyJWK) {
        const aesKey = await crypto.subtle.generateKey(
            { name: 'AES-GCM', length: 256 },
            true,
            ['encrypt', 'decrypt']
        );
        const rawAesKey = new Uint8Array(await crypto.subtle.exportKey('raw', aesKey));
        const wrappedAesKey = await rsaOaepEncrypt(rawAesKey, devicePublicKeyJWK);
        const iv = generateRandomBytes(CONFIG.ivLength);
        const plaintext = new TextEncoder().encode(JSON.stringify(identityPrivateKeyJWK));
        const ciphertext = new Uint8Array(await crypto.subtle.encrypt(
            { name: 'AES-GCM', iv },
            aesKey,
            plaintext
        ));
        const combined = new Uint8Array(wrappedAesKey.length + iv.length + ciphertext.length);
        combined.set(wrappedAesKey, 0);
        combined.set(iv, wrappedAesKey.length);
        combined.set(ciphertext, wrappedAesKey.length + iv.length);
        return combined;
    }

    async function unwrapIdentityKeyForDevice(wrappedBytes, devicePrivateKeyJWK) {
        if (!wrappedBytes || wrappedBytes.length < 268) {
            throw new Error('Invalid wrapped identity key payload: insufficient length');
        }
        const wrappedAesKey = wrappedBytes.slice(0, 256);
        const iv = wrappedBytes.slice(256, 268);
        const ciphertext = wrappedBytes.slice(268);

        const rawAesKey = await rsaOaepDecrypt(wrappedAesKey, devicePrivateKeyJWK);
        const aesKey = await crypto.subtle.importKey(
            'raw',
            rawAesKey,
            { name: 'AES-GCM' },
            false,
            ['decrypt']
        );
        const decrypted = await crypto.subtle.decrypt(
            { name: 'AES-GCM', iv },
            aesKey,
            ciphertext
        );
        return JSON.parse(new TextDecoder().decode(decrypted));
    }

    function parseEnvelope(envelope) {
        const wrapAlg = String(envelope?.dek_wrap_alg || '').trim().toUpperCase();
        if (!envelope?.wrapped_dek_b64) throw new Error('Missing file key envelope');
        return {
            wrappedBytes: fromBase64(envelope.wrapped_dek_b64),
            wrapAlg,
            nonceBytes: envelope.dek_wrap_nonce_b64 ? fromBase64(envelope.dek_wrap_nonce_b64) : new Uint8Array()
        };
    }

    // Opens a file key envelope: a raw key (guest share links), or one wrapped
    // with RSA-OAEP for a quick share guest's throwaway key or for the user's
    // identity key.
    async function unwrapFileDEK(envelope, {
        authenticated = !!window.CONFIG?.authenticated,
        ephemeralPrivateKey = null,
        identityPrivateKeyJWK = null
    } = {}) {
        const parsed = parseEnvelope(envelope);
        if (parsed.wrapAlg.startsWith('RAW-DEK')) {
            if (authenticated) throw new Error('Raw file keys are not valid for authenticated access');
            return parsed.wrappedBytes;
        }
        if (parsed.wrapAlg.startsWith('RSA-OAEP')) {
            const privateKey = ephemeralPrivateKey || (identityPrivateKeyJWK
                ? await crypto.subtle.importKey('jwk', identityPrivateKeyJWK,
                    { name: 'RSA-OAEP', hash: 'SHA-256' }, false, ['decrypt'])
                : null);
            if (!privateKey) throw new Error('No private key available for file envelope');
            const raw = await crypto.subtle.decrypt({ name: 'RSA-OAEP' }, privateKey, parsed.wrappedBytes);
            return new Uint8Array(raw);
        }
        throw new Error('Unsupported file key envelope');
    }

    async function wrapFileDEKForIdentity(dekBytes, identityPublicKeyJWK) {
        return rsaOaepEncrypt(dekBytes, identityPublicKeyJWK);
    }

    // ── Legacy user key (read-only) ──
    // Before identity keys, each account had one AES "user key" that wrapped
    // its file keys. Nothing is wrapped with it anymore; it is only read to
    // migrate an account's files to its identity key. Remove with the
    // identity migration.
    function getLegacyUserKeyRaw(userId) {
        const value = localStorage.getItem(`${USER_KEY_PREFIX}${userId || 'guest'}`);
        return value ? fromBase64(value) : null;
    }

    async function unwrapWithLegacyUserKey(wrappedBytes, nonceBytes, userKeyRaw) {
        const key = await crypto.subtle.importKey('raw', userKeyRaw, { name: 'AES-GCM' }, false, ['decrypt']);
        const raw = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: nonceBytes }, key, wrappedBytes);
        return new Uint8Array(raw);
    }

    // Registers this browser as a device of the signed-in account and returns
    // the account's identity key if this device holds a copy of it. The
    // server's copy is authoritative. A new identity keypair is only generated
    // when the server asks for one (a brand-new account) or on recovery,
    // which replaces the account's identity key with a new version.
    //
    // payload.needs_enrollment: another device has to approve this one.
    // payload.needs_identity_migration: the account predates identity keys.
    async function registerAuthenticatedDevice({
        endpoint = '/api/me/devices/register',
        userId = window.CONFIG?.cnsUserId || 0,
        username = window.CONFIG?.cnsUsername || '',
        csrfToken = ''
    } = {}) {
        const recovering = /\/recover$/.test(endpoint);
        let identity = await getOrCreateDeviceIdentity(userId);
        let newIdentityKey = null;

        const deviceFields = () => ({
            device_id: identity.deviceId,
            device_label: `${username || t('user_default')} device`,
            public_key_jwk: identity.publicKeyJWK,
            key_algorithm: identity.keyAlgorithm,
            key_version: identity.keyVersion
        });
        const identitySetupFields = async () => newIdentityKey ? {
            identity_public_key_jwk: newIdentityKey.publicKeyJWK,
            identity_key_algorithm: newIdentityKey.keyAlgorithm,
            wrapped_identity_private_key_b64: toBase64(await wrapIdentityKeyForDevice(newIdentityKey.privateKeyJWK, identity.publicKeyJWK)),
            identity_key_wrap_alg: 'RSA-OAEP-2048+AES-GCM-256-v1',
            identity_key_wrap_meta: { type: 'self-wrap', device_id: identity.deviceId }
        } : {};

        const headers = { 'Content-Type': 'application/json' };
        if (csrfToken) headers['X-CSRF-Token'] = csrfToken;
        const send = async () => {
            let response = await fetch(endpoint, {
                method: 'POST',
                headers,
                body: JSON.stringify({ ...deviceFields(), ...(await identitySetupFields()) })
            });
            if (response.status === 409 && userId) {
                const conflict = await response.clone().json().catch(() => ({}));
                if (conflict.code === 'DEVICE_ID_CONFLICT') {
                    // This browser's device id is registered to another account
                    // (shared browser). Give this account its own device identity.
                    identity = await createUserScopedDeviceIdentity(userId);
                    response = await fetch(endpoint, {
                        method: 'POST',
                        headers,
                        body: JSON.stringify({ ...deviceFields(), ...(await identitySetupFields()) })
                    });
                }
            }
            if (!response.ok) {
                const payload = await response.json().catch(() => ({}));
                throw new Error(payload.error || 'Device registration failed');
            }
            return response.json().catch(() => ({}));
        };

        if (recovering) newIdentityKey = await generateIdentityKeypair();
        let payload = await send();
        if (payload.needs_identity_setup) {
            newIdentityKey = await generateIdentityKeypair();
            payload = await send();
        }

        // Temporary: an account from before identity keys (or one of its
        // devices without the identity key yet) is moved onto them with its
        // legacy user key; see identity-migration.js.
        const migration = window.SendlyIdentityMigration;
        for (let attempt = 0; migration && attempt < 2 && !payload.identity_key_envelope; attempt++) {
            if (!await migration.obtainIdentityKey({ userId, identity, payload, headers })) break;
            payload = await send();
        }

        const identityKey = await identityKeyFromPayload(payload, identity);
        if (identityKey) {
            saveIdentityKey(userId, identityKey);
            migration?.migrateFilesInBackground({ userId, identity, identityKey, headers });
        }
        rescueLockedFileKeysInBackground({ userId, payload, headers });
        return { identity, identityKey, payload };
    }

    // After a recovery, file keys wrapped for an older identity key version
    // stay locked. If this browser still holds such a version (e.g. it was
    // offline during the recovery), it re-wraps them for the account's active
    // public key as the server reports it. Runs once per page load.
    let rescueRun = null;
    function rescueLockedFileKeysInBackground({ userId, payload, headers }) {
        const active = payload?.identity_public_key;
        const stored = getIdentityKey(userId);
        if (rescueRun || !active?.public_key_jwk || !stored) return rescueRun;
        const oldKeys = [stored, ...(stored.retired || [])]
            .filter((key) => key?.privateKeyJWK && key.keyVersion < active.key_version);
        if (!oldKeys.length) return null;

        rescueRun = (async () => {
            for (const oldKey of oldKeys) {
                let after = '';
                for (;;) {
                    const response = await fetch(`/api/me/identity-rescue/locked?version=${oldKey.keyVersion}&after=${encodeURIComponent(after)}`, { headers });
                    const page = response.ok ? await response.json().catch(() => null) : null;
                    if (!page?.items?.length) break;
                    const items = [];
                    for (const item of page.items) {
                        try {
                            const dek = await rsaOaepDecrypt(fromBase64(item.wrapped_dek_b64), oldKey.privateKeyJWK);
                            items.push({
                                file_id: item.file_id,
                                identity_wrapped_dek_b64: toBase64(await wrapFileDEKForIdentity(dek, active.public_key_jwk)),
                                identity_dek_wrap_alg: 'RSA-OAEP-2048-v1'
                            });
                        } catch (_) {
                            // Not openable with this key; it stays locked.
                        }
                    }
                    if (items.length) {
                        const saved = await fetch('/api/me/identity-rescue', {
                            method: 'POST',
                            headers,
                            body: JSON.stringify({ from_version: oldKey.keyVersion, to_version: active.key_version, items })
                        });
                        if (!saved.ok) break;
                    }
                    after = page.items[page.items.length - 1].file_id;
                }
            }
        })().catch((error) => console.warn('Rescuing locked file keys failed:', error));
        return rescueRun;
    }

    // Two JWKs describe the same RSA key if modulus and exponent match; a
    // private JWK carries both, so it can be checked against a public one.
    function isSameRsaKey(jwk, publicJWK) {
        return !!jwk?.n && jwk.n === publicJWK?.n && jwk.e === publicJWK?.e;
    }

    // Opens this device's copy of the identity key from a registration
    // response. A copy that doesn't open, or doesn't belong to the account's
    // active identity public key, counts as none.
    async function identityKeyFromPayload(payload, identity) {
        const envelope = payload?.identity_key_envelope;
        const active = payload?.identity_public_key;
        if (!envelope?.wrapped_private_key_b64 || !active?.public_key_jwk) return null;
        try {
            const privateKeyJWK = await unwrapIdentityKeyForDevice(fromBase64(envelope.wrapped_private_key_b64), identity.privateKeyJWK);
            if (!isSameRsaKey(privateKeyJWK, active.public_key_jwk)) return null;
            return {
                keyVersion: active.key_version,
                keyAlgorithm: active.key_algorithm || 'RSA-OAEP-2048',
                publicKeyJWK: active.public_key_jwk,
                privateKeyJWK
            };
        } catch (error) {
            console.error('Could not open this device\'s identity key copy:', error);
            return null;
        }
    }

    // Wraps a file key (or session password) for the signed-in user's own
    // identity key, as the uploader's 'owner' envelope.
    async function buildOwnerEnvelope(secretBytes, identityKey) {
        if (!identityKey?.publicKeyJWK) throw new Error('This device has no identity key');
        return {
            identity_wrapped_dek_b64: toBase64(await wrapFileDEKForIdentity(secretBytes, identityKey.publicKeyJWK)),
            identity_dek_wrap_alg: 'RSA-OAEP-2048-v1',
            identity_dek_wrap_version: 1,
            identity_key_version: identityKey.keyVersion
        };
    }

    async function encrypt(data, password) {
        const salt = generateRandomBytes(CONFIG.saltLength);
        const iv = generateRandomBytes(CONFIG.ivLength);
        const key = await deriveKey(password, salt);

        const ciphertext = await crypto.subtle.encrypt(
            {
                name: CONFIG.algorithm,
                iv: iv
            },
            key,
            data
        );

         
        const result = new Uint8Array(salt.length + iv.length + ciphertext.byteLength);
        result.set(salt, 0);
        result.set(iv, salt.length);
        result.set(new Uint8Array(ciphertext), salt.length + iv.length);

        return result;
    }


    async function decrypt(encryptedData, password) {
        const data = new Uint8Array(encryptedData);

         
        const salt = data.slice(0, CONFIG.saltLength);
        const iv = data.slice(CONFIG.saltLength, CONFIG.saltLength + CONFIG.ivLength);
        const ciphertext = data.slice(CONFIG.saltLength + CONFIG.ivLength);

        const key = await deriveKey(password, salt);

        try {
            const decrypted = await crypto.subtle.decrypt(
                {
                    name: CONFIG.algorithm,
                    iv: iv
                },
                key,
                ciphertext
            );

            return new Uint8Array(decrypted);
        } catch (error) {
            throw new Error(t('error_decryption'));
        }
    }

    const FORMAT_MAGIC = new Uint8Array([0x53, 0x48, 0x43, 0x4B]);

    function readFileSlice(file, start, end) {
        return new Promise((resolve, reject) => {
            const reader = new FileReader();
            reader.onload = () => resolve(new Uint8Array(reader.result));
            reader.onerror = () => reject(new Error('Failed to read file'));
            reader.readAsArrayBuffer(file.slice(start, end));
        });
    }

    function blobToUint8Array(blob) {
        return new Promise((resolve, reject) => {
            const reader = new FileReader();
            reader.onload = () => resolve(new Uint8Array(reader.result));
            reader.onerror = () => reject(new Error('Failed to read blob'));
            reader.readAsArrayBuffer(blob);
        });
    }

    async function encryptFileChunked(file, password, chunkSize, onChunk, { concurrency = 1 } = {}) {
        const salt = generateRandomBytes(CONFIG.saltLength);
        const key = await deriveKey(password, salt);
        const totalChunks = Math.ceil(file.size / chunkSize);

        const inflight = new Set();
        const errors = [];

        const startUpload = (i, chunkData) => {
            const p = onChunk(i, chunkData);
            const cleanup = () => { inflight.delete(p); };
            const wrapped = p.then(cleanup, (err) => { cleanup(); errors.push(err); });
            inflight.add(wrapped);
            return wrapped;
        };

        for (let i = 0; i < totalChunks; i++) {
            const start = i * chunkSize;
            const end = Math.min(start + chunkSize, file.size);
            const plaintext = await readFileSlice(file, start, end);

            const iv = generateRandomBytes(CONFIG.ivLength);
            const ciphertext = await crypto.subtle.encrypt(
                { name: CONFIG.algorithm, iv },
                key,
                plaintext
            );

            const isFirstChunk = i === 0;
            const chunkData = new Uint8Array(
                (isFirstChunk ? FORMAT_MAGIC.length + salt.length : 0) +
                iv.length + ciphertext.byteLength
            );
            let offset = 0;
            if (isFirstChunk) {
                chunkData.set(FORMAT_MAGIC, offset); offset += FORMAT_MAGIC.length;
                chunkData.set(salt, offset); offset += salt.length;
            }
            chunkData.set(iv, offset); offset += iv.length;
            chunkData.set(new Uint8Array(ciphertext), offset);

            if (inflight.size >= concurrency) {
                await Promise.race(inflight);
            }
            startUpload(i, chunkData);
        }

        await Promise.all(inflight);
        if (errors.length) throw errors[0];
    }

    async function decryptFileChunked(encryptedBlob, password, originalFileSize, onProgress) {
        const data = await blobToUint8Array(encryptedBlob);
        const hasMagic =
            data.length >= 4 &&
            data[0] === FORMAT_MAGIC[0] && data[1] === FORMAT_MAGIC[1] &&
            data[2] === FORMAT_MAGIC[2] && data[3] === FORMAT_MAGIC[3];

        if (!hasMagic) {
            if (onProgress) onProgress(0, t('status_decrypting'));
            const result = await decrypt(data, password);
            if (onProgress) onProgress(100, t('status_decryption_complete'));
            return result;
        }

        const salt = data.slice(FORMAT_MAGIC.length, FORMAT_MAGIC.length + CONFIG.saltLength);
        const key = await deriveKey(password, salt);
        const CHUNK_SIZE = 5 * 1024 * 1024;
        const totalChunks = Math.ceil(originalFileSize / CHUNK_SIZE);
        const resultParts = [];
        let offset = FORMAT_MAGIC.length + CONFIG.saltLength;

        for (let i = 0; i < totalChunks; i++) {
            const plaintextSize = Math.min(CHUNK_SIZE, originalFileSize - i * CHUNK_SIZE);
            const encryptedChunkSize = CONFIG.ivLength + plaintextSize + 16;

            const iv = data.slice(offset, offset + CONFIG.ivLength);
            const ciphertext = data.slice(offset + CONFIG.ivLength, offset + encryptedChunkSize);

            let decrypted;
            try {
                decrypted = await crypto.subtle.decrypt(
                    { name: CONFIG.algorithm, iv }, key, ciphertext
                );
            } catch (e) {
                throw new Error(t('error_decryption'));
            }
            resultParts.push(new Uint8Array(decrypted));

            offset += encryptedChunkSize;
            if (onProgress) {
                onProgress(Math.round(((i + 1) / totalChunks) * 100), t('status_decrypting'));
            }
        }

        const totalSize = resultParts.reduce((sum, p) => sum + p.length, 0);
        const result = new Uint8Array(totalSize);
        let pos = 0;
        for (const part of resultParts) {
            result.set(part, pos);
            pos += part.length;
        }
        return result;
    }

    async function encryptFile(file, password, onProgress) {
        return new Promise((resolve, reject) => {
            const reader = new FileReader();

            reader.onload = async function(e) {
                try {
                    if (onProgress) onProgress(0, t('status_encrypting'));
                    
                    const data = new Uint8Array(e.target.result);
                    const encrypted = await encrypt(data, password);
                    
                    if (onProgress) onProgress(100, t('status_encryption_complete'));
                    
                    resolve(new Blob([encrypted], { type: 'application/octet-stream' }));
                } catch (error) {
                    reject(error);
                }
            };

            reader.onerror = function() {
reject(new Error(t('error_failed_read_file')));
            };

            reader.readAsArrayBuffer(file);
        });
    }

    function computeOriginalFileSize(encryptedLength) {
        const baseOverhead = FORMAT_MAGIC.length + CONFIG.saltLength;
        const chunkOverhead = CONFIG.ivLength + 16;
        const CHUNK_SIZE = 5 * 1024 * 1024;
        const dataSize = encryptedLength - baseOverhead;
        const numChunks = Math.ceil(dataSize / (CHUNK_SIZE + chunkOverhead));
        return encryptedLength - baseOverhead - numChunks * chunkOverhead;
    }

    async function decryptBlob(blob, password, onProgress) {
        return new Promise((resolve, reject) => {
            const reader = new FileReader();

            reader.onload = async function(e) {
                try {
                    if (onProgress) onProgress(0, t('status_decrypting'));

                    const data = new Uint8Array(e.target.result);

                    const hasMagic =
                        data.length >= 4 &&
                        data[0] === FORMAT_MAGIC[0] && data[1] === FORMAT_MAGIC[1] &&
                        data[2] === FORMAT_MAGIC[2] && data[3] === FORMAT_MAGIC[3];

                    let decrypted;
                    if (!hasMagic) {
                        decrypted = await decrypt(data, password);
                    } else {
                        const originalSize = computeOriginalFileSize(data.length);
                        decrypted = await decryptFileChunked(blob, password, originalSize, onProgress);
                    }

                    if (onProgress) onProgress(100, t('status_decryption_complete'));

                    resolve(decrypted);
                } catch (error) {
                    reject(error);
                }
            };

            reader.onerror = function() {
                reject(new Error(t('error_failed_read_encrypted')));
            };

            reader.readAsArrayBuffer(blob);
        });
    }

    function validatePassword(password) {
        if (!password || typeof password !== 'string') {
            return { valid: false, error: t('crypto_password_required') };
        }

        const words = password.toLowerCase().trim().split('-');
        
        if (words.length !== CONFIG.wordCount) {
            return { 
                valid: false, 
                error: tpl('crypto_password_words', {count: CONFIG.wordCount}) 
            };
        }

        for (const word of words) {
            if (!/^[a-z]+$/.test(word)) {
                return { 
                    valid: false, 
                    error: t('crypto_password_letters') 
                };
            }
            if (word.length < 2) {
                return { 
                    valid: false, 
                    error: t('crypto_password_length') 
                };
            }
        }

        return { valid: true };
    }

    function getPasswordFromHash() {
        const hash = window.location.hash;
        if (!hash || hash.length <= 1) {
            return null;
        }
        return decodeURIComponent(hash.substring(1));
    }

    function formatFileSize(bytes) {
        if (bytes === 0) return t('format_bytes');
        
        const k = 1024;
        const sizes = [t('format_bytes_label'), t('format_kb_label'), t('format_mb_label'), t('format_gb_label')];
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        
        return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
    }

    function formatDate(dateString) {
        const date = new Date(dateString);
        return date.toLocaleString();
    }

    function getTimeRemaining(expiresAt) {
        const now = new Date();
        const expires = new Date(expiresAt);
        const diff = expires - now;

        if (diff <= 0) {
            return t('format_expired');
        }

        const days = Math.floor(diff / (1000 * 60 * 60 * 24));
        const hours = Math.floor((diff % (1000 * 60 * 60 * 24)) / (1000 * 60 * 60));
        const minutes = Math.floor((diff % (1000 * 60 * 60)) / (1000 * 60));

        if (days > 0) {
            return tpl('format_time_remaining_days', {days, hours});
        } else if (hours > 0) {
            return tpl('format_time_remaining_hours', {hours, minutes});
        } else {
            return tpl('format_time_remaining_minutes', {minutes});
        }
    }

     
    return {
        generatePassword,
        encryptFile,
        decryptBlob,
        encryptFileChunked,
        decryptFileChunked,
        validatePassword,
        getPasswordFromHash,
        formatFileSize,
        formatDate,
        getTimeRemaining,
        loadWordList,
        toBase64,
        fromBase64,
        getOrCreateDeviceIdentity,
        getLegacyUserKeyRaw,
        unwrapWithLegacyUserKey,
        rsaOaepEncrypt,
        rsaOaepDecrypt,
        isSameRsaKey,
        parseEnvelope,
        unwrapFileDEK,
        wrapFileDEKForIdentity,
        buildOwnerEnvelope,
        registerAuthenticatedDevice,
        cacheFileKey,
        getCachedFileKey,
        removeCachedFileKey,
        generateIdentityKeypair,
        wrapIdentityKeyForDevice,
        unwrapIdentityKeyForDevice,
        saveIdentityKey,
        getIdentityKey
    };
})();

 
window.SecureCrypto = SecureCrypto;