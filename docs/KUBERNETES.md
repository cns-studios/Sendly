# Running Sendly on Kubernetes

Sendly runs as several identical replicas (for example on a k3s HA cluster). Any replica can serve any request, and a replica can be killed or moved at any moment without users noticing. This page lists what the cluster has to provide and why. It does not ship manifests; those live in the fleet repository.

## How replicas share state

There is no Redis and no state in a replica's memory that another replica would need. Everything shared lives in PostgreSQL or on the shared volume:

| State | Where | Notes |
|---|---|---|
| Files, keys, accounts, tunnels | PostgreSQL | As before. |
| Upload sessions and received chunks | PostgreSQL (`upload_sessions`, `upload_chunks`) | The chunks of one upload may arrive at different replicas. |
| Rate limit counters | PostgreSQL (`rate_limit_counters`, unlogged) | Lost on a crash or failover, which only resets the windows. |
| CNS token refreshes | PostgreSQL (`token_refresh_results`, unlogged) | See [Token refresh](#token-refresh). |
| Periodic jobs | PostgreSQL (`job_runs`) plus advisory locks | Cleanup and stats reporting run on one replica per interval. |
| Live events (WebSockets) | PostgreSQL `LISTEN`/`NOTIFY` on `sendly_events` | See [WebSockets](#websockets). |
| Encrypted blobs and chunks | Shared volume (`DATA_DIR`, `CHUNK_DIR`) | Ciphertext only. |

None of this changes end-to-end encryption: the server still only ever holds ciphertext and wrapped keys. The new tables hold upload bookkeeping, counters and auth tokens, never file keys.

## Components

| Workload | Notes |
|---|---|
| `sendly` Deployment, N replicas | Image `ghcr.io/cns-studios/sendly`, port `8085`. Serves pages, API and WebSockets. |
| CloudNativePG cluster | Point `POSTGRES_HOST` at its `-rw` Service. |
| ReadWriteMany volume | Mounted by every replica at `DATA_DIR` (and `CHUNK_DIR`). |

CI (`.github/workflows/ci.yml`) pushes the image on every push to `main`, tagged with the full commit SHA and with `main-<sha7>-<unix timestamp>`. The second form sorts by time, which is what Flux image automation follows (the same scheme as ILC).

## Database

- Connect to the CNPG `-rw` Service directly, **not through a pooler** (PgBouncer in transaction mode). The migration lock and the periodic-job locks are session-level advisory locks, and the realtime listener holds a `LISTEN` connection; both need a real session.
- Set `POSTGRES_SSLMODE=require` (CNPG serves TLS). Values: `disable`, `require`, `verify-ca`, `verify-full`.
- The password comes from the CNPG-generated `<cluster>-app` Secret (`password` key). Any characters are fine; the DSN is quoted.
- Migrations run at startup on every replica, serialised by an advisory lock, so rolling updates need no separate migration Job. While a rollout runs, old replicas keep serving on the new schema, so **migrations must be expand-only**: add tables and columns, never drop or rename in the same release that stops using them.
- `db/migrations/000-baseline.sql` creates the original schema (formerly `init.sql`), so an empty CNPG database needs nothing else.
- During a CNPG failover, requests fail for a few seconds. The browser retries them (see [Browser behaviour](#browser-behaviour)), running upload assemblies retry their final write for up to 2 minutes, and a replica starting while the database is unreachable waits up to 2 minutes instead of crash-looping.

## Storage

- `DATA_DIR` must be one ReadWriteMany volume shared by all replicas, for example the SMB CSI driver ILC uses. `CHUNK_DIR` must be shared too, either inside `DATA_DIR` (the default `DATA_DIR/chunks`) or another RWX volume: the chunks of one upload land on different replicas.
- The volume must be writable by the pod's UID. The image runs as UID/GID 1000; set `runAsUser`/`fsGroup` to match the volume's mount options, as ILC does (`uid=`/`gid=` on the StorageClass).
- Storage ownership: on first start the server writes `.sendly-instance` (the database's instance ID) into `DATA_DIR`. Several replicas of the same deployment share it without conflict. A different database pointed at the same directory refuses to start (see [Operations](OPERATIONS.md#storage-ownership)).
- The root filesystem can be read-only. Mount an `emptyDir` at `/tmp`: large multipart requests spill there. Only ciphertext ever passes through.

## Probes

| Probe | Endpoint | Meaning |
|---|---|---|
| startup and liveness | `GET /livez` | The process answers. No dependencies, so a database outage never restarts pods. |
| readiness | `GET /readyz` | PostgreSQL answers, storage is writable and carries this instance's marker, and the replica is not shutting down. |

`/health` still exists for Docker and answers like `/readyz` without the shutdown state.

## Shutdown and rollouts

On SIGTERM a replica:

1. fails `/readyz`,
2. closes its WebSockets with code 1012 ("service restart"), so clients reconnect to another replica at once,
3. lets in-flight requests finish, up to `SHUTDOWN_TIMEOUT_SECONDS` (default 25),
4. lets running upload assemblies finish in the time left, then releases their leases so another replica resumes them within about 10 seconds,
5. stops its background loops and exits.

What the pod spec needs:

- `preStop: sleep 10`: Kubernetes removes the pod from the Service in parallel with sending SIGTERM, and Traefik needs a moment to notice. The sleep keeps the replica serving until then.
- `terminationGracePeriodSeconds` of at least preStop + `SHUTDOWN_TIMEOUT_SECONDS` + 5, for example 45.
- `strategy.rollingUpdate: maxUnavailable: 0, maxSurge: 1`, a PodDisruptionBudget with `maxUnavailable: 1`, and a `topologySpreadConstraint` on `kubernetes.io/hostname` so replicas spread over the nodes.
- `POD_NAME` from the downward API (`metadata.name`). It names the replica in assembly leases; the hostname works too.

A replica that dies without warning (OOM kill, node failure) leaves its assembly lease behind. Another replica takes over once the lease runs out (30 seconds) and redoes the assembly from the chunks, which are only deleted after the result is recorded. The browser keeps polling meanwhile.

Downloads of single large requests (desktop and Android clients) are cut off when the shutdown timeout ends; the web client downloads in 32 MiB ranges and retries a broken range.

## Upload flow across replicas

Every step is safe to repeat, so a client can retry it after a lost response, against any replica:

- `init` creates a new session; an abandoned one expires.
- `chunk` replaces a chunk that was already received (written under a temporary name and renamed into place).
- `complete` answers the same for an upload that is already assembling or done.
- `finalize` answers with the file the first `finalize` created.

Assembly runs in the background on the replica that took the lease. The blob is built under a temporary name and renamed into place, so it is either complete or absent.

## WebSockets

`/api/me/devices/ws`, `/desktop/ws` and the Android sockets work behind Traefik with no extra configuration. An event (a transfer, an enrollment request or decision, a new desktop file) is published with `NOTIFY` and delivered by whichever replica holds the recipient's socket. Payloads above about 7.9 KB can't go through `NOTIFY` and only reach the publishing replica; current events are far below that.

The server pings every socket every 25 seconds, which keeps it open through Cloudflare (100 second idle limit) and detects dead clients. If a replica's `LISTEN` connection drops (database failover), it closes its sockets with 1012 once it reconnects, and clients reload their state on reconnecting.

## Token refresh

CNS rotates refresh tokens and revokes the whole session when a refresh token is redeemed twice. Several requests from one browser often carry the same expiring token at once, and with several replicas they land on different pods. Refreshes are therefore coordinated through PostgreSQL: the first replica redeems the token under an advisory lock and stores the result for 10 seconds, and the others reuse it. The stored result is encrypted with a key derived from the old refresh token and indexed by a hash of it, so the table is useless to anyone who doesn't already hold that token.

## Client IPs and Cloudflare

With the path Cloudflare → cloudflared → Traefik → pod, the pod's peer is Traefik. Set:

```
BEHIND_CLOUDFLARE=true
TRUSTED_PROXIES=10.42.0.0/16   # the cluster's pod CIDR (k3s default)
```

`CF-Connecting-IP` is then believed only from peers inside the pod network. Leaving `TRUSTED_PROXIES` empty makes every client appear as Traefik, so all guests share one rate limit.

## Network policy

Unlike ILC's workers, Sendly needs egress to the internet: CNS (`CNS_AUTH_URL`, `CNS_SERVICE_API_URL`), the Discord webhook and the stats bot, all on 443. Ingress comes only from Traefik on 8085. Egress to the database on 5432 and to DNS.

## Moving the existing deployment

1. Stop writes on the old host (or accept that uploads in progress at the cut-over are lost: their sessions were in Redis and are not migrated).
2. `pg_dump` the old database and restore it into the CNPG cluster. `instance_meta` comes along, so the instance ID stays the same.
3. Copy the old `DATA_DIR` (`/mnt/shareit`) to the new volume, including `.sendly-instance`. Its marker matches the restored database, so no `SENDLY_ADOPT_DATA_DIR` is needed. If the marker is missing, start once with `SENDLY_ADOPT_DATA_DIR=true`.
4. Start the Deployment, check `/readyz`, then move the DNS / tunnel route.

Rate limit windows start fresh, and signed-in users stay signed in (sessions live in their cookies).

## Chaos test

`scripts/chaos.sh` uploads files through the public API while deleting Sendly pods at random, then checks that every upload finalizes and downloads with the right size. Run it against a test deployment, not production:

```
BASE_URL=https://sendly.example.com NAMESPACE=sendly scripts/chaos.sh
```

## Known limits

- During a rollout that changes static assets, a page served by a new replica may load a script from an old one (or the reverse) for a few seconds. Keep script changes backwards compatible across one release.
- Uploads in progress during the cut-over from the Redis-based version are lost (see above). Afterwards no state is lost when a replica goes away.
