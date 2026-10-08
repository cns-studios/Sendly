# Sendly Privacy Policy

*Last updated: Oct 08, 2026*

## 1. Who is responsible

The controller under Art. 4(7) GDPR is:

**Aaron Gerkens**, operating as a part under the brand CNS Studios (not a registered company) Mittelstraße 79, 22869 Schenefeld, Germany Email: privacy@cns-studios.com

In this policy 'we' means the team behind CNS Studios that runs Sendly. We have not appointed a data protection officer because we are not required to (Art. 37 GDPR, § 38 BDSG).

## 2. The short version

- Your files are encrypted in your browser before upload. We store only the encrypted file and cannot read its contents. The password of a share link sits behind the # in the link, which your browser does not send to us.
- We can see: file names, file sizes, upload and expiry times, your IP address and, if you sign in, your username, avatar and who you send files to.
- We use no advertising and no tracking cookies. The only statistics we keep about usage are aggregate totals, such as the total amount of data processed. These totals are not linked to any person.
- Encrypted files are deleted automatically: after 7 days without an account, after 90 days with an account, or when a tunnel ends.
- Sendly runs on our own servers in Germany. Cloudflare sits in front of it.

## 3. What we process, why, and on what legal basis

### 3.1 Visiting Sendly

When you open a page or call our API, our servers and Cloudflare process your IP address, the time of the request, the requested path (which can contain a file ID), the HTTP status and technical request data sent by your browser. We need this to deliver pages, keep the service secure and find errors.

Legal basis: Art. 6(1)(f) GDPR (legitimate interest in operating and securing the service). Retention: see section 7.

### 3.2 Sharing files without an account (link share and quick share)

Files are encrypted in your browser with AES-256-GCM and uploaded in chunks. We store:

- the encrypted file and the encrypted key material needed to open it
- the file name in plain text (it is not encrypted), the file size, a file ID, a 12-digit code, the upload time and the expiry time
- your IP address at the time of upload and the number of reports received for the file

A link share password consists of five random words and is placed after the # in the link. Browsers do not send this part to us. If you pass the link on through a service that records full links (some messengers and email scanners do), that service may see the password.

Anyone who knows the file ID or the 12-digit code can download the encrypted file. Only people with the password can decrypt it. Quick share uses a temporary key pair created in your browser for the session; the private key stays in the browser.

Legal basis: Art. 6(1)(b) GDPR for providing the sharing function you request; Art. 6(1)(f) GDPR for the IP address (preventing abuse, enforcing limits, defending against legal claims).

### 3.3 Accounts and devices

You sign in through CNS Accounts, our own authentication service. CNS Accounts processes your login credentials including your username, a password hash and your uploaded user avatar. [Privacy Policy of CNS and CNS Accounts](https://cns-studios.com/privacy). Sendly receives access tokens and a profile (user ID, username, avatar, status), which we cache.

For encryption we store, per account, the public part of your identity key and the private part only in encrypted form, wrapped separately for each of your approved devices. For each device we store a label, its public key and the time it was revoked. When you add a device we store an enrollment record (verification code, status, expiry), and an existing device approves the new one; this is announced to your devices over a WebSocket connection. Files you upload while signed in are linked to your account and kept for up to 90 days.

Legal basis: Art. 6(1)(b) GDPR.

### 3.4 Transfers between users

When you send a file to another user we process sender and recipient IDs, usernames, file name and size, the status (pending, accepted, declined), timestamps and the key envelope for the recipient. The recipient sees your username, your avatar, and the file name and size before accepting. Sender and recipient can report a transfer.

Legal basis: Art. 6(1)(b) GDPR.

### 3.5 Tunnels

Tunnels are temporary rooms that you start and join with a code. We process the host token, each participant's public key, a hashed participant token, the time of approval and the key envelopes. The host approves or rejects each person who wants to join. We keep a record of rejections including the IP address so that the host's decision can be enforced. Files shared in a tunnel expire when the tunnel ends.

Legal basis: Art. 6(1)(b) GDPR; for rejection records Art. 6(1)(f) GDPR.

### 3.6 Reports and moderation

Anyone can report a file or transfer. We process the file ID and file name, the time, your IP address (if you are not signed in) or your user ID (if you are), and anything you write to us. We cannot look inside encrypted files, so we act on the report, the file ID and the surrounding metadata.  A file is deleted automatically when 3 different signed-in users have reported it. Reports from users only notify us.

We receive notifications about reports, automatic deletions, cleanup summaries and administrator actions (viewing, deleting or downloading the encrypted file) through a Discord webhook. These messages contain file names, IP addresses, user IDs, user IPs, file IDs and share links.

Legal basis: Art. 6(1)(f) GDPR (protecting users and third parties from illegal content) and, where we must handle notices, Art. 6(1)(c) GDPR in connection with Art. 16 of the Digital Services Act.

### 3.7 Security and rate limiting

We count requests per IP address in a short-lived memory store (Redis) to block abuse. Upload sessions and chunk status are kept for up to one hour, pending uploads for ten minutes. Legal basis: Art. 6(1)(f) GDPR.

### 3.8 Contacting us

If you write to us we process your email address, the content of your message and any file IDs you mention, in order to answer you. Legal basis: Art. 6(1)(b) or (f) GDPR.

### 3.9 Statistics

Every five minutes our servers sends aggregate counters (bytes uploaded and processed) to our own reporting tool. They contain no personal data.

### 3.10 No tracking

We do not use advertising, analytics or profiling except from Cloudflare on our behalf: see section 3.11

### 3.11 Cloudflare

All traffic to Sendly passes through the network of Cloudflare, Inc. (USA), which acts as a protective and delivery layer in front of our servers. This covers page loads, local-mode code, and online-mode uploads and downloads.

- Cloudflare ends the encrypted connection (TLS) at its servers and forwards the traffic to ours. **This means Cloudflare can technically see traffic in transit, including files uploaded and your IP address.**
- Cloudflare processes this data on our behalf under a data processing agreement (Art. 28 GDPR). It also applies rate limiting for us and provides basic traffic statistics. For its own security and network operation, Cloudflare processes some data as an independent controller. See Cloudflare's privacy policy for details.
- Cloudflare may set technical cookies for security purposes (section 4).
- Legal basis: Art. 6(1)(f) GDPR. Our legitimate interest is a secure, available service protected against attacks.


## 4. Cookies and local storage

Sendly stores the following on your device:

- a security cookie (csrf_token) that protects against forged requests
- your sign-in tokens
- your encryption keys (sendly\_device\_identity\_v1 and sendly\_identity\_key\_v1\_\*). If you clear your browser data you may lose access to files that depend on these keys.
- your list of recent files and your language setting

All of this is strictly necessary to provide the service you asked for. Under § 25(2) no. 2 TDDDG no consent is needed, so there is no cookie banner. Cloudflare may set its own technical cookies for security.

## 5. Who receives your data

- **Cloudflare, Inc. (USA)** as processor (CDN and proxy). It handles encrypted traffic and request metadata such as IP addresses.
- **Hosting:** Sendly runs on our own servers in Germany, so no hosting company processes the data.
- **Discord Inc. (USA)** for moderation notifications as described in 3.6.
- **CNS Accounts**, operated by us.
- **Other users:** recipients and tunnel participants see the information described above.
- **Authorities:** only if a valid legal order or legal obligation requires it. We can hand over metadata and encrypted files, not file contents.

We do not sell your data.

## 6. Transfers outside the EU/EEA

Cloudflare and Discord are based in the USA. Transfers rely on the EU–US Data Privacy Framework adequacy decision for certified companies and, additionally, on standard contractual clauses (Art. 46 GDPR). You can request a copy of the safeguards from us.

## 7. How long we keep data

- **Encrypted files:** 7 days (no account), 90 days (account), or until the tunnel ends. A cleanup job runs every 5 minutes. Files are deleted earlier if you or a moderator delete them.
- **File records** (file name, size, IP address, owner, report count): marked deleted at expiry and erased after 30 days.
- **Upload statistics by IP address:** 30 days.
- **Reports** (including reporter IP or user ID): 90 days after the report is closed.
- **Tunnel rejection records:** 30 days.
- **Server and proxy logs:** 180 days.
- **Rate limit counters:** about one minute. **Upload sessions:** up to one hour.
- **Account data:** while your account exists. Cached profile data is deactivated if it is not refreshed for 30 days.
- **Emails to us:** until your request is resolved, then up to 12 months.
- **Cloudflare:** according to its own retention periods.

Legal retention duties or ongoing legal disputes can extend these periods for the data affected.

## 8. Security

Content is encrypted end-to-end with AES-256-GCM, keys are wrapped with RSA-OAEP, and traffic to Sendly is encrypted with TLS. The metadata described above (file names, sizes, IP addresses) is not end-to-end encrypted. No system is perfectly secure, and we cannot promise that data can never be accessed by unauthorized parties.

## 9. Your rights

You have the right to access (Art. 15), rectification (Art. 16), erasure (Art. 17), restriction (Art. 18), data portability (Art. 20) and to object to processing based on legitimate interests (Art. 21). Contact us at privacy@cns-studios.com. We respond within one month.

Because we cannot read encrypted files and anonymous uploads are not tied to a name, we can only find data for you if you give us the file ID, link or 12-digit code (Art. 11 GDPR). We cannot give you the contents of files.

You also have the right to complain to a supervisory authority. For us this is the Unabhängiges Landeszentrum für Datenschutz Schleswig-Holstein, or the authority in your country of residence.

## 10. Automated decisions

We do not make decisions with legal effect based solely on automated processing. Files can be removed automatically after reports from several signed-in users. You can contest this by emailing contact@cns-studios.com (see section 9 of the Terms of Service); a person then reviews the decision.

## 11. Minimum age

Sendly is for people aged 16 or older.

## 12. Changes

We will update this policy if the service or the law changes and show the date at the top. Material changes will be announced in the service.
