# [Sendly](https://sendly.cns-studios.com)

**Share files privately.** Sendly is an end-to-end encrypted file sharing service: files are encrypted in your browser before they leave it, so the server only ever stores ciphertext it cannot read.

Built by [CNS Studios](https://cns-studios.com). No account is needed to share a file.

## Features

- **Share by link.** Upload a file and send the link. The decryption key lives in the URL fragment, which is never sent to the server. Recipients can also open a file with a short numeric code.
- **Quick share.** Open a temporary room (10 minutes to 24 hours), let others join with a code or QR, approve each person by comparing key fingerprints, and swap files. No accounts required.
- **Send to a person.** Signed-in users send a file straight to another user, who accepts or declines it. The file key is wrapped for the recipient's public key.
- **Accounts and trusted devices.** Sign in with CNS Auth and reach your files from every device. New devices are approved by an existing one, and a recovery path exists if you lose them all.
- **Large files.** Chunked uploads up to 750 MB for guests and 1.5 GB for accounts.
- **Automatic expiry.** Files are deleted after 7 days (guests) or 90 days (accounts), with background cleanup and data retention.
- **Abuse reporting.** Anyone can report a file; enough distinct reporters remove it automatically.
- **English and German**, with no third-party scripts, fonts or trackers.

## Security at a glance

- **End-to-end encryption.** Files are encrypted in the browser with WebCrypto. Keys, session passwords and private keys never reach the server in the clear.
- **Zero-knowledge storage.** A full database and disk leak exposes ciphertext and wrapped keys only.
- **Per-account identity keys.** Private keys exist only in your browsers and move between devices only through approval with a verification code.
- **Hardened by default.** Strict Content Security Policy, CSRF protection, PKCE sign-in, layered rate limiting, host-approved quick share rooms and spoof-resistant client IP handling.

Details and trust assumptions: [docs/SECURITY.md](docs/SECURITY.md).

## Documentation

| Doc | What you will find |
|---|---|
| [Architecture](docs/ARCHITECTURE.md) | Features in depth, system design, upload flow, device and identity keys, data model, background jobs. |
| [Security](docs/SECURITY.md) | Threat model, encryption design, authorization, abuse prevention, CSP, hardening checklist. |
| [API](docs/API.md) | Every HTTP endpoint with requests, responses, limits and error codes. |
| [Operations](docs/OPERATIONS.md) | Running Sendly, every configuration variable, health checks, cleanup, storage safety, troubleshooting. |

## Run it

Needs Docker with Compose, or Go with PostgreSQL and Redis.

```bash
cp .env.example .env   # adjust values
make dev-full          # build and start the stack on http://localhost:8085
```

The `Makefile` covers the rest (`make logs`, `make down`, `make prod-up`, `make migrate`); see [Operations](docs/OPERATIONS.md) for details.

## License

Licensed under the [AGPL-3.0](LICENSE).
