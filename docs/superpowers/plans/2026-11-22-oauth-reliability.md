# OAuth reliability implementation plan

> Use subagent-driven-development for isolated components and root integration; user approved the design on 2026-11-22.

Goal: complete the approved 13-item reliability scope without losing the existing uncommitted fixes.
Architecture: retained per-owner OAuth sessions hold an account lease until submit/cancel/expiry; token and profile are resumable stages; router guards serialize account mutations; worker shares the account lock.
Tech stack: existing Go, SQLite, Vue/TypeScript; no dependency additions.
Spec: docs/superpowers/specs/2026-11-22-oauth-reliability-design.md
Baseline: 9da8e73 (includes the user's existing fixes copied from the source checkout).

## Global constraints
- Preserve existing user changes. Worktree only until final integration.
- Never emit tokens, authorization codes or proxy secrets in logs/errors/metrics.
- Single-process coordination; browser egress and sticky proxy IP require deployment validation.
- No invented refresh_token parameter on authorization_code grant.
- Meaningful failing regression tests before implementation, focused validation after each task, whole-project checks once integrated.

## Task 1: OAuth client stages (agent; internal/claudeoauth only)
- [x] Add regression tests for token success followed by profile/roles errors, typed response vs transport failures, 403/429/5xx probe distinction, fixed request headers, Retry-After overflow.
- [x] Export Token(ctx, client, code, state, verifier) (Result,error); retain Exchange wrapper. CompleteProfile returns non-nil partial Result on every recoverable profile/roles failure; Result.ProfileLoaded bool (json omitted) permits roles-only resume.
- [x] Add TransportError{Op string, BeforeWrite bool}, AsTransportError; only definitely pre-connection failures qualify for automatic retry. Preserve ResponseError and StatusError, no secrets in errors.
- [x] Add Observation{Stage string, Status int, Duration time.Duration, Kind string} and WithObserver(ctx, func(Observation)) context helper; observe probe/token/profile/roles once per actual request.
- [x] Stable application UA, Accept-Language, application version header; no undocumented old-refresh-token parameter.
- [x] Run go test ./internal/claudeoauth and report.

## Task 2: Frontend lifecycle (agent; web only)
- [x] Add accountId/email props, always pass actual account ID independent of proxy selection.
- [x] OAuthResult gains oauth_session_id?: string; store it in AccountForm and send top-level on reauthorize/onboard. Keep ready session through component unmount during wizard transitions; explicit discard closes it through parent.
- [x] Start response includes proxy_id/account_id; normal start reuses; refresh is explicit button labelled 作废并重新获取. Proxy selection with pending requires that explicit action, not automatic DELETE watcher.
- [x] Absolute-deadline cooldown gates buttons/enter/paste/start/refresh, including RATE_LIMITED. Use 75s exchange and 45s start timeout.
- [x] Error actions use codes/action, no blanket status>=500; only oauth_session_expired flag clears expired flow; OAUTH_RESULT_INVALID clears paste but retains session.
- [x] Add regression tests, run tests/typecheck; report.

## Task 3: Credential invariants (agent; validation.go, account_reauthorize.go, onboard.go, worker.go, app.go accountCredentials, advanced.go and tests)
- [x] Separate device-ID initialization from pure validateAuthFields. Persist generated IDs once; explicit values win, then saved IDs. Preserve old imports and worker retries.
- [x] Add credential freshness comparison under account lock for reauthorize/onboard/ordinary credentials: reject earlier last_refresh or expired and ambiguous equal timestamps with different tokens.
- [x] Worker may resolve a missing legacy proxy once and persist it; when existing proxy_url differs from current pool URL reject PROXY_BINDING_CHANGED rather than overwrite.
- [x] For accountReauthorize use a.lockAccountWrite(ctx,id) instead of direct accountLocks.lock; root implements context-aware guard and holds locks around HTTP write handlers. Do not implement own session lifecycle.
- [x] Expose applyCredentialInvariants(auth,saved map[string]any) *errs.Error for writer callers, with focused tests.
- [x] Add a.oauth.close() in App.Close before final DB.Close (root implements close); do not alter App fields unless coordinated.
- [x] Run focused Go tests and report.

## Task 4: Server session lifecycle and guards (root)
- [x] Atomic per-owner start; fixed network snapshot/client; all probes gated; retain tokens/results until final submit.
- [x] Hold shared account lock from start; reject active jobs; release safely on cancel/expiry and shutdown; claiming prevents duplicate exchange.
- [x] Router account-write guard acquires ordinary locks or validates OAuth proof and borrows the already-held lease. Store success response for idempotent submit replay. Validate account/proxy/tokens against session.
- [x] Freeze indirect writes through shared proxy operations/assignments, avoiding lock-order inversions.
- [x] Full Retry-After without business cap; owner fallback plus email alias; recheck after queue.
- [x] One pre-write retry using a clone of captured transport only. Stage errors and low-cardinality histograms + protected per-account audit.

## Task 5: Integration and delivery (root + review)
- [x] Full Go, race-targeted, frontend tests/typecheck/build, relevant E2E.
- [x] Independent review of lease lifetime, lock order, stale credentials, exchange/push replay and secret exposure; fix findings and retest.
- [x] Update docs and completion matrix, save baseline SHA256 and patch + recovery method; verify artifacts exist.
