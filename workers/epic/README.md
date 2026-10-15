# Epic browser worker

This is an experimental, conservative Playwright adapter, not a simulated success worker.
It has NOT been validated against a signed-in live Epic account. Epic's storefront
and checkout may change; unsupported/ambiguous markup stops the claim. Expect
manual intervention, especially first login, age verification, CAPTCHA, 2FA and
new terms. No solver, stealth, fingerprint bypass, password storage, or automatic
terms acceptance is included.

## Install

Use Node 22 or later. In this directory run `npm ci` and
`npx playwright install --with-deps chromium`. The dependency and lockfile are pinned. Run `npm test` for offline mock
tests (these do not need Playwright installed). The Go tests also exercise invalid,
missing, duplicated, unknown, oversized, slow and failed worker responses.

## Session bootstrap

Set `EPIC_PROFILE_ROOT` to a private persistent directory shared with the worker.
On the trusted operator host, with a graphical display, run:

    node workers/epic/login.mjs 1

Replace `1` with the dashboard account's numeric ID. Sign in and resolve any
challenge yourself in the browser, then close it. The worker will reuse that
profile. To resolve a checkout-specific CAPTCHA or terms prompt, optionally pass
the game product URL as a second argument, or navigate there yourself in this
browser. Manually open checkout and complete only the actions you approve. Close
the browser and retry the account from the dashboard. If you completed a claim
manually, the next run checks ownership first. Some challenges do not persist
across checkout attempts; those claims may require completing checkout manually. Never paste passwords, cookies, codes or browser profiles into the app.
Profiles contain sensitive session cookies: restrict host and volume access,
exclude them from source control, backups and support bundles as appropriate.
The interactive and scheduled processes must use the same profile directory and
OS user. A cloud container without a GUI cannot perform interactive bootstrap;
provide a secured operator display or bootstrap on the same trusted host. Do not
expose an unauthenticated remote-debugging endpoint.

Each account uses `account-<ID>` under that root, mode 0700, with an exclusive lock.
A crashed/hard-killed worker or a recreated container may leave `account-<ID>.lock`. The lock stores `<hostname> <pid>`; workers reclaim it automatically when the owner is gone (other hostname = old container, or dead PID; legacy locks without owner after 15 minutes) and also remove Chromium's stale `Singleton*` links. A lock held by a live process is never taken over.

## Protocol

Configure the Go command as `node /app/workers/epic/worker.mjs`. It accepts exactly
one JSON object on stdin with `account_id` and `games` (the catalog Game fields),
and prints exactly one JSON array to stdout. Every requested namespace/offer pair
must have one result. Messages returned to Go are replaced with safe fixed text.
Diagnostics never echo browser errors or URLs. Stdout and input are bounded and
the Go adapter defaults to a five-minute timeout.

Supported result statuses: claimed, owned, failed, unconfirmed, login_required,
captcha_required, two_factor_required, terms_required, price_changed, unsupported. A missing
browser install/profile/login does not report success.

The adapter supports an English storefront and checkout URL identity with either
an exact `namespace` plus single raw `offers` value, or one packed
`offers=1-<namespace>-<offerId>` value on `/store/purchase` or `/purchase`. It checks a single matching
product, one explicit final Total amount, and one explicit Tax amount (zero or
Included). Only numeric zero is accepted; a Free badge is insufficient. It
rechecks this and promotion expiry immediately before Place Order / Add to library. New terms
must be handled manually. Post-checkout success requires reloading the exact
product and seeing its account-specific In Library/Owned purchase control;
thank-you text alone is never success. An ambiguous response is unconfirmed.

Selectors and checkout identity rules intentionally have no broad fallback. If
Epic changes its DOM, inspect the live flow manually, add a redacted local fixture
and update the adapter/tests before enabling claims again. Do not weaken total,
tax, identity, terms, or ownership verification to make a failing claim pass.

Reference: https://playwright.dev/docs/api/class-browsertype#browser-type-launch-persistent-context


## Public product evidence and experimental checkout

See [frontend evidence](../../docs/epic-frontend-evidence.md) for dated sources,
a captured public fixture and the precise remaining authenticated-checkout gap.
On 2026-10-14, the exact product extractor was verified read-only against a live
public Epic page. It binds the main Product JSON-LD `sku` to namespace/offer ID,
checks current/canonical/product URLs and title, and requires a unique visible
`purchase-cta-button`. No account login or Get/submit click was performed.
Only that public product extraction is live-verified. Authenticated ownership
and checkout are not. Catalog JSON-LD prices never count as final order totals.

### Legacy synthetic contract (checkout remains unverified)

The legacy data-attribute selectors below describe a strict adapter contract
tested with synthetic HTML, not a claim that Epic currently renders those
attributes. The new public schema path replaces this requirement for matching
product pages, but checkout still uses this unverified contract and may stop
every live claim as `unsupported`. This terminal state requires manual review,
not automatic retries. Never weaken evidence requirements merely to get a claim
through. Validate and update the adapter against a manually reviewed, redacted
live fixture before relying on automatic claims.

Ownership requires exactly one rendered `main [data-testid="purchase-section"]`
or `main [data-component="PurchaseSection"]` carrying both `data-namespace` and
`data-offer-id` equal to the requested offer. The Owned/In Library or Get control
must be inside that region. A matching page heading or another edition's Owned
button is insufficient. Conflicting/nested identities invalidate the evidence.

Checkout requires one `[data-testid="order-summary"]` or
`.payment-order-summary` and a contained `[data-testid="order-items"]` or
`.payment-order-summary__items`. Every rendered direct child is enumerated,
including unidentified rows. Exactly one row may exist, marked
`[data-testid="order-line-item"]` or `.payment-order-summary__item`, with exact
namespace and offer attributes, explicit quantity one, and one exact matching
title in `[data-testid="order-item-title"]` or
`.payment-order-summary__item-title`. Extra, unknown, mismatched or ambiguous rows
stop the claim. Total and Tax evidence must be inside that same summary.

`browser.dom.test.mjs` runs real HTML parsing and DOM extraction using jsdom.
Geometry and innerText are shimmed because jsdom has no layout engine. These
fixture tests establish extraction safety for the declared contract; they do not
establish Chromium behavior or current Epic compatibility.

Account identity binding is not implemented. A numeric account ID isolates a
browser profile; it does not prove which Epic account is signed in. Operators
must verify the intended signed-in account in the manual profile browser. The
worker does not ingest credentials or claim to verify an Epic account identifier.

The summary has a closed financial structure: its only rendered direct children
must be the items container, a unique `[data-testid="final-order-total"]` (or
`.payment-order-summary__final-total`) row, and a unique
`[data-testid="order-tax"]` (or `.payment-order-summary__order-tax`) row. Each
financial row must have exactly one plain label and one dedicated amount element:
`[data-testid="final-order-total-amount"]` /
`.payment-order-summary__final-total-amount`, or
`[data-testid="order-tax-amount"]` /
`.payment-order-summary__order-tax-amount`. Generic item-level Total/Tax labels
are never used as order-level amounts. Extra order-level rows, duplicate financial
regions, unidentified amounts and additional amount-due text require review.

## 网页驱动的无头登录（login-headless.mjs）

由 Go 服务在用户点击“登录 Epic”时启动，凭据只通过 stdin 传入（不出现在 argv、环境变量或日志中）。协议为 JSON 行：stdin 第一行是账户编号/邮箱/密码/TOTP，之后可能收到 `{"code":"…"}`；stdout 输出 `logging_in`、`code_required`、`code_submitted`、`ready`、`captcha_required`、`two_factor_required`、`login_failed`、`error` 等状态。页面识别逻辑在 `login-flow.mjs`，选择器基于 Epic 公开登录页的常见结构，**未用真实账号验证**；无法识别的页面 60 秒后以 `error` 结束，出现人机验证时以 `captcha_required` 结束，不做任何规避。与领取 Worker 共用同一 profile 锁，因此同一账户不会同时登录和领取。
