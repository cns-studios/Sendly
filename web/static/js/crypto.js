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

    function userKeyStorageKey(userId) {
        return `${USER_KEY_PREFIX}${userId || 'guest'}`;
    }

    function saveUserKeyRaw(userId, keyRaw) {
        localStorage.setItem(userKeyStorageKey(userId), toBase64(keyRaw));
    }

    function getUserKeyRaw(userId) {
        const value = localStorage.getItem(userKeyStorageKey(userId));
        return value ? fromBase64(value) : null;
    }

    function identityKeyStorageKey(userId) {
        return `${IDENTITY_KEY_PREFIX}${userId || 'guest'}`;
    }

    function saveIdentityKey(userId, keyData) {
        if (!keyData) return;
        localStorage.setItem(identityKeyStorageKey(userId), JSON.stringify(keyData));
    }

    function clearIdentityKey(userId) {
        localStorage.removeItem(identityKeyStorageKey(userId));
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

    function generateUserKeyRaw() {
        return generateRandomBytes(32);
    }

    async function importUserKey(rawKey) {
        return crypto.subtle.importKey(
            'raw',
            rawKey,
            { name: 'AES-GCM' },
            false,
            ['encrypt', 'decrypt']
        );
    }

    async function wrapSecretWithUserKey(secretBytes, userKeyRaw) {
        const iv = generateRandomBytes(12);
        const key = await importUserKey(userKeyRaw);
        const wrapped = await crypto.subtle.encrypt(
            { name: 'AES-GCM', iv },
            key,
            secretBytes
        );
        return {
            wrapped: new Uint8Array(wrapped),
            nonce: iv
        };
    }

    async function unwrapSecretWithUserKey(wrappedBytes, nonceBytes, userKeyRaw) {
        const key = await importUserKey(userKeyRaw);
        const raw = await crypto.subtle.decrypt(
            { name: 'AES-GCM', iv: nonceBytes },
            key,
            wrappedBytes
        );
        return new Uint8Array(raw);
    }

    async function wrapUserKeyForDevice(userKeyRaw, publicKeyJWK) {
        const publicKey = await crypto.subtle.importKey(
            'jwk',
            publicKeyJWK,
            { name: 'RSA-OAEP', hash: 'SHA-256' },
            false,
            ['encrypt']
        );
        const wrapped = await crypto.subtle.encrypt({ name: 'RSA-OAEP' }, publicKey, userKeyRaw);
        return new Uint8Array(wrapped);
    }

    async function unwrapUserKeyForDevice(wrappedUserKeyBytes, privateKeyJWK) {
        const privateKey = await crypto.subtle.importKey(
            'jwk',
            privateKeyJWK,
            { name: 'RSA-OAEP', hash: 'SHA-256' },
            false,
            ['decrypt']
        );
        const raw = await crypto.subtle.decrypt({ name: 'RSA-OAEP' }, privateKey, wrappedUserKeyBytes);
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
        const wrappedAesKey = await wrapUserKeyForDevice(rawAesKey, devicePublicKeyJWK);
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

        const rawAesKey = await unwrapUserKeyForDevice(wrappedAesKey, devicePrivateKeyJWK);
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

    async function unwrapFileDEK(envelope, {
        authenticated = !!window.CONFIG?.authenticated,
        deviceIdentity = null,
        userKeyRaw = null,
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
                : null) || (deviceIdentity && deviceIdentity.privateKeyJWK
                ? await crypto.subtle.importKey('jwk', deviceIdentity.privateKeyJWK,
                    { name: 'RSA-OAEP', hash: 'SHA-256' }, false, ['decrypt'])
                : null);
            if (!privateKey) throw new Error('No private key available for file envelope');
            const raw = await crypto.subtle.decrypt({ name: 'RSA-OAEP' }, privateKey, parsed.wrappedBytes);
            return new Uint8Array(raw);
        }
        if (!authenticated) throw new Error('Unsupported key envelope for guest decryption');
        if (!userKeyRaw) throw new Error('Missing user key for file envelope');
        return unwrapSecretWithUserKey(parsed.wrappedBytes, parsed.nonceBytes, userKeyRaw);
    }

    async function wrapFileDEKForDevice(dekBytes, publicKeyJWK) {
        return wrapUserKeyForDevice(dekBytes, publicKeyJWK);
    }

    async function wrapFileDEKForIdentity(dekBytes, identityPublicKeyJWK) {
        return wrapUserKeyForDevice(dekBytes, identityPublicKeyJWK);
    }

    async function registerAuthenticatedDevice({
        endpoint = '/api/me/devices/register',
        userId = window.CONFIG?.cnsUserId || 0,
        username = window.CONFIG?.cnsUsername || '',
        csrfToken = '',
        includeBootstrapEnvelope = true
    } = {}) {
        let identity = await getOrCreateDeviceIdentity(userId);
        let userKeyRaw = getUserKeyRaw(userId);
        let bootstrapKey = null;
        let wrappedUserKeyB64 = '';
        let ukWrapAlg = '';
        let ukWrapMeta = {};

        // Identity keypair state
        let identityKey = getIdentityKey(userId);
        let wrappedIdentityPrivateKeyB64 = '';
        let identityKeyWrapAlg = '';
        let identityKeyWrapMeta = {};
        let identityPublicKeyJWK = null;
        let identityKeyAlgorithm = '';
        let identityKeyVersion = 1;

        if (includeBootstrapEnvelope) {
            if (!userKeyRaw) {
                bootstrapKey = generateUserKeyRaw();
                userKeyRaw = bootstrapKey;
            }
            wrappedUserKeyB64 = toBase64(await wrapUserKeyForDevice(userKeyRaw, identity.publicKeyJWK));
            ukWrapAlg = 'RSA-OAEP-2048-v1';
            ukWrapMeta = { type: 'self-wrap', device_id: identity.deviceId };

            if (!identityKey) {
                identityKey = await generateIdentityKeypair();
            }
            const wrappedIdKeyBytes = await wrapIdentityKeyForDevice(identityKey.privateKeyJWK, identity.publicKeyJWK);
            wrappedIdentityPrivateKeyB64 = toBase64(wrappedIdKeyBytes);
            identityKeyWrapAlg = 'RSA-OAEP-2048+AES-GCM-256-v1';
            identityKeyWrapMeta = { type: 'self-wrap', device_id: identity.deviceId };
            identityPublicKeyJWK = identityKey.publicKeyJWK;
            identityKeyAlgorithm = identityKey.keyAlgorithm || 'RSA-OAEP-2048';
            identityKeyVersion = identityKey.keyVersion || 1;
        }

        const buildDeviceFields = (identity) => ({
            device_id: identity.deviceId,
            device_label: `${username || t('user_default')} device`,
            public_key_jwk: identity.publicKeyJWK,
            key_algorithm: identity.keyAlgorithm,
            key_version: identity.keyVersion
        });
        const reqBody = {
            ...buildDeviceFields(identity),
            wrapped_user_key_b64: wrappedUserKeyB64,
            uk_wrap_alg: ukWrapAlg,
            uk_wrap_meta: ukWrapMeta
        };
        if (wrappedIdentityPrivateKeyB64) {
            reqBody.identity_public_key_jwk = identityPublicKeyJWK;
            reqBody.identity_key_algorithm = identityKeyAlgorithm;
            reqBody.identity_key_version = identityKeyVersion;
            reqBody.wrapped_identity_private_key_b64 = wrappedIdentityPrivateKeyB64;
            reqBody.identity_key_wrap_alg = identityKeyWrapAlg;
            reqBody.identity_key_wrap_meta = identityKeyWrapMeta;
        }

        const headers = { 'Content-Type': 'application/json' };
        if (csrfToken) headers['X-CSRF-Token'] = csrfToken;
        let response = await fetch(endpoint, {
            method: 'POST',
            headers,
            body: JSON.stringify(reqBody)
        });
        if (response.status === 409 && userId) {
            const conflict = await response.clone().json().catch(() => ({}));
            if (conflict.code === 'DEVICE_ID_CONFLICT') {
                // This browser's device id is registered to another account
                // (shared browser). Give this account its own device identity
                // and re-wrap the self-wrapped keys for it.
                identity = await createUserScopedDeviceIdentity(userId);
                Object.assign(reqBody, buildDeviceFields(identity));
                if (includeBootstrapEnvelope) {
                    reqBody.wrapped_user_key_b64 = toBase64(await wrapUserKeyForDevice(userKeyRaw, identity.publicKeyJWK));
                    reqBody.uk_wrap_meta = { type: 'self-wrap', device_id: identity.deviceId };
                    if (wrappedIdentityPrivateKeyB64) {
                        reqBody.wrapped_identity_private_key_b64 = toBase64(await wrapIdentityKeyForDevice(identityKey.privateKeyJWK, identity.publicKeyJWK));
                        reqBody.identity_key_wrap_meta = { type: 'self-wrap', device_id: identity.deviceId };
                    }
                }
                response = await fetch(endpoint, {
                    method: 'POST',
                    headers,
                    body: JSON.stringify(reqBody)
                });
            }
        }
        if (!response.ok) {
            const payload = await response.json().catch(() => ({}));
            throw new Error(payload.error || 'Device registration failed');
        }
        let payload = await response.json().catch(() => ({}));
        if (!payload.needs_enrollment && !userKeyRaw && payload.user_key_envelope?.wrapped_uk_b64) {
            userKeyRaw = await unwrapUserKeyForDevice(fromBase64(payload.user_key_envelope.wrapped_uk_b64), identity.privateKeyJWK);
        }
        if (!payload.needs_enrollment && userKeyRaw) saveUserKeyRaw(userId, userKeyRaw);

        if (!payload.needs_enrollment) {
            const resolved = await resolveIdentityKey(identityKey, payload, identity);
            if (resolved.envelopeInvalid && !/\/recover$/.test(endpoint)) {
                // The stored copy isn't this account's identity key. Drop it so
                // another device can wrap the right one for this device.
                const retry = await fetch(endpoint, {
                    method: 'POST',
                    headers,
                    body: JSON.stringify({ ...reqBody, discard_identity_key_envelope: true })
                });
                if (retry.ok) payload = await retry.json().catch(() => payload);
            }
            identityKey = resolved.key;
            if (identityKey) saveIdentityKey(userId, identityKey);
            else clearIdentityKey(userId);

            if (identityKey && payload.devices_missing_identity_key?.length) {
                const distributeEndpoint = endpoint.replace(/\/(register|recover)$/, '/identity-key/envelopes');
                distributeIdentityKey(distributeEndpoint, headers, identity, identityKey, payload.devices_missing_identity_key)
                    .catch((error) => console.warn('Could not share the identity key with other devices:', error));
            }
        }

        return { identity, userKeyRaw, identityKey, payload };
    }

    // Two JWKs describe the same RSA key if modulus and exponent match; a
    // private JWK carries both, so it can be checked against a public one.
    function isSameRsaKey(jwk, publicJWK) {
        return !!jwk?.n && jwk.n === publicJWK?.n && jwk.e === publicJWK?.e;
    }

    // Picks the identity private key this device should use: the one belonging
    // to the account's active identity public key. A key this device generated
    // itself is dropped when another device's key is already the account key;
    // this device then gets that key from a sibling device instead.
    async function resolveIdentityKey(localKey, payload, identity) {
        const active = payload.identity_public_key;
        const belongsToAccount = (key) => !active || isSameRsaKey(key.privateKeyJWK, active.public_key_jwk);
        const withAccountKey = (key) => active
            ? { ...key, keyVersion: active.key_version, publicKeyJWK: active.public_key_jwk }
            : key;

        if (localKey?.privateKeyJWK && belongsToAccount(localKey)) {
            return { key: withAccountKey(localKey) };
        }
        const envelope = payload.identity_key_envelope;
        if (!envelope?.wrapped_private_key_b64) return { key: null };

        let privateKeyJWK = null;
        try {
            privateKeyJWK = await unwrapIdentityKeyForDevice(fromBase64(envelope.wrapped_private_key_b64), identity.privateKeyJWK);
        } catch (_) {
            // Handled below: a copy this device can't open is as good as none.
        }
        const key = privateKeyJWK && {
            keyVersion: envelope.identity_key_version || 1,
            keyAlgorithm: 'RSA-OAEP-2048',
            privateKeyJWK
        };
        if (key && belongsToAccount(key)) return { key: withAccountKey(key) };
        return { key: null, envelopeInvalid: true };
    }

    // Wraps this device's identity private key for the account's other trusted
    // devices that have no copy of it yet.
    async function distributeIdentityKey(endpoint, headers, identity, identityKey, devices) {
        const envelopes = [];
        for (const device of devices) {
            if (!device?.device_id || device.device_id === identity.deviceId || !device.public_key_jwk) continue;
            try {
                const wrapped = await wrapIdentityKeyForDevice(identityKey.privateKeyJWK, device.public_key_jwk);
                envelopes.push({
                    device_id: device.device_id,
                    identity_key_version: identityKey.keyVersion || 1,
                    wrapped_private_key_b64: toBase64(wrapped),
                    wrap_alg: 'RSA-OAEP-2048+AES-GCM-256-v1',
                    wrap_meta: { type: 'sibling-device', sender_device_id: identity.deviceId, request_device_id: device.device_id }
                });
            } catch (error) {
                console.warn('Skipping identity key copy for device', device.device_id, error);
            }
        }
        if (!envelopes.length) return;
        const response = await fetch(endpoint, {
            method: 'POST',
            headers,
            body: JSON.stringify({ device_id: identity.deviceId, envelopes })
        });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
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
        saveUserKeyRaw,
        getUserKeyRaw,
        generateUserKeyRaw,
        wrapSecretWithUserKey,
        unwrapSecretWithUserKey,
        wrapUserKeyForDevice,
        unwrapUserKeyForDevice,
        parseEnvelope,
        unwrapFileDEK,
        wrapFileDEKForDevice,
        wrapFileDEKForIdentity,
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