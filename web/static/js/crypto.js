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

    async function getOrCreateDeviceIdentity() {
        const cached = localStorage.getItem(DEVICE_STORAGE_KEY);
        if (cached) {
            return JSON.parse(cached);
        }

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
        localStorage.setItem(DEVICE_STORAGE_KEY, JSON.stringify(identity));
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
        cacheFileKey,
        getCachedFileKey,
        removeCachedFileKey
    };
})();

 
window.SecureCrypto = SecureCrypto;