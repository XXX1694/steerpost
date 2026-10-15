# Decisions

A short log of decisions that had alternatives. Newest last. Each entry gives the context, the decision and the
consequences. The entries are amended rather than deleted: when a decision is superseded, a new entry says so.

## D-001: Production runs on a shared VPS, behind the host's existing reverse proxy (2026-10-09)

**Context.** The production server is a small VPS (2 vCPU, 2 GB RAM, 40 GB disk) that already runs another service.
That service's Caddy (systemd, not Docker) owns ports 80 and 443. `deploy/` assumed SocialOS owns the whole host and
runs its own Caddy container on 80/443.

**Decision.** SocialOS lives in `/opt/socialos` and runs in Docker. Its containers publish ports on `127.0.0.1` only.
The host Caddy gets one line, `import /opt/socialos/caddy/*.caddy`, and our site blocks are in that file. `deploy/`
gains a "host proxy" mode (compose override + Caddy snippet) next to the existing standalone mode. The existing
mode stays the default for self-hosters. Containers get memory limits sized for a 2 GB host.

**Consequences.** Nothing else on the host changes. The Caddyfile is validated before every reload and backed up, and
the reload is rolled back if the other site stops answering. RAM is the tightest resource, so we measure it after the
first deploy. A larger server would cost money and needs the owner's approval.

## D-002: Secrets live only on the server, and deploys are pulled, not pushed (2026-10-09)

**Context.** The owner wants every key on the server and none in GitHub. `deploy.yml` deploys over SSH, which needs a
private key in GitHub Secrets.

**Decision.** Production secrets are only in `/opt/socialos/.env` (mode 600, root). A systemd timer on the server
polls the repository's latest GitHub Release (public API, no token). When the tag changes, it runs `deploy.sh <tag>`,
which pulls the public GHCR images, migrates, waits for `/ready` and rolls back on failure. `deploy.yml` stays for
self-hosters who prefer push deploys. It is skipped here, because the `DEPLOY_*` secrets do not exist.

**Consequences.** CI has no write access to the server, and a compromised GitHub token cannot reach production.
A deploy starts within the polling interval (5 min) after a release is published. Only tagged releases reach
production.

## D-003: No paid domain yet, so production uses sslip.io hostnames (2026-10-09)

**Context.** Budget is 0, and there is no domain or DNS access.

**Decision.** `DOMAIN=<ip-with-dashes>.sslip.io`, which gives `app.`, `api.`, `mcp.` and `s3.` hostnames that resolve to
the server with no DNS setup. Moving to a real domain later only changes `.env` (DOMAIN, COOKIE_DOMAIN, public URLs),
the release variables `API_PUBLIC_URL`/`MCP_PUBLIC_URL`, the LinkedIn redirect URL and the Telegram webhook.

**Consequences.** sslip.io is not on the Public Suffix List, so Let's Encrypt rate limits are shared with every
sslip.io user. Certificate issuance is checked after the first deploy. If Let's Encrypt refuses, the options are
Caddy's secondary ACME issuer (ZeroSSL) or a real domain. A real domain is the first thing to buy once there is a budget.

## D-004: Telegram uses the owner's existing bot token, which will not be rotated (2026-10-09)

**Decision (owner).** Production uses the existing bot. The token is supplied straight to the server's `.env`. It never
goes into the repository, GitHub or chat logs. Production receives updates by webhook (`TELEGRAM_UPDATES_MODE=webhook`).

**Consequences.** Before the webhook is registered, `getWebhookInfo` is checked so that we do not take over updates
from another consumer of the same bot.

## D-005: Free tiers only, with graceful fallbacks for services that need the owner's accounts (2026-10-09)

**Decision.** Transactional mail goes through a provider-agnostic SMTP adapter. In dev, mail is written to the log;
Resend is the intended production provider. Errors go to a Sentry-compatible SDK that is a no-op without a DSN.
Uptime is checked by a scheduled GitHub Actions workflow. There is no product analytics at launch. Off-site backups
are designed for any S3-compatible bucket.

**Consequences.** Every feature can ship and be tested without the accounts. Each one becomes live in production as
soon as its key is in `.env`. Until then, the UI and the docs state honestly what is off.

## D-006: Transactional mail through a queued, provider-agnostic port (2026-10-09)

**Context.** Email verification, password reset and account notices need mail. Sending inline in the request is slow, makes
register fail when the relay is down, and lets response timing reveal whether an address exists.

**Decision.** Services depend on `port.MailQueue`, which enqueues a `mail:send` Asynq task (MaxRetry 5, Retention 0,
so one-time links do not linger in Redis). The worker runs `port.Mailer`: an SMTP adapter (STARTTLS required on 587,
or implicit TLS on 465; it never authenticates or sends in clear text) or a log adapter (masked recipient, body only when
`APP_ENV=development`). Templates are embedded, English, plain, rendered to text plus HTML. `MAIL_PROVIDER` defaults to `log`.

**Consequences.** Any relay works; Resend is the intended one. Resend needs a verified sending domain, so production
mail stays off while the host uses sslip.io (D-003). `smtp` with a missing field fails at startup; `log` in production
only logs a warning. A rendered body (with its link) sits in Redis until the worker sends it.

## D-007: Audit MCP tool calls in the backend (2026-10-09)

**Context.** Agents act on a user's accounts through MCP. The user needs to see what an agent did, with the outcome,
and the log must not be forgeable by the client or leak content.

**Decision.** The backend writes the audit row, not the MCP server. The MCP server tags every tool call with
`X-MCP-Tool: <name>`; the API-key audit middleware records a request with a valid tag as one `mcp.tool_call` row (instead
of `api_key.request`) with the status and error code the backend itself produced. Metadata is an allow-list (tool,
route pattern, status, error code, URL ids, key label and id, client IP). The client IP comes from
`X-SocialOS-Client-IP` only when `X-SocialOS-Gateway` equals `MCP_GATEWAY_SECRET` (constant-time compare); otherwise it
is the caller's own address. The tool name is descriptive, not a security input.

**Alternatives.** The MCP server POSTs its own audit events: it would assert the outcome itself and cost an extra call
per tool. Trusting the client-IP header without a secret: anyone could forge the address in the log.

**Consequences.** Calls that bypass the MCP server (plain REST with a key) stay `api_key.request`. A key holder can
mislabel the tool name of their own calls; the route and status are still the backend's. `MCP_GATEWAY_SECRET` is a new
shared secret that must be set on both services, otherwise the audit IP is the MCP server's.

## D-008: Email verification and password reset use single-use hashed tokens in the URL fragment (2026-10-09)

**Context.** Users need to prove they own an address and recover a lost password. Verification must not lock out
self-hosters who have no mail relay, and the endpoints must not reveal which addresses are registered.

**Decision.**
- One `email_tokens` table serves both purposes. A token is 32 random bytes; only its SHA-256 is stored. Redeeming it is
  one atomic `UPDATE … WHERE used_at IS NULL AND expires_at > now() RETURNING`, so a link works once even under
  concurrent clicks. Lifetimes are 48 h (verify) and 30 min (reset). Issuing a new token retires the older ones, and a
  user gets at most 10 tokens per purpose per rolling 24 h; beyond that, requests are dropped silently (same `202`).
- Links use the URL fragment, which browsers send neither to the server nor in `Referer`, so tokens stay out of access
  logs; the frontend reads the fragment and clears it with `history.replaceState`.
- `POST /auth/password/forgot` only validates the address, enqueues an `auth:forgot` task (Retention 0) and answers `202`
  with a fixed body. The lookup, the 60 s cooldown, the cap and the mail all happen in the worker, so the request does
  the same work for every address and its status, body and timing do not depend on whether the address exists.
- A reset signs out every session and marks the address verified; a change keeps only the current session. Neither
  touches API keys or MCP connections unless the caller sets `revoke_keys` (opt-in checkbox, audited as
  `keys_revoked`). The UI and the `password_changed` mail say which happened and link to the Developer page. Keys
  survive by default because agents run on them, but a reset after a suspected break-in should offer the revoke.
- The mail task carries the token row id, never the raw token. If the final retry fails, the worker retires that token,
  so a task left in the archive holds a dead link.
- Verification is enforced (`403 EMAIL_NOT_VERIFIED`) only when `MAIL_PROVIDER=smtp`; with the log provider nothing is
  gated and the UI says mail is off. The check also runs where the actor says nothing about the owner: completing an
  OAuth or chat-link connection checks the owner's row, and editing an already scheduled post needs a verified owner,
  so switching mail on later does not leave unverified users with working paths.

Rejected: signed JWT links (cannot be revoked one by one), tokens in the query string (end up in logs), sending the
reset mail inline (its timing would reveal the address), and enforcing verification unconditionally (blocks every
self-hoster without SMTP).

**Consequences.** `register` still answers `409` for an existing address, so existence can be probed there (accepted,
the same as most sign-up forms). Existing users are unverified and must verify once mail is switched on. The rendered
mail, with its link, sits in Redis until the worker sends it (D-006), and the forgot task holds the address until
it runs. The mail limiter is in memory and per instance.

## D-009: Connect with a pasted token needs the critical `social:connect` scope (2026-10-09)

**Context.** Webhook URLs, app passwords and API keys are bearer secrets. Networks that use them (Discord, Mastodon,
Bluesky, Slack, Dev.to, VK) need one generic way to connect. Per-provider endpoints mean N handlers and N forms, and
metadata cannot hold the secret because the account API returns it.

**Decision.** One port and one route. A provider declares `connect_method: token` and its form (`connect_fields`);
`POST /social/accounts/token` validates the fields, calls the adapter's `Verify` (live whoami, 10 s) and stores the
credential in the encrypted vault, reusing the OAuth upsert and audit path. The scope `social:connect` is **critical**
and never part of a default set: an agent that can connect accounts can attach a credential it chose, so it is granted
on purpose. Browser sessions always may connect. Once server-side approvals exist, a key-initiated connect should need
the owner's approval like other dangerous actions. MCP gets no connect tool (reversed by D-025).

**Alternatives.** An endpoint per provider (rejected: duplication). Secrets in `metadata` (rejected: returned by the
API). Allowing sessions only (rejected: it blocks the API-key testing flow in PLATFORMS).

**Consequences.** Static tokens are never refreshed; a revoked one marks the account `expired` and the user repeats the
POST. Adapters must keep secrets out of the profile; the use case refuses a profile that contains them.

## D-010: User-supplied hosts are fetched through an SSRF-safe client (2026-10-09)

**Context.** Mastodon, Misskey and WordPress take a host from the user, so the server would fetch a user-chosen URL.

**Decision.** Such adapters use `safehttp.NewSafeClient`: https only; the client resolves the host itself, refuses
loopback, private, link-local (including the 169.254.169.254 metadata address), CGNAT, multicast, unspecified and other
reserved addresses in IPv4, IPv6 and IPv4-mapped/NAT64/6to4 forms, dials the vetted address (no second DNS lookup, so
no rebinding) and re-checks it in the dialer `Control`; redirects only to the same host, at most three; responses over
1 MB fail instead of being truncated; no proxy environment variables; timeouts on dial, TLS, headers and the whole
request. Adapters with a fixed host set `AllowedHosts` instead. Every adapter takes an injected `http.Client` for tests.

**Consequences.** A self-hosted instance on a private network cannot be connected. Accepted: SocialOS runs on a shared
host next to other services (D-001).

## D-011: Bound password hashing in memory (2026-10-09)

**Context.** Every argon2id hash used 64 MiB and nothing limited how many ran at once. The production API container has a
160 MB cap and the auth limiter allows bursts of 10 per IP, so a few parallel logins or registrations could get the API
OOM-killed.

**Decision.**
- The `crypto` hasher runs at most `PASSWORD_HASH_CONCURRENCY` (default 2) operations at once **and** keeps their combined
  argon2 memory within `PASSWORD_HASH_MEMORY_MIB` (default 48). Memory is read from the PHC string, so an old 64 MiB hash
  is heavier than the whole budget and verifies alone; two new 19 MiB hashes run together. Worst case is therefore one
  64 MiB verification, or about 38 MiB for two new hashes, never 128 MiB.
- A caller waits for capacity until its context ends or 5 s pass, then gets `429 RATE_LIMITED` ("server is busy, retry
  shortly") with `Retry-After: 5`. No new error code. An unknown email gets the same answer as a known one while the
  hasher is saturated, so load does not reveal which addresses exist; a busy hasher is never reported as a wrong password.
- New hashes use the OWASP minimum argon2id configuration, m=19 MiB, t=2, p=1
  ([Password Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html)).
  Stored hashes keep verifying because their parameters are read from the hash.
- A successful login with a hash made under other parameters rehashes the password, best effort: a failure is logged and
  never fails the login. The write is conditional on the hash that was verified, so a password reset or change that lands
  meanwhile is not overwritten.

Rejected: raising the container limit (hides the burst), and a new 503 code (needs contract, MCP and docs changes for no
client benefit).

**Consequences.** Under a burst, excess logins wait up to 5 s and are then told to retry. Until old hashes are upgraded,
each old-hash login occupies the whole budget for its duration. The first login after deploy costs one extra hash.

## D-012: SocialOS gets at most half of the shared host, enforced by a systemd slice and a load-shedding guard (2026-10-09)

**Context.** Production shares a 2 vCPU / 2 GB VPS with another production service (D-001). Containers could swap, the
default per-container task limit (2264) times seven exceeds the host's thread limit (about 15000), and dockerd/containerd
are unbounded (containerd peaked at 963 MB during the first image pull). The other service's units, Caddy and the host
configuration are not ours to change.

**Decision.** All SocialOS containers run in `socialos.slice` (compose `cgroup_parent` in host-proxy mode; Docker already
uses the systemd cgroup driver on cgroup v2, so no daemon change or restart): `CPUQuota=100%`, `CPUWeight=50`,
`MemoryHigh=616M`, `MemoryMax=664M`, `MemorySwapMax=0`, `TasksMax=512`, a 60 MB/s read cap and no write cap. Each container
gets `memswap_limit = mem_limit` and `pids_limit`; backend and worker keep 160m with `GOMEMLIMIT=100MiB` for upload buffers.
Drop-ins give dockerd and containerd `CPUQuota=25%`, `CPUWeight=50` and a soft `MemoryHigh` (192M / 128M), with no boot
ordering. Slice plus daemons: 984M, half of the RAM. A guard timer reads host and per-service pressure (PSI) and SocialOS's
share; only when the host is under pressure and SocialOS contributes (slice anon ≥ 300 MB, ≥ 40% of a CPU or ≥ 10 MB/s IO) it
stops the worker, then the non-essential containers, and starts what it stopped after calm runs (level 2 with a doubling
backoff). It alerts but never acts on the other service. A Caddy pre-check quarantines a SocialOS snippet that would stop
Caddy from starting.

**Alternatives.** `daemon.json` `cgroup-parent`: catches `docker run` too, but needs a Docker restart. Per-container caps
that add up to the slice: leaves the backend too little room for uploads. A slice of 848M (no overcommit): about 56% of the
RAM. `MemoryMax` on containerd: could kill the shims and orphan running containers. `IOWeight` alone: no effect with the
`none` IO scheduler. A write cap: throttled writeback can stall ext4 journal commits and the other service's fsync.
`After=caddy irbisa` on Docker: nice at boot, but a cycle if either is ever ordered after Docker. Pausing the worker: it
would freeze holding database locks. A guard that sheds on any pressure: it would stop SocialOS for pressure it does not
cause. A loop-mounted filesystem as a hard disk quota: needs a data migration; deferred. A drop-in on `caddy.service`:
edits a unit that belongs to the other service.

**Consequences.** The per-container caps (784m, 848m during a deploy) overcommit the 664M slice: under a simultaneous
peak, the OOM kill hits SocialOS inside its slice, never the other service. Under real contention the slice gets a third
of the CPU. A shed stops publishing (posts go out late) and, at level 2, the UI and MCP until the host has been calm for
10 minutes or more; autoupdate pauses meanwhile. The read cap is a placeholder until the disk is measured. Standalone mode
is unchanged. Not solved here (the other service's owner): `caddy.service` has `Restart=no`, and journald has no size cap.

**Incident 2026-10-09 (06:41-07:50 UTC).** The 80m cap of MinIO was too small for its working set: it thrashed its own page
cache (`memory.current` pinned at `memory.max`, `memory.events` `max` 1.3M, `workingset_refault_file` 80M) and read 327 GB from
disk in about 3 hours (about 56 MB/s, the 60 MB/s slice read cap). Health checks timed out, host IO pressure was some 87% /
full 80% and memory stall about 35%. The guard counted SocialOS as a contributor (IO) and shed worker, mcp and frontend for
about 70 minutes, which could never help because MinIO was the cause and kept running; the API answered 503 meanwhile. Fixed
live with `MINIO_MEM_LIMIT=192m` and `MINIO_GOMEMLIMIT=144MiB`; the pressure was gone. Consequences: the defaults are now
192m / 144MiB, so the per-container caps add up to 896m (960m during a deploy) against the 664M slice, an overcommit of
about 35%: it only matters if everything peaks at once, and then the slice hard cap (OOM inside the slice) still protects
the host. The guard now detects a thrashing container (memory at 95% of its cap and `workingset_refault_file` above 20 MB/s),
names it in an alert and restarts it once per cooldown (non-essential ones are stopped) instead of shedding others, and its
pressure alert says when the top IO container is one that shedding does not stop.

## D-013: Dangerous actions by API keys need the owner's approval, enforced in the API (2026-10-09)

**Context.** Until now an agent was asked to pass `confirm: true` for publish, delete and disconnect. The flag is an
argument the agent writes itself, so it proves nothing, and it only existed in the MCP server: a plain REST call with the
key skipped it.

**Decision.** The application layer decides. When a request authenticated by an API key attempts a dangerous action
(publish now, retry now, delete a post, disconnect an account, connect with a pasted token (D-009), or schedule less than
`AGENT_MIN_SCHEDULE_LEAD` (5 m) ahead, which is publish-now in disguise) and the key's `dangerous_policy` is `approve`
(the default for every new and existing key), the API answers `428 APPROVAL_REQUIRED` with an `approval_id` and stores a
pending approval. Only a browser session can approve or deny it. The agent then repeats the identical call with the
header `X-Approval-Id`. An approval belongs to one user and key, covers one action on one target with one payload
(fingerprint: post id and `updated_at`, the requested time, a hash of the whole create body or of the connect fields),
expires after `APPROVAL_TTL` (10 m) and is spent in the same transaction as the action, so a failed action does not burn
it and two racing calls cannot both use it. The scope check still comes first. The `confirm` flag is gone from MCP.
Sessions, the scheduler and system actors never need approval; `dangerous_policy: trusted` (chosen when the owner creates
the key) skips it for that key on purpose. Anything that does not match gets a fresh `428` and no hint why (no oracle on
other tenants' ids); an approval the owner denied gets `403` so the agent stops. A repeated ask returns the open
approval; at most 10 are open per key (a noisy key cannot block the others), checked and inserted under a per-key lock
so parallel calls cannot overshoot. The gate answers in two steps: inside the use case's transaction `Require` consumes
a matching approval or returns `ApprovalNeeded`; after the transaction rolled back, `Open` stores the pending row and
answers 428. A row written inside the transaction would be erased by that rollback, and a second connection opened
meanwhile could exhaust the pool under load. An edit of a post is approved as the post it will become (not as the
field the key named), and any key edit of a post running within the lead needs approval. The owner sees the full text,
per-network texts and media. A token connect is fingerprinted with an HMAC under an HKDF subkey of the encryption key.
Decided and expired approvals are purged after 30 days. Created, approved, denied and used are audited.

**Alternatives.** A server-minted confirmation token: the agent can fetch and present it itself, so it proves no more than
`confirm: true`. A per-key allow flag only: no per-action guarantee. Enforcing in the MCP server: bypassed by curl.
Approval id in the body: DELETE has none, a header works for every route.

**Consequences.** Breaking for API-key integrations: dangerous calls now take two steps and a human, so unattended
publishing needs a `trusted` key (opt-in, shown as such) or a scheduled post at least 5 minutes ahead. The approval is
spent before the live check of a token connect, so a rejected credential needs a new approval. Whoever holds the owner's
browser session can approve. `trusted` is visible: the key list shows it as a badge, and choosing it at creation needs a separate explicit
confirmation under the "Dangerous" heading. Changing the policy of an existing key and an OAuth-grant policy come later.

## D-014: Plan limits are counted in the application layer under a per-user row lock (2026-10-09)

**Context.** A public instance needs caps (accounts, posts per month, media storage, agent request rate) so one user cannot
exhaust the shared host (D-012), and self-hosters need to switch them off. Migration `00003` already carries
`users.plan` and `posts.quota_counted_at`.

**Decision.** One plan, `free`, whose limits come from env (`QUOTA_*`). They are opt-in: the default is `-1` (unlimited), so an automatic update never caps an existing single-owner install; a public instance sets positive numbers in its `.env`. Count limits are checked in
the application services (`accounts.connectAccount`, `posts.scheduleLocked` and `startPublishing`, `media.Upload`) by
`quota.Service`, which first takes `SELECT 1 FROM users WHERE id=$1 FOR NO KEY UPDATE` in the transaction of the change,
then counts, then lets the caller write. `FOR NO KEY UPDATE` conflicts only with itself, so inserts of child rows (which
take key-share locks on the user) are not held up, while two requests of one user queue and cannot both pass the check. A
post counts when it is scheduled or published (`quota_counted_at`), and again when that happens in a later UTC month (a
post cannot bank an allowance by being scheduled at month end); within a month unschedule/schedule and retries are free
and deleting does not refund. Quota is checked before the owner's approval is asked for or spent, and a cheap
no-lock pre-check runs before an OAuth start and before a token connect's approval and live check. A chat link that hits
the limit is dropped (logged, the code stays unused) rather than retried, because the Telegram poller is shared. The
request rate is a token bucket in memory per API instance, keyed by user (not by key) and applied to API-key actors only;
the rate cap stays `RATE_LIMITED` (429). Usage is shown by `GET /account/usage`, the MCP tool `get_usage` and a card in
Settings. Posts are counted per UTC month. A refusal is the typed error
`QUOTA_EXCEEDED` (403), mapped once in `httpx`. The request rate is a token bucket in memory per API instance, keyed by user (not by key) and applied to API-key
actors only; the rate cap stays `RATE_LIMITED` (429). Usage is shown by `GET /account/usage`, the MCP tool `get_usage` and
a card in Settings.

**Alternatives.** A `plans` table: no second plan exists yet, and env is enough for self-hosters (the column stays for it).
A database trigger or constraint: hides the rule from the code and the tests and cannot say what to do about it.
Check-then-insert without a lock: two parallel requests overshoot (the e2e test fails without the lock). An advisory
lock: not tied to the row and invisible in `pg_locks` joins with users. A counter column: drifts when rows are deleted by
paths that forget it. Counting scheduled posts instead of first-scheduled ones: lets a user cycle posts forever.

**Consequences.** Every counted change takes one row lock per user for the length of its transaction (short, and only
for users who have limits switched on). The agent cap is per instance, so with N API replicas the effective cap is up to N times the setting. Media: concurrent uploads cannot overshoot, but the size of a streamed upload is only known once it has been
read, so the check runs with the insert and a refused upload has already been stored and then has its object deleted. Existing users are counted from the first day of use; nothing is retro-fitted, so a
user already above a limit keeps what they have and cannot add more. Raising a limit needs only an env change.
## D-015: Stream media uploads to S3 in bounded parts, cap them, and send big uploads around the Next.js proxy (2026-10-09)

**Context.** A load check with the production caps (backend 160m, MinIO 80m) showed that three parallel 100 MB uploads
OOM-killed the API (peak 163m of 160m) and MinIO (killed 8 times). The handler parsed the multipart body into temp files
in the container's writable layer (page cache counts against the cgroup), and `PutObject` with a known size made minio-go
upload 16 MiB parts on 4 threads, so each upload put about 64 MiB in flight at the API and at MinIO. Separately, the Next.js
rewrite proxy that carries `/api/v1/*` for the web UI buffers request bodies and cuts them at 10 MB, so a video could never
arrive.

**Decision.**
- The handler reads the multipart body with `MultipartReader` and hands the `file` part to the media service as a stream:
  no `ParseMultipartForm`, no temp file. The service reads the first 3 KiB, sniffs the MIME type and rejects a type that is
  not on the allow-list before anything is stored. A guard around the stream counts and hashes the bytes and fails as soon
  as the kind's limit (10 MB image, 100 MB video) is exceeded, so the size is enforced while streaming, not after.
  Videos go to `PutObject` with unknown size (`-1`), `PartSize` 5 MiB and `NumThreads` 1: the API holds one 5 MiB buffer per
  upload however large the file is. Images (at most 10 MB) are read into one buffer allocated at the limit (no regrowing)
  because their dimensions are needed, so an image upload holds about 10 MB plus the client's 5 MiB part buffer.
- `PutObject` runs on a context detached from the request (30 min bound), because minio-go aborts a failed multipart
  upload with the context it got and a cancelled one would leave the parts behind; a client disconnect still ends the
  upload because reading the body fails. A bucket lifecycle rule (abort incomplete multipart uploads after 1 day, prefix
  `users/`) is added best effort; MinIO refuses that rule type but cleans stale uploads itself.
- On any failure (limit, wrong type, client gone, store error) minio-go aborts the multipart upload, and the service also
  deletes the key, so no object and no media row remain.
- At most `MEDIA_UPLOAD_CONCURRENCY` (default 2) uploads run at once, the same pattern as D-011: a caller waits up to 5 s for
  a slot, then gets `429 RATE_LIMITED` with `Retry-After: 5`. The slot is taken before the body is read. A user holds at
  most one slot: a second concurrent upload by the same user is refused at once, so one account cannot starve the rest.
  Speed floor: the connection read deadline
  moves forward on every read (30 s idle), but once an upload is 5 s old it is cut as soon as its average speed since the
  start is below `UPLOAD_MIN_KBPS` (default 32 KiB/s), and the total time is bounded by the size limit divided by that
  floor (about 54 min for 101 MiB). One rule, so a 1 byte/25 s client is cut within the idle window and frees its slot. A
  client can still bank speed with a fast burst and then slow down, but only until the average falls to the floor, and the
  bytes it sent are bounded by the 100 MB limit.
- MinIO is unchanged: with 5 MiB parts and at most two in flight its working set stayed at 44 of 80 MiB, and
  `MINIO_API_REQUESTS_MAX` made no difference in the same test, so no new setting was added. The caps (784m, within the
  664 MB slice as before) are unchanged.
- The web UI uploads straight to the API host (`NEXT_PUBLIC_API_URL`, baked at build time like the MCP URL; `credentials:
  'include'`) instead of through the proxy. This needs nothing new: `CORS_ALLOWED_ORIGINS` already lists the app origin with
  credentials and allows `X-CSRF-Token`, and `COOKIE_DOMAIN` already shares the session and CSRF cookies with `api.<domain>`
  (SameSite=Lax cookies are sent on a same-site POST). Without `NEXT_PUBLIC_API_URL` (`next dev`) uploads use the proxy.

**Alternatives.** Raising the proxy body limit (`experimental.proxyClientMaxBodySize`): the 160m frontend would buffer 100 MB.
Presigned PUT URLs straight to S3: the browser needs the S3 host, a second CORS policy on MinIO, and the type and size
checks would move to after the upload (a user could store anything up to the bucket limit); rejected for now. Bigger caps:
the slice is full. A temp file on tmpfs: counts against the same cgroup.

**Load check (local, production caps and slice file, 20 parallel logins + 3 parallel 100 MiB uploads).** Peak of
`memory.peak` / working set (anon): backend 101 MiB of 160 (93 anon), MinIO 80 of 80 (44 anon; the rest is reclaimable page
cache that the kernel drops under pressure), Postgres 39 of 128, worker 31 of 160, Redis 9 of 32. OOM kills: 0 in every
container. All three uploads answered 201 (the third waited for a slot, 4.8 s in the slowest run).

**Consequences.** A third simultaneous upload waits up to 5 s and is then refused; a browser that is still sending its body
may report a network error instead of the 429, because the connection is closed after an early answer. The web UI now needs
`API_PUBLIC_URL` set when the frontend image is built (the release workflow already does this). Image uploads still take about
10 MB of heap each (plus the 5 MiB part buffer), at most two at once.

## D-016: Registration requires accepting the current Terms; existing users are not blocked (2026-10-09)

**Context.** SocialOS can be run by anyone, so each operator needs Terms and a Privacy Policy that users see and accept, and
a LinkedIn app needs a public Privacy Policy URL. Migration 00003 already has `users.terms_accepted_at` and `terms_version`.

**Decision.** `POST /auth/register` takes `accept_terms: true`; anything else is `400 VALIDATION_ERROR` with
`fields.accept_terms`. The server stores its own `terms.CurrentVersion` and the clock time, not a version sent by the
client, so a client cannot claim to have accepted a text that does not exist. The texts are TSX in the frontend
(`/terms`, `/privacy`, static, no auth, no new dependency) with the version in `frontend/src/lib/legal.ts`; a Go test fails
when the two versions differ. The operator's name and contact are read from `OPERATOR_NAME` and `OPERATOR_CONTACT` of the
frontend container per request, because the published image is shared by all operators and `NEXT_PUBLIC_*` values are baked
in at build time. Unset values render a placeholder that says the operator must set them.

**Alternatives.** Block existing users until they accept a new version (a gate after login, a 403 on every route): the safer
legal posture, but it locks out accounts and API keys that run unattended, and needs UI and API states that do not exist
yet. Markdown files plus the `marked` dependency, shared with the static site: one source, but a new dependency and a build
step for two short pages. Operator values in the backend config: an extra API call on a page that should be static.

**Consequences.** Breaking for API clients that register users (the MCP server never does). Existing accounts have an empty
`terms_version` and carry on; when the texts change in meaning, bumping the version only affects new accounts. Re-acceptance
for existing users is a later decision. The texts are a template: the operator must review them.

## D-017: The landing page moves with CSS and a few small scripts, no animation library (2026-10-09)

**Decision.** The landing page has its own layout (`site/src/layout-landing.html`); docs keep the calm one. Motion is CSS
(keyframes, CSS scroll-driven animations where supported, cross-document View Transitions) plus about 10 KB of first-party
JavaScript (`landing.js`, `hero-flow.js`): IntersectionObserver reveals, counters, the sticky "how it works" picture and a canvas
background. Only `transform` and `opacity` animate. Everything honours `prefers-reduced-motion`: no video, no loops, a static
frame of the background, nothing hidden until revealed. The hero video is real footage of the demo, recorded by
`site/scripts/record-hero.mjs`, and phones get its poster. Network marks are Simple Icons (CC0), inlined; counters show only
numbers the build derives from the README.

**Alternatives.** GSAP or Motion: 25 to 60 KB for effects CSS already does, and a dependency to vendor and audit. Lottie or a
generated video: not real UI, and the page would claim things the product does not do. A WebGL background: weight and battery
for decoration.

**Amendment (landing v2).** Scroll-driven animations (`animation-timeline`) now also drive the hero exit, the clip-path screen reveals and the "how it works" route, with the active-step state (IntersectionObserver) as the fallback. First-party JS grew to `landing.js`, `hero-flow.js` and `motion.js` (pointer effects, mouse only); still no library. The route line animates `stroke-dashoffset` and the reveals animate `clip-path`; both are paint-only on small areas. Pause motion (`html.motion-off`) disables every one of them.

**Consequences.** Safari and Firefox without scroll-driven animations show the parallax and hero exit still, which is fine.
The hero video must be re-recorded (`npm run record`) when the compose or approvals screens change.

Addendum (review): a visible "Pause motion" switch (remembered in `localStorage`) stops the video, canvas, glow blobs, marquee
and story sweep (WCAG 2.2.2); it is not offered under reduced motion, where nothing moves. Each script block is guarded and the
`js` class that hides unrevealed sections is set only after the reveal setup works. The retry counter is read from
`MaxRetry` in `backend/internal/application/scheduler/backoff.go` at build time. The docs' Mermaid renderer is now vendored
from the `mermaid` npm package (MIT, 3.5 MB, loaded only on pages with diagrams) instead of jsDelivr, so the site makes no
third-party request.

## D-018: Account data export is a ZIP streamed by the worker into the user's S3 prefix (2026-10-09)

**Context.** The Privacy Policy promised export "later". An export has to include media files, so it cannot be one JSON
response, and the production caps (backend 160m, worker 160m, D-015) rule out building an archive in memory or on the
container's layer.

**Decision.**
- A browser session (never an API key) asks `POST /account/exports`. The API inserts a `data_exports` row (`pending`) and
  enqueues `account:export {export_id}` with `MaxRetry(0)` and `Retention(0)` (no task id: an archived task would keep its id and block re-queueing; a duplicate task is harmless because the build claims the row). A
  failed build is recorded on the row (`failed` plus a short `error_code`) and the user asks again; retrying blindly would
  redo hours of work. Migration 00003's partial unique index allows one `pending|running` export per user (`409`).
  A successful export blocks the next request for 24 hours (`429` with `Retry-After`); when a new one is made the old
  ZIP is expired and deleted, so each user holds at most one archive.
- Exports run on their own queue (`<queue>-exports`) served by a second Asynq server with concurrency 1: one build at a
  time per worker, and a long build never takes a slot from publishing. The worker claims the row atomically (`pending` to
  `running`; a second delivery finds nothing to claim). The ZIP is written into an `io.Pipe` that `Storage.Put` reads with unknown size, which
  D-015 already made a bounded 5 MiB part upload: no temp file, no whole-archive buffer. JSON is deflated and read from
  Postgres in keyset batches of 200 rows (`id > after`, `(user_id, id)` indexes in migration 00005); media files are
  copied from S3 through a 32 KiB buffer and stored uncompressed. Worker memory is the batch plus one part, independent
  of the account size.
- Each dataset is one SQL statement with an explicit column list that renders JSON in the database, so no password hash,
  token hash, key hash, encrypted credential, session or CSRF value is selected at all, and a column added later is not
  exported until someone adds it. Contents: `profile`, `social_accounts` (no credentials), `posts` (with targets and
  attempts), `media` and `media/<id><ext>`, `api_keys` and `mcp_connections` (prefix only), `approvals`, `audit_logs`,
  `README.txt`. An unreadable media object is listed in `media/MISSING.txt`; any other failure fails the export and the
  object is deleted.
- The archive lives at `users/<uid>/exports/<id>.zip`. `GET /account/exports/{id}` (session, tenant-scoped, audited)
  returns a presigned URL valid for 5 minutes, only while the export is `ready` and before `expires_at`
  (`EXPORT_RETENTION_DAYS`, default 7, at most 30). An hourly worker sweep deletes expired archives, fails a `running`
  export older than 2 hours (a crashed worker) and re-enqueues a `pending` one older than 10 minutes (a lost task) and fails one older than 2 hours.
- Audit: `account.export_requested`, `account.export_ready`, `account.export_failed`, `account.export_downloaded`.

**Alternatives.** A synchronous JSON response: no media, and a request that holds memory. Proxying the download through
the API: the API's 160m would carry multi-hundred-megabyte transfers; a presigned URL sends the bytes from S3 directly (the
same as media URLs). A temp file then `Put` with the known size: needs disk the containers do not have. An email with the
link: mail may be off (`MAIL_PROVIDER=log`) and the link would sit in a mailbox for days; the Settings page polls instead.

**Consequences.** A presigned URL is a bearer link for five minutes; anyone who gets it can download the archive in that
window, which is why it is short and why `GET` is audited. The 5 MiB part size caps an archive at about 50 GB on S3, far
above the per-user media quota. The partial unique index means a crash that leaves a row `running` blocks the user until
the sweep fails it (at most 2 hours plus the sweep interval).

Accepted for now: with concurrency 1, an export queued behind others for more than 2 hours is failed by the sweep while
still `pending` (`export.go`, `runningLimit`), and the user asks again. A build also needs its `MarkReady` to find the row
`running`; if the sweep failed it first, the archive is deleted and the failure stands. On shutdown the export server waits
only `ExportShutdownTimeout` (5 s by default) in parallel with the publish server, so a build in flight is marked
`interrupted`; the upload itself is bounded by the build budget (90 minutes), not the 30 minutes of an ordinary upload.

## D-019: Account deletion has a grace period, an explicit cancel, and a batched purge by the worker (2026-10-09)

**Context.** The Privacy Policy said deletion would exist "later". Deleting an account is irreversible and removes rows in
a dozen tables plus S3 objects; a stolen session must not be able to destroy data at once, the worker has 160m, and a
single `DELETE FROM users` would run one huge transaction and could trip `post_media.media_id ... ON DELETE RESTRICT`.

**Decision.**
- `POST /account/delete {password, confirm}` needs a browser session (an API key gets `403`), the **current password**
  (wrong: `400` with `fields.password`; the hasher's `429` passes through) and the account's **email typed** as
  confirmation (`fields.confirm`). It is rate-limited like password change (`reauth:`).
- Success schedules the deletion `ACCOUNT_DELETION_GRACE_DAYS` ahead (default 7, 1 to 30; `users.deletion_scheduled_at`,
  migration 00006) and answers `202 {status:"scheduled", scheduled_for}`. In one transaction every session is deleted, every
  API key and MCP connection is revoked, an `account_deletions` record is written and `account.deletion_scheduled` is
  audited; after the commit, every scheduled post goes back to a draft (queue tasks removed), and the publisher skips
  whatever is still due (a scheduled post becomes a draft the same way; a publishing one fails its target with
  `ACCOUNT_DELETION_SCHEDULED` and can be retried after a cancel), so nothing is published during the grace period. The owner gets a mail (`account_deletion_scheduled`) that says how to cancel, which is also the alarm when
  someone else did it. The cookies are cleared.
- The account stays `active` during the grace period. Signing in works, `/me` carries `user.deletion_scheduled_at`, the web
  app shows a banner with **Cancel deletion** (`POST /account/delete/cancel`, session, `409` when nothing is scheduled), and
  export still works. Cancelling clears the schedule and the record; sessions and keys stay revoked and posts stay drafts.
  Signing in alone does **not** cancel: an accidental sign-in must not undo a decision, and the banner makes the state visible.
- The hourly worker sweep queues `account:purge {user_id}` (on the maintenance queue `<queue>-exports`, one at a time, Asynq `Unique(55m)`, `MaxRetry(2)`; the hourly sweep is the outer retry loop) for accounts whose date
  has passed, and for accounts a previous purge left half done. The purge: (1) **claims** the account atomically
  (`status='deleted'`, `deleted_at`; from then on login, sessions and keys fail on their own); a cancelled, unknown or
  not-yet-due account is a no-op; (2) fails with a retryable error while a post is `publishing` or an export is being built;
  (3) stores row counts once; (4) deletes in batches of 200, in this order: `posts` (cascades to targets, media links, jobs,
  attempts), `audit_logs`, `analytics`, `action_approvals`; (5) per batch of media, the S3 objects first, then the rows;
  (6) export archives; (7) one transaction deletes the user row (cascades to sessions, accounts and their encrypted
  credentials, keys, MCP connections, link codes, tokens, export rows) and stamps `account_deletions.purged_at`;
  (8) mails `account_deleted`. Every step deletes only what is still there, so a retry or a concurrent run is safe.
- Posts go before media (`RESTRICT`) and before social accounts (`post_targets` has no cascade). Migration 00007 adds, concurrently, the
  missing indexes on `scheduled_jobs(post_target_id)` and `analytics(post_target_id)`, which the cascades would otherwise
  scan per deleted target.
- What stays: `account_deletions(user_id, requested_at, purged_at, counts)`: numbers and ids, no email, no content, no foreign
  key. The audit log is deleted with the account (it is the user's data); the durable evidence is this record.
- Credentials of connected networks are **not** destroyed at request time: that would make cancelling a lie. They go with
  the purge.

**Alternatives.** Immediate irreversible deletion (the first plan): one stolen session, or one mis-click, loses everything.
A cancel that happens on sign-in: simpler, but silent. Soft delete forever: not deletion. One `DELETE FROM users` cascade:
a long transaction in the 160m worker, and the `RESTRICT` ordering risk. Anonymising the audit log instead of deleting it:
it is still personal data about the user's actions and an agent's, and an anonymised log helps nobody.

**Consequences.** Data lives up to the grace period plus an hour after a request, and backups keep it as long as the
operator keeps them (the Privacy Policy says so). The email address is unavailable for registration until the purge ends.
Counts are those of the first purge attempt. A post stuck `publishing` delays a purge until the reconciler settles it.

Addendum (review): the legal version is bumped to 2026-10-10 because the Privacy Policy now describes export and
deletion, which changes its meaning. As D-016 says, a bump only affects new accounts; existing users are not asked to
accept again. While a deletion is scheduled the account refuses scheduling and publishing (`409`, `RequireNotDeleting`,
set at authentication), the publisher skips due jobs of such accounts (the post goes back to a draft, audited as
`post.unscheduled` with reason `account_deletion`), access is revoked in the same transaction that sets the schedule
(posts are unscheduled right after, and again by the purge after it claims the account), and the purge removes every
object under `users/<uid>/` (S3 listing) after the keys it knows, so failed-export leftovers go too.

## D-020: The product is renamed Steerpost; stored and host identifiers keep the `socialos` name (2026-10-09)

**Decision.** The product, the MCP server, the generated client configs, the images, the npm packages and the Go module are
now called Steerpost / `steerpost` ("the human steers, the agent posts"). The GitHub repository moves from SMSI to steerpost. Names that name
stored state or a host resource stay `socialos`: session/CSRF cookies, `X-SocialOS-*` headers, Redis keys, the Bluesky rkey
salt and the Mastodon idempotency prefix, the S3 rule and default bucket, metrics, localStorage keys, the compose project, DB names,
`/opt/socialos`, systemd units, `socialos.slice`, Caddy snippets and backup paths. The MCP server reads `STEERPOST_*` and falls back to
`SOCIALOS_*`. Images are published under both names until every server pulls `steerpost-*`. `autoupdate.sh` follows redirects.

**Alternatives.** Rename everything at once: a prod migration on a shared host, a forced logout, duplicate posts on retry,
new empty volumes. Rename the copy only: users would still see `socialos` in images, configs and the MCP server name.

**Consequences.** Two names coexist, and `deploy/README.md` (section 15.2) lists the legacy identifiers. The old Pages URL `/SMSI/` returns 404 (no
redirect). Never create a repo named SMSI again. Dual-publish and the env fallback are removed once every known server pulls `steerpost-*`.
The Bluesky salt and the Mastodon prefix are never changed. The new `steerpost-*` GHCR packages start private; the owner makes them public
(deploy/README.md, section 15.1). The shims (env fallback, dual publish, `curl -L` in `autoupdate.sh`) land and are deployed before the repository is renamed.

## D-021: The UI is localized with next-intl on the client; locales ship when complete, beta until a native review (2026-10-09)

*Amended by D-024: the "CJK and Arabic use system fonts" rule no longer holds, in the app or on the landing.*

**Decision.** The dashboard and the demo use a client-side provider (`frontend/src/i18n/`) with ICU catalogs in
`frontend/messages/{locale}.json` (typed from `en.json`; since #145 split into `messages/{locale}/{namespace}.json`) and a `useTranslations(ns)` hook with the same shape as next-intl's.
The runtime is a small in-house ICU subset (`src/i18n/icu.ts`: arguments, number/date/time, plural, selectordinal, select,
`#`, rich tags), not next-intl itself: next-intl 4.14 supports Next 15 but measured +14 kB gzipped on every route (see the
CHANGELOG), this is about 2 kB. A test asserts that it prints the same text as FormatJS `intl-messageformat`, and
`npm run i18n:check` validates every catalog with the official FormatJS parser. Switching to next-intl later means changing
the import of `useTranslations`. The locale is resolved in the browser, the same way in the
standalone and the static-export build: `users.locale` → `localStorage steerpost_locale` → `navigator.languages` → `en`.
There are no `/[locale]/` routes in the app; the landing page gets `/{locale}/` pages with hreflang. Locales: `en` (source),
then `ru`; `es`, `pt-BR`, `de`, `fr`, `id`; `ja`, `zh-CN`; `kk` (hidden until a native review); `ar` last, after logical CSS.
This supersedes the wave table in docs/copy/languages.md: `uk` waits, `zh-CN` is in. `uk` and `zh-Hant` fall back to `en`.
Dates and numbers come from `lib/time`/`lib/calendar` with an explicit locale and the user's timezone. CJK and Arabic use
system fonts (superseded by D-024: both the app and the landing now load self-hosted Noto faces for `ar`, `ja` and
`zh-CN`, only on those locales). API error codes, API messages, emails, MCP text and docs stay English; the UI maps error
codes to text.
Translations are machine-drafted with the glossary, back-translated on a sample and labelled "Beta translation" until a
native speaker signs `docs/copy/review/{locale}.md`. CI blocks missing keys in every enabled locale.

**First paint.** English is a static import of the provider module (a cached JS chunk), not a prop, so it is not
serialised into every document. Other catalogs are lazy chunks. Routes stay static in both builds: the HTML is English, the
stored (or detected) locale is applied after mount, and a head script hides the shell (`data-i18n-pending`, at most 1.5 s)
only when the stored locale is not English, so English users are unaffected and others see no flash. The head script
accepts only locales the build offers. The `steerpost_locale` cookie is still written by the switcher for future server use.
Measured trade-off (server build, 12 sidebar navigations): reading the cookie in the root layout made every route dynamic;
with a `loading.tsx` the click feedback was fast (p50 144 to 14 ms at +100 ms latency) but content-ready p95 went from
49 ms to 330 ms (React holds the reveal about 300 ms after a fallback), and without it each navigation waited for a server
round trip. Static routes avoid both. Runtime formatting supports named number/date/time styles only (FormatJS presets);
`i18n:check` rejects skeletons and custom patterns, and a test compares every catalog message with FormatJS.

**Alternatives.** `[locale]` prefix routes with `generateStaticParams`: the app renders in the browser behind login, so no
SEO gain, 11× the exported pages and every link rewritten. Server negotiation by cookie: impossible in the static export
and makes every prod route dynamic. next-intl and react-intl: the same FormatJS engine, about 14 kB gzipped on every route. i18next, Lingui, Paraglide: a second
message syntax or a brittle SWC plugin. Vendored CJK/Arabic fonts: megabytes for glyphs every OS ships (revisited in
D-024, which loads only the slices a page uses).

**Consequences.** Server metadata titles stay English. Server field-level messages are replaced by a generic localized
hint outside `en` until field codes exist. A release is blocked by missing keys or failed checks, not by draft status.
Localized emails need a later decision built on `users.locale`.
The English catalog (about 10.5 kB gzipped) was first one static import, which added 13 to 16 kB to first-load JS on every app and auth route (for example `/login` 131 to 147 kB). Since #145 each route imports only the English namespaces it uses: the root layout `common` and `errors`, `(auth)` adds `auth` and `legal`, `(app)` adds `nav`, `shell`, `language` and `legal`, and each page adds its own, through `MessagesScope`s in `frontend/src/i18n/scopes/`. Other locales still arrive as one lazy chunk per locale, from which a scope takes its namespaces. A page that uses a namespace its scopes lack shows the key in development (`MISSING_SCOPE` in the console) and fails `tests/i18n-route-scopes.test.tsx`. Measured as the gzip of the page and its layout chunks, `/login` went from 169.7 to 162.0 kB (the English text it loads is now about 3.5 kB: `common`, `errors`, `auth`, `legal`). The same build of the commit before #142 measures 148.9 kB, so the issue's goal of staying within about 3 kB of the pre-#142 numbers is not met; the remaining difference (the ICU runtime, `errors` and the shared helpers, and what the app gained since) is not attributed. Next's own table leaves the layout chunk out, so it shows no change for `/login`.

## D-022: Steerpost is licensed under the AGPL-3.0 (2026-10-09)

**Context.** The repository was public but had no licence, so no one could legally use, modify or self-host the code, and
copy that called the product "open-source" was not true. The owner chose a licence.

**Decision.** AGPL-3.0-only, in `LICENSE` (the unmodified text from gnu.org), with `"license": "AGPL-3.0-only"` in every
`package.json`. Anyone may use, modify and self-host Steerpost; whoever runs a modified version as a network service must
offer its source to that service's users. This matches comparable self-hosted social schedulers (Postiz, TryPost).

**Alternatives.** MIT or Apache-2.0 (more permissive: a hosted fork could stay closed, which works against a small
open-source project). No licence (source-available only; rejected because the product is meant to be self-hosted by
others).

**Consequences.** Product copy may say "open-source (AGPL-3.0)". Contributions are accepted under the same licence. The
owner, as the sole author so far, could still dual-license later; once outside contributions land, that would need their
agreement or a CLA.

## D-023: Social sign-in runs in the Go backend: OAuth 2.0 / OIDC with Authorization Code + PKCE, Google and GitHub first, each optional per deployment (2026-10-09)

**Decision.** Users can sign in with Google or GitHub through the Steerpost API itself: Authorization Code with PKCE (S256),
a `state` cookie bound to the browser, a nonce checked in our code, and provider access tokens thrown away after login. A provider
is on only when its client id and secret are configured. The Google adapter is generic OIDC (a static provider config), so a
self-hoster can later point it at Keycloak or Authentik. Apple and Microsoft come later: Apple needs the paid developer program
(99 USD a year), which conflicts with D-005 (free tiers only).

**Alternatives.** A hosted identity service (Firebase, Auth0, Clerk): every self-hoster would need a vendor account, which breaks
D-022's promise that anyone can self-host; every user's email would go to another processor; the vendors ship third-party
JavaScript against "no third-party trackers" (AGENTS §7); and it adds a second session system next to our revocable server-side
sessions. Hand-rolled OIDC: verifying ID tokens ourselves (algorithm confusion, key rotation, clock skew) is the risky part, and
`coreos/go-oidc` already does it.

**Linking rules** (the pure function `domain/identity.Decide`):
1. A known `(provider, subject)` signs its owner in; an inactive owner is refused (`account_unavailable`).
2. A local account with the same email is linked automatically only when the provider is authoritative for that address
   (Google: a `gmail.com` address, or a verified address with an `hd` claim; GitHub: primary and verified) and the local email is
   already verified. Otherwise the flow ends with `account_exists` ("sign in with your password, then connect the provider in
   Settings"). Unverified emails are never auto-linked: that blocks pre-hijacking, where someone registers a victim's address
   first. Production does not verify emails yet (`MAIL_PROVIDER=log`), so this refusal will be common for existing users.
3. No match starts a pending sign-up, completed after the user accepts the Terms (D-016); no user row exists before that. It
   starts only when the provider is authoritative for the address; anything weaker (no email, unverified, or verified on a
   custom domain without `hd`) ends with `email_unverified`, because an account created with an address that can change hands
   would let its next owner pre-empt the real person. The new account's email is stored as verified.

Any future generic OIDC issuer (Keycloak, Authentik) gets its own provider id and is never authoritative: the gmail.com and
`hd` rules apply only to tokens issued by `https://accounts.google.com`.

Users may have no password (`users.password_hash` is nullable): login spends the cost of a real check for them, so timing does
not reveal them, and they set a first password with `POST /auth/password/set` (a session younger than 10 minutes, else `REAUTH_REQUIRED`). Connecting and disconnecting providers is session + CSRF and re-authenticates (the current password, or a session under 10 minutes old without one); the last way to sign in cannot be removed. Provider email changes are not synced to `users.email`. A user without a password deletes the account (D-019) with a session created in the last 10 minutes instead of a password, and an older session gets `403 REAUTH_REQUIRED` (sign in again, then retry), so deletion never needs a password that does not exist.

**Consequences.** Google needs a domain the owner can verify (sslip.io hosts cannot be), so GitHub ships first. We now own
account-linking security. Flow state lives in `auth_oauth_flows` (migration 00008) as hashes; user links in `user_identities`.
The Down migration marks password-less users with `!`, which never verifies, so they sign in again only through a password reset; the next Up turns the marker back into NULL. Migration versions must be contiguous (goose rejects a number below an applied one), so migration PRs merge in number order.

## D-024: Glass surfaces over a teal mesh, a lagoon secondary colour, Onest with per-script Noto, and rare hero transitions (2026-10-09)

D-023 is reserved for social sign-in.

**Context.** The owner asked for a glassmorphism style, deliberately chosen brand colours applied by a 60-30-10 rule,
status chips that do not read as generic AI UI, a typeface with more character than Inter that covers all eleven locales
(D-021, including Kazakh Cyrillic, Arabic, Japanese and Chinese), and expressive transitions for the moments that matter.
BRAND.md had ruled out decorative gradients beyond the hero glow, set Inter as the only face, and the landing used system
fonts for Japanese, Chinese and Arabic. The design proposal (palette with computed contrast, glass tokens, a motion audit
and a preview) was approved.

**Decision.**
- **Colour.** Harbour teal stays the only accent. A quieter teal, **lagoon** (`secondary`), takes the structural 20 to 30 %:
  the chrome tint, the active nav row, selected rows, secondary buttons, chart series 2 and 3. `info` (blue) and `canvas`
  are added. The app spends colour 70 / 20 / 10 by area (a dense dashboard needs neutrals to dominate); the landing uses the
  classic 60 / 30 / 10.
- **Glass.** Real backdrop blur only on chrome (sidebar, header, landing nav) and floating layers (popovers, menus,
  toasts); cards are translucent without blur over one fixed mesh layer; dialogs are a solid panel over a plain dim scrim; tables, inputs, the composer and long text stay
  solid. At most two blurred layers on screen. Fallbacks for missing `backdrop-filter`, `prefers-reduced-transparency`
  and `forced-colors`. Text contrast is checked against the glass composited over the mesh's strongest point.
- **Status tags.** Square-ish 5 px tags with a hairline border, the label in the text colour and a status glyph that alone
  carries the colour (each status a different shape), replacing the rounded pastel pills.
- **Type.** Onest Variable for Latin and Cyrillic; Noto Sans Arabic, Noto Sans JP and Noto Sans SC (variable) only for their
  own locale, scoped with `html:lang()`, in the app (a lazy CSS chunk on locale switch) and on the landing (a stylesheet linked
  only from that locale's page). Everything is self-hosted from `@fontsource-variable` packages, with their OFL licences
  shipped next to the files. This supersedes D-021's "CJK and Arabic use system fonts" for both the app and the landing:
  the system faces differed by platform and Windows' Arabic and Chinese fallbacks did not match the product. The script
  faces use `font-display: optional`, so a late font never shifts the layout; the system face stays in that case.
- **Motion.** Frequent navigation becomes instant or a 150 ms fade; lists stop staggering except the dashboard's first load;
  the app gets the Pause motion setting the landing has. Three rare moments get a hero transition of at most 520 ms through
  the View Transitions API (accent circle fill on sign-in and onboarding complete, accent wipe on publish now, a
  shared-element move from the composer to the calendar), with a plain fallback and a cross-fade under reduced motion.

**Alternatives.** Keep Inter (cheapest, but generic, and the owner asked for character). Geologica (distinctive but wide,
costly in dense tables and 35 % longer translations), Golos Text (close to Inter, no weights under 400), Rubik (rounded,
reads playful), IBM Plex Sans (good and has script siblings, but 126 KB for Latin and Cyrillic and closely tied to IBM's
Carbon look), Manrope and Unbounded (both miss Kazakh letters). For Japanese and Chinese, keeping system fonts (no download)
was weighed against a consistent face; the slices loaded per page keep the cost to the characters actually shown. For
glass, blurring every card was rejected: it repaints on scroll and costs low-end phones frames for no visible gain over a
soft mesh. A single 60-30-10 rule for both surfaces was rejected because a dashboard at 30 % secondary looks tinted.

**Consequences.** BRAND.md sections 4 to 7 describe the system; tokens live in `frontend/src/styles/tokens.css`, and the
landing derives them as before. The change lands in steps: tokens and fonts first, then the glass utilities, then the app
shell and status tags, then cards, motion and the landing. Product screenshots and the hero video are re-recorded after the
app screens change. Japanese and Chinese pages now download a few hundred kilobytes of font slices on first visit (measured
in the font PR); English and Cyrillic pages get lighter. The wordmark keeps its Inter outlines until it is redrawn
separately.

## D-025: AI agents can sign up over MCP for a key their owner confirms by mail, with connect trust only (2026-10-15)

**Context.** Third-party agents (Claude Desktop and the like) should be able to onboard a person without that person first
clicking through the web app: create the account, connect a token network (a Discord webhook) and schedule posts. D-009 said
"MCP gets no connect tool", and every key so far was minted by a signed-in owner. An unauthenticated endpoint that mints keys
and creates accounts invites account pre-hijacking, address enumeration and mail bombing.

**Decision.** `POST /auth/agent-signups` (public, MCP tool `sign_up`) takes an email, an agent name and the owner's Terms
acceptance, and answers `201` with a raw key that does not work yet. The request is a row in `agent_signups` only: no user
and no API key exist until the owner presses Confirm in the mailed link (`/agent-access#token=…`, 24 h). The HTTP side never
looks the address up and answers the same shape for every address; the worker decides the mail variant (new account or
existing account) and mails nobody for disabled or deleting accounts or past 5 mails per address per day. Confirming deletes
the request and, in the same transaction, stores the key under the hash the agent already holds plus an MCP connection, so
the owner sees and revokes it under Developer.
- A new address gets an account without a password, verified by the link, with the Terms stored at confirmation (D-016) and
  a session, so the owner can set a password at once (D-023's fresh-session rule).
- An existing account is never signed in by the link: confirming needs that account's own session (`401` signed out, `403`
  as someone else). A link in a forwarded or leaked mail therefore cannot take over an account.
- The key gets the default scopes plus `social:connect` and the new `dangerous_policy` `trusted_connect`: connecting a
  token network skips the approval (that is the point of unattended onboarding), every other dangerous action still needs it
  (D-013), and the scopes exclude publish-now, delete and disconnect. An agent schedules at least `AGENT_MIN_SCHEDULE_LEAD`
  ahead without asking; a post then publishes at its time.
- Until confirmed the key answers every route `403 KEY_PENDING_CONFIRMATION` with the masked address, the expiry and the
  scopes, so the agent tells its owner instead of signing up again.

This reverses the last sentence of D-009: MCP gets `connect_account` and `list_platforms`.

**Alternatives.** `trusted` for agent keys: it would let an agent publish now or delete without the owner, which is exactly
what D-013 forbids. Approval for every connect: the owner would have to open the web app during onboarding anyway, which
defeats the feature. Creating the user (unverified) at sign-up: an attacker could pre-register a victim's address and wait;
the account would also exist for someone who never asked. Answering differently for known addresses ("you already have an
account"): an enumeration oracle. Signing an existing account in by the link: mail is a weaker factor than the owner's
password or provider, and the link travels through an agent's hands.

**Consequences.** One more public, mail-sending endpoint: it sits behind the auth and mail limiters per client, and the
limiter now keys anonymous requests by the address the MCP gateway vouches for (`X-SocialOS-Client-IP` with
`MCP_GATEWAY_SECRET`), otherwise every agent behind one MCP server would share one bucket. The agent name is limited to
letters, digits, spaces and `-_.()'`, with no letter or digit right after a dot, so it cannot hold a domain that a mail client
would turn into a link ("verify at steerpost-help.com"); this also refuses version numbers like "3.5". It is HTML-escaped in
the mail, and the mail carries no agent-supplied URL. The daily mail cap is counted and claimed in one transaction under a
per-address advisory lock, so a burst processed by several workers cannot pass it, and a request gets its token once: a
redelivered task does not replace the link already mailed. A link opened
by someone who is signed in to another account gets `409` and must sign out first. Confirm and decline require
`Content-Type: application/json`, so a cross-site page cannot confirm for a visitor (login CSRF). Down migration 00009 turns
`trusted_connect` keys into `approve`.
