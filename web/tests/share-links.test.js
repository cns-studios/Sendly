'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { createHmac } = require('node:crypto');

const CRYPTO_JS = fs.readFileSync(path.join(__dirname, '../static/js/crypto.js'), 'utf8');
const WORDLIST = fs.readFileSync(path.join(__dirname, '../static/wordlist.txt'), 'utf8');
const CHUNK_SIZE = 5 * 1024 * 1024;

const LEGACY = {
    passphrase: 'maple-orbit-cactus-ribbon-velvet',
    plaintext: 'legacy share link payload',
    chunked: 'U0hDS5g2hTMKLJPQGfP/xV1/8Gqx9HjO0ZUUdASK94ZXv8qNShGESk5jbdUSCUKVYyuJezHj4d3QoILQ8FbRaNBWeX9O6d7lwg==',
    single: 'TD4C7+JXtnetU5YQ30BF6vV+UN2LNVQOnEeGDlf1h3ENnX4cyYR9UEJpGGs2i01tWswx/hlDh/lc9A12/GLZETgqwDtq'
};

class FileReader {
    readAsArrayBuffer(blob) {
        blob.arrayBuffer().then((result) => {
            this.result = result;
            if (this.onload) this.onload({ target: { result } });
        }, (error) => {
            if (this.onerror) this.onerror(error);
        });
    }
}

function loadSecureCrypto({ hash = '', baseURL } = {}) {
    const window = { location: { hash, origin: 'https://origin.test' }, CONFIG: baseURL === undefined ? undefined : { baseURL } };
    const context = {
        window, crypto: globalThis.crypto, TextEncoder, TextDecoder, Blob, FileReader, btoa, atob, console,
        DOMException, fetch: async () => ({ text: async () => WORDLIST })
    };
    vm.createContext(context);
    vm.runInContext(`${CRYPTO_JS}\nthis.SecureCrypto = SecureCrypto;`, context);
    return context.SecureCrypto;
}

function expectedFileId(token) {
    const mac = createHmac('sha256', Buffer.from(token, 'utf8')).update('sendly-link-file-id-v1').digest();
    const value = BigInt(`0x${mac.subarray(0, 16).toString('hex')}`);
    return (value % (36n ** 17n)).toString(36).padStart(17, '0');
}

async function encryptToBytes(SecureCrypto, data, secret) {
    const parts = [];
    await SecureCrypto.encryptFileChunked(new Blob([data]), secret, CHUNK_SIZE, async (index, chunk) => { parts[index] = chunk; });
    return Buffer.concat(parts.map((part) => Buffer.from(part)));
}

test('link tokens are 22 url-safe characters carrying 128 random bits', () => {
    const SecureCrypto = loadSecureCrypto();
    const seen = new Set();
    for (let i = 0; i < 500; i++) {
        const token = SecureCrypto.generateLinkToken();
        assert.match(token, /^[A-Za-z0-9_-]{22}$/);
        assert.ok(SecureCrypto.isLinkToken(token));
        assert.equal(Buffer.from(token, 'base64url').length, 16);
        seen.add(token);
    }
    assert.equal(seen.size, 500);
});

test('legacy word passphrases are not mistaken for link tokens', () => {
    const SecureCrypto = loadSecureCrypto();
    for (const value of [LEGACY.passphrase, '', null, undefined, 'a'.repeat(21), 'a'.repeat(23), 'aaaaaaaaaaaaaaaaaaaaa+', 'aaaaaaaaaaaaaaaaaaaaa=']) {
        assert.equal(SecureCrypto.isLinkToken(value), false, String(value));
    }
});

test('the file id is an HMAC of the token in the existing 17 character id format', async () => {
    const SecureCrypto = loadSecureCrypto();
    const vectors = ['AAAAAAAAAAAAAAAAAAAAAA', 'q8Vn2kD0xYbLr5WcT1mZaE', '_-_-_-_-_-_-_-_-_-_-_-'];
    for (let i = 0; i < 50; i++) vectors.push(SecureCrypto.generateLinkToken());
    const ids = new Set();
    for (const token of vectors) {
        const id = await SecureCrypto.fileIdFromLinkToken(token);
        assert.match(id, /^[a-z0-9]{17}$/);
        assert.equal(id, expectedFileId(token));
        assert.equal(await SecureCrypto.fileIdFromLinkToken(token), id);
        ids.add(id);
    }
    assert.equal(ids.size, vectors.length);
    await assert.rejects(SecureCrypto.fileIdFromLinkToken(LEGACY.passphrase));
});

test('new links are /s#token on the configured base url', () => {
    assert.equal(loadSecureCrypto({ baseURL: 'https://sendly.zip/' }).linkShareUrl('q8Vn2kD0xYbLr5WcT1mZaE'), 'https://sendly.zip/s#q8Vn2kD0xYbLr5WcT1mZaE');
    assert.equal(loadSecureCrypto().linkShareUrl('q8Vn2kD0xYbLr5WcT1mZaE'), 'https://origin.test/s#q8Vn2kD0xYbLr5WcT1mZaE');
});

test('copying a link from the uploads list keeps the format the file was made with', async () => {
    const SecureCrypto = loadSecureCrypto({ baseURL: 'https://sendly.zip' });
    const token = SecureCrypto.generateLinkToken();
    const id = await SecureCrypto.fileIdFromLinkToken(token);
    assert.equal(await SecureCrypto.shareLinkFor(id, `https://sendly.zip/shared/${id}`, token), `https://sendly.zip/s#${token}`);
    assert.equal(
        await SecureCrypto.shareLinkFor('k3x9q2m8v1p0z7r4t', 'https://sendly.zip/shared/k3x9q2m8v1p0z7r4t', LEGACY.passphrase),
        `https://sendly.zip/shared/k3x9q2m8v1p0z7r4t#${LEGACY.passphrase}`
    );
    assert.equal(
        await SecureCrypto.shareLinkFor('k3x9q2m8v1p0z7r4t', 'https://sendly.zip/shared/k3x9q2m8v1p0z7r4t', token),
        `https://sendly.zip/shared/k3x9q2m8v1p0z7r4t#${token}`
    );
});

test('the key is read from the fragment for both link formats', () => {
    assert.equal(loadSecureCrypto({ hash: '#q8Vn2kD0xYbLr5WcT1mZaE' }).getLinkSecretFromHash(), 'q8Vn2kD0xYbLr5WcT1mZaE');
    assert.equal(loadSecureCrypto({ hash: `#${LEGACY.passphrase}` }).getLinkSecretFromHash(), LEGACY.passphrase);
    assert.equal(loadSecureCrypto({ hash: '' }).getLinkSecretFromHash(), null);
    assert.equal(loadSecureCrypto({ hash: '#' }).getLinkSecretFromHash(), null);
    assert.equal(loadSecureCrypto({ hash: '#%E0%A4%A' }).getLinkSecretFromHash(), null);
});

test('files encrypted before short links still decrypt', async () => {
    const SecureCrypto = loadSecureCrypto();
    const chunked = new Blob([Buffer.from(LEGACY.chunked, 'base64')]);
    const single = new Blob([Buffer.from(LEGACY.single, 'base64')]);
    const size = Buffer.byteLength(LEGACY.plaintext);
    assert.equal(Buffer.from(await SecureCrypto.decryptFileChunked(chunked, LEGACY.passphrase, size)).toString(), LEGACY.plaintext);
    assert.equal(Buffer.from(await SecureCrypto.decryptBlob(chunked, LEGACY.passphrase)).toString(), LEGACY.plaintext);
    assert.equal(Buffer.from(await SecureCrypto.decryptBlob(single, LEGACY.passphrase)).toString(), LEGACY.plaintext);
    await assert.rejects(SecureCrypto.decryptFileChunked(chunked, 'wrong-words-for-this-old-file', size));
});

test('short link uploads use the existing blob format across chunks', async () => {
    const SecureCrypto = loadSecureCrypto();
    const token = SecureCrypto.generateLinkToken();
    const data = Buffer.alloc(CHUNK_SIZE + 1234);
    for (let i = 0; i < data.length; i++) data[i] = i % 251;
    const encrypted = await encryptToBytes(SecureCrypto, data, token);
    assert.equal(encrypted.subarray(0, 4).toString(), 'SHCK');
    assert.ok(Buffer.from(await SecureCrypto.decryptFileChunked(new Blob([encrypted]), token, data.length)).equals(data));
    assert.ok(Buffer.from(await SecureCrypto.decryptBlob(new Blob([encrypted]), token)).equals(data));
    await assert.rejects(SecureCrypto.decryptFileChunked(new Blob([encrypted]), SecureCrypto.generateLinkToken(), data.length));
});

test('quick share session passwords are still word passphrases', async () => {
    const SecureCrypto = loadSecureCrypto();
    const password = await SecureCrypto.generatePassword();
    assert.match(password, /^[a-z]+(-[a-z]+){4}$/);
    const encrypted = await encryptToBytes(SecureCrypto, Buffer.from('quick share'), password);
    assert.equal(Buffer.from(await SecureCrypto.decryptBlob(new Blob([encrypted]), password)).toString(), 'quick share');
});
