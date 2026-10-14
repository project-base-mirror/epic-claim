# Cloud implementation validation

Baseline: c142856fbff41b29ec2e153eb56782017d01dfd1 from project-base-mirror/epic-claim.
Validation date: 2026-10-14. Linux cloud workspace, Go 1.24.13, Node.js 24.19.0.

Passed:

- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build ./cmd/epic-claim`
- `npm --prefix workers/epic test`: 18 tests, including jsdom HTML extraction and pre-click mutations
- `node --check internal/web/static/app.js`
- `git diff --check`

Coverage includes durable deduplication, restart reconciliation, database single-instance locking, blocked-state resume, completed-job protection, safe-failure backoff, configuration validation, authenticated API handling, and an offline HTTP handler → SQLite → scheduler → fixture worker integration test. Worker tests include final zero totals, exact offer identities, expiry, immediate pre-click revalidation, manual gates, and post-checkout ownership verification.

Not verified:

- Real Epic login, challenges, checkout, or current storefront markup compatibility
- Docker build/runtime and graphical session bootstrap inside a container
- Browser-rendered visual QA: Chromium startup failed at runtime with socket permissions even after an approved escalation; no reviewer denial occurred
- Production deployment, notification delivery, high availability, or existing-user data migration

Post-review: previous broad ownership/button and checkout title-count heuristics were identified as unsafe and replaced by exact identity/line-item evidence. Regression fixtures cover unrelated ownership buttons, additional cart items, and item-level zero amounts versus a positive final order amount. Synthetic DOM fixtures do not establish current Epic compatibility. The adapter must return unsupported when those required structures are absent.

No real account secrets were used. No live claims or deployment were performed. The source was initially delivered for review without publication; the user subsequently authorized committing and pushing it. The UI and worker intentionally state when manual action or verification is required.

## Later public-frontend review (2026-10-14)

The earlier Chromium startup failure applies to that local Playwright launch;
it does not mean the supported remote cloud browser is unavailable. Using that
cloud browser, the exact updated product extractor was run read-only against
Epic's public Fortnite page and verified the expected SKU/URL/title/CTA result.
See [frontend evidence](epic-frontend-evidence.md) for source and capture details.
Authenticated checkout and ownership remain unverified. No Get/submit click,
login or claim was performed. The final checkout structural fixture is still
synthetic; automatic claims may stop as unsupported there.

Latest worker verification: `npm --prefix workers/epic test`, **23 passed**.
Added source-backed packed checkout URL parsing, the Add to library label, a
captured public product DOM fixture and mutation checks. The changes do not
relax final total, tax, line count or identity requirements before submission.

A new Go rerun in the current shell was blocked because `/usr/bin/go` resolves
to an unrelated command (`Go: Unknown option: test`); the earlier Go test/build
results above are historical results, not a fresh rerun of this tranche. This
tranche changed worker JavaScript/tests and documentation, not Go code.

## Pre-publication verification (2026-10-14)

The real Go 1.24.13 toolchain was recovered from the cloud workspace. Fresh
`go test ./...`, `go test -race ./...`, `go vet ./...`, and
`go build ./cmd/epic-claim` all passed. The latest 23 Node tests,
`node --check internal/web/static/app.js`, and `git diff --check` also passed.
These are offline checks; authenticated checkout and Docker remain unverified.
