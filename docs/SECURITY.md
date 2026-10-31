# Security

What the prototype protects, how, what it does not yet do, and what production would add.
Every control below is in the code and covered by a test (names in brackets).

## Assets and trust boundaries

- **Food-safety data**: lot genealogy, stock ledger, recalls. A wrong or blocked recall is a
  public-health risk, so integrity matters more than confidentiality here.
- **Money data**: costs, EKP, sales.
- **Supervisor PIN**: unlocks a stopped POS and opens a back-office session.
- **POS device keys**: ECDSA P-256 keys in the Android Keystore that sign offline sales.

Boundaries: Android POS ↔ Go API (HTTP, signed batches), browser dashboard ↔ Go API
(HTTP + CORS), Go API ↔ MySQL (private compose network).

## Threat model (STRIDE)

| Threat | Example | Control in place |
|---|---|---|
| **S**poofing | Someone posts sales as a POS terminal, or starts a recall as "kalite_muduru" | Sync: device key pinned per `device_id` in `pos_device_key` (first signed sync = trust on first use, or provisioned by a supervisor with `POS_ENROLLMENT=closed`); a different key → 403 `KEY_MISMATCH`. Back office: writes need a supervisor session (PIN → 30-min HMAC token); recall initiator and movement `created_by` come from the token, not the request body. [`TestSyncPinsFirstKeyAndRefusesAnotherKey`, `TestRequireOperatorProtectsWritesOnly`] |
| **T**ampering | Change qty or lot of a signed sale in transit; replay a UUID with other data | Envelope signature over the whole body is mandatory and each item is signed (`v2|tenant|device|role|lot|qty|uuid`); the server price is used, never a client price. A UUIDv7 already stored with a different payload hash → `REPLAY_MISMATCH`. Stock ledger and audit log are append-only (UPDATE/DELETE blocked by triggers). [`TestSyncRequiresEnvelopeSignature`, `TestIntegrationSyncIdempotentAndReplayMismatch`, `test_ledger_is_append_only`] |
| **R**epudiation | "I did not start that recall / unlock that terminal" | `audit_log`: every PIN attempt (`SUPERVISOR_UNLOCK[_DENIED]`, `OPERATOR_SESSION[_DENIED]`) with device, IP, lot, recall; lot status changes by trigger; recalls store the authenticated initiator. |
| **I**nformation disclosure | Stack traces / SQL errors in responses, secrets in the APK or logs | 500 responses carry no driver text (logged server-side only). PINs are never logged or stored (bcrypt cost 12 in MySQL). The release APK has no NVIDIA key and no PIN hash (forced empty in `buildTypes.release`, checked by `scripts/build-release-apk.sh`). `Cache-Control: no-store`. |
| **D**enial of service | Huge bodies, request floods, PIN brute force | 1 MiB body cap (413), ≤ 500 items per sync (413), per-IP token bucket 20 req/s, burst 120 (429 + `Retry-After`), header/read/write timeouts, 64 KiB header cap. PIN: 5 failures per device **and** 10 per client IP → 5-minute lockout, so rotating `device_id` does not help. [`TestRateLimiterPerIP`, `TestSecurityMiddleware`, `TestPINLockoutCannotBeBypassedByRotatingDeviceID`] |
| **E**levation of privilege | A browser page on another site calls the API with the user's network position; SQL injection | CORS only for the dashboard ports on loopback / private LAN hosts (or an explicit list); writes need a bearer token, which a cross-site page cannot read. Every SQL statement is parameterised (`?` placeholders; the only string concatenation adds a fixed `WHERE` clause). The API no longer runs DDL at start-up (procedures live in `schema.sql`), so it can use a DML-only DB user. [`TestAllowedOrigin`] |

## Controls by component

**Go API** (`api-go/security.go`, `controllers/session.go`, `controllers/auth_controller.go`, `controllers/sync_controller.go`)
- Write endpoints behind `RequireOperator`: `POST /recall`, `/movements/*`, `/rework`,
  `/production`, `/purchases`, `/butchery/operations`, `/ai/forecast/butchery/freeze`,
  `/sync/register-key`. Reads stay open for
  the dashboard and the POS.
- `POST /auth/session`: PIN → token `base64url(user|exp).HMAC-SHA256`, secret from
  `API_SESSION_SECRET` (≥ 32 bytes) or random per process; constant-time comparison.
- `POST /sync`: P-256 SPKI key, SHA256withECDSA (ASN.1 DER), envelope + per-item signatures,
  item `device_id` must match the envelope, UUIDv7 idempotency in `sync_inbox` (one transaction
  with the sale, so a retry never double-books stock).
- Response headers: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
  `Referrer-Policy: no-referrer`, `Content-Security-Policy: default-src 'none'`.

**MySQL**: CHECK constraints and triggers enforce the business rules even if the API is bypassed
(negative stock, movement signs, recalled lots cannot be sold/shipped/consumed, supplier and expiry
on purchased lots, append-only ledger and audit).

**Android POS**: signing key generated in the Android Keystore (TEE/StrongBox when the device has
it), never exported; outbox in SQLite; network security config allows cleartext only to
`127.0.0.1` / `localhost` / `10.0.2.2` in release. Recalled lots are blocked offline from the
cached recall list.

**React**: the PIN is sent only to `/auth/session`; the token is kept in memory (not
`localStorage`) and dropped on reload. [`web-react/src/__tests__/smoke.test.js`]

## Proof (curl, 2026-10-28, compose stack)

```
POST /recall (no token)                -> 401 AUTH_REQUIRED
POST /auth/session {"pin":"111111"}    -> 401 INVALID_PIN {"attempts_left":9}
POST /auth/session {"pin":"<demo>"}    -> 200 {token, expires_at}
POST /sync with a 1.1 MB body          -> 413 PAYLOAD_TOO_LARGE
POST /sync unsigned, unknown device    -> 403 DEVICE_NOT_ENROLLED
GET /lots  Origin: http://evil.example:5173 -> no Access-Control-Allow-Origin
POST /recall with token, "initiator":"spoofed" -> recall.initiated_by = kalite_muduru
```

## Dependency & scanning status

Checked 2026-10-28 with `gh api repos/<repo>/dependabot/alerts` and `.../code-scanning/alerts`.

| Source | Open before | Action | Open after |
|---|---|---|---|
| Dependabot, Go (`api-go/go.mod`) | 19 (9 critical, 3 high, 6 medium, 1 low) | Upgraded `golang.org/x/crypto` v0.28.0 -> v0.55.0 and `filippo.io/edwards25519` v1.1.0 -> v1.2.0. Go toolchain 1.22 -> 1.25, because x/crypto >= v0.52 requires it (Dockerfile, CI, check-all). | 0 expected after re-scan |
| Dependabot, npm (`web-react/package-lock.json`) | 14 (2 critical, 1 high, 10 medium, 1 low) | Upgraded `vite` 5.4 -> 6.4.5 and `vitest` 2.1 -> 4.1.12 (pulls `esbuild` 0.25.12 and `@vitest/mocker` 4.1.12; `tinypool` is no longer a dependency). Lockfile regenerated; `npm audit`: 0 vulnerabilities. | 0 expected after re-scan |
| Code scanning | none: no analysis is configured (API returns "no analysis found") | - | - |
| Secret scanning | disabled for this repository | - | - |

Notes:
- No alert was dismissed: every one had a patched version, so all were fixed by upgrading.
- Reachability: the API uses only `golang.org/x/crypto/bcrypt`; all 19 Go advisories are in the
  `ssh` / `ssh/agent` packages, which are not compiled into the binary. The npm advisories affect the
  dev server and test runner only (Vite dev middleware, Vitest UI/mocker, Tinypool workers); the
  production dashboard is a static `vite build` output. They were upgraded anyway so the advisory
  list stays clean.
- Android lint (`lintDebug`) runs in CI and fails the build on errors. The two existing errors were
  fixed: `AppDatabase` no longer subclasses `RoomDatabase` (it called the restricted
  `InvalidationTracker` constructor; Room's generated implementation never existed, so the direct
  SQLite engine was always the one in use), and `MainActivity` loads its start-up state with
  `LaunchedEffect` instead of `produceState`.

## Known limitations (prototype)

- **Demo PIN** `246813` is published in the README for the judges; one PIN unlocks everything
  (no per-user roles yet). The old PIN `590314` is in git history and no longer valid.
- **HTTP, not HTTPS.** Traffic is local (USB `adb reverse`, emulator, or a LAN in the debug
  build). Signatures protect sale integrity, not confidentiality.
- **Trust on first use** for POS keys: the first device to sync under a `device_id` pins it.
  Set `POS_ENROLLMENT=closed` to require supervisor provisioning (`POST /sync/register-key`).
  Each installation has its own random `device_id` (`DeviceIdentity`, kept with the app data like
  the Keystore key), so a second phone or a reinstall is pinned under a new id on its first sync;
  the old id stays in `pos_device_key` until it is removed. Until 2026-10-29 every phone used the
  fixed id `POS-TERM-NURI-001`, so a second phone was refused with `KEY_MISMATCH`.
- **Reads are unauthenticated** (lots, trace, reports) so the dashboard needs no login.
- Rate limits and PIN lockouts are in memory per API instance and reset on restart.
- The session secret is random per process unless `API_SESSION_SECRET` is set.
- MySQL runs as `root` with a demo password in `docker-compose.yml`.
- The demo APK is signed with a demo-only key kept outside git; it is not a store key.

## Production hardening plan

1. **TLS everywhere**: reverse proxy (Caddy/nginx) with certificates, HSTS; remove cleartext from
   the Android network config and pin the API certificate's public key.
2. **Identity**: per-user accounts with roles (cashier, butcher, quality manager, admin) via OIDC;
   short-lived JWTs; recall and key provisioning limited to the quality role; MFA for admins.
3. **Secret management**: DB credentials, `API_SESSION_SECRET` and third-party keys from a secret
   store (Vault / cloud KMS) injected at runtime; OCR calls go through the server so no key ever
   reaches a device.
4. **Key rotation**: rotate the session secret (two active keys during rollover), POS device keys
   on reinstall/compromise via provisioning, DB passwords quarterly; Play App Signing for the APK.
5. **Database**: dedicated least-privilege users (API: DML + EXECUTE only; migrations separate),
   TLS to MySQL, encrypted backups, binlog-based audit shipping.
6. **Abuse controls**: shared rate-limit/lockout store (Redis) across API instances, alerting on
   `KEY_MISMATCH`, `REPLAY_MISMATCH` and repeated `*_DENIED` events.
7. **Supply chain**: Dependabot alerts, `govulncheck`, `npm audit`, gitleaks in CI,
   signed release artefacts with checksums.

## Reporting

Please report vulnerabilities privately to the repository owners (GitHub *Security → Report a
vulnerability*), not in public issues.
