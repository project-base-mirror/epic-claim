# Epic frontend evidence and remaining compatibility gap

Research and public-page observation: **2026-10-14 UTC**. No Epic login, account
credentials, claim, CAPTCHA, checkout submission, push, or deployment was used.

## Verified public storefront behavior

Observed URL: https://store.epicgames.com/p/fortnite?lang=en-US (the storefront
redirected the `/en-US/p/fortnite` URL here). Read-only DOM inspection in the
remote **cloud** Chrome browser found:

- `button[data-testid="purchase-cta-button"]`, with nested spans and text `Get`.
- `h1` containing `[data-testid="pdp-title"]` and `Fortnite`.
- `link[rel="canonical"]` matching the current product URL.
- `script#_schemaOrgMarkup-Product[type="application/ld+json"]` with a main
  `Product`, `mainEntityOfPage: true`, `sku: fn:09176f4ff7564bbbb499bbe20bd6348f`,
  the same product URL and title.
- The button's inspected ancestors had CSS classes, not the adapter's previously
  invented offer/namespace attributes. Those invented attributes were therefore
  not a viable requirement for this observed public page.

The adapter now recognizes this public structure. It requires exact
`namespace:offerId` equality against the requested identity; matching product,
canonical and actual URLs; one matching main heading; and one visible primary
CTA. Unrelated `Owned` text is ignored. Multiple CTAs or inconsistent metadata
are unsupported. It does **not** use JSON-LD prices, related offers, or a page
heading alone as checkout/ownership evidence.

The exact updated `extractDOMEvidence` function was executed read-only in that
live page and returned `identityVerified: true`, `getCount: 1`, `owned: false`,
`ownedCount: 0`, `source: epic-product-schema`. No Get button was clicked.
This verifies one public product layout, not all offers, authenticated ownership,
or ordering. The fixture `workers/epic/fixtures/fortnite-public.html` contains
captured DOM excerpts; prose, unrelated offers, and much layout were omitted.
Its ownership-label mutations are explicitly synthetic, not account captures.

## Upstream source evidence

These are source observations, not assertions that each project's present-day
claim flow works. GPL Python implementation code was not copied into this MIT
project; only externally observable selectors/labels were used to inform an
independently written adapter.

1. [claabs purchase URL builder](https://github.com/claabs/epicgames-freegames-node/blob/8dfa98f489166e77c7ba947ef32842469cb4a378/src/purchase.ts)
   and [endpoint constants](https://github.com/claabs/epicgames-freegames-node/blob/8dfa98f489166e77c7ba947ef32842469cb4a378/src/common/constants.ts):
   `/store/purchase` uses repeated `offers=1-<namespace>-<offerId>` tuples.
   Our single-item adapter now accepts exactly one such tuple, quantity one,
   while rejecting duplicates, mixed identity representations and other offers.
   It compares the full tuple rather than incorrectly splitting hyphenated IDs.
   The existing separate-namespace format remains supported.
2. [claabs current README](https://github.com/claabs/epicgames-freegames-node):
   v5 sends checkout links and says purchase is no longer automated. It must not
   be cited as proof of current automatic checkout compatibility.
3. [claabs free-games source](https://github.com/claabs/epicgames-freegames-node/blob/8dfa98f489166e77c7ba947ef32842469cb4a378/src/puppet/free-games.ts):
   `getMappingByPageSlug` can map a product slug to exact offer identity;
   `getOffersValidation` reads `Entitlements.cartOffersValidation`, including
   `fullyOwnedOffers`. A failure to purchase is not necessarily ownership:
   conflicts and prerequisites are separate conditions. The accompanying
   [types](https://github.com/claabs/epicgames-freegames-node/blob/8dfa98f489166e77c7ba947ef32842469cb4a378/src/interfaces/types.ts)
   describe preview requests with `lineOffers` (namespace, offerId, quantity,
   title), but leave the preview response body broadly untyped. That is
   insufficient evidence for a safe current final-total response parser.
4. [Ronchy2000 service](https://github.com/Ronchy2000/epic-freebies-helper/blob/d90190bf3f9f87e9da71c50c81f155c4586bdb33/app/services/epic_games_service.py)
   uses `purchase-cta-button`, purchase iframes, and both `Place Order` and
   `Add to library`. Its [maintenance log](https://github.com/Ronchy2000/epic-freebies-helper/blob/d90190bf3f9f87e9da71c50c81f155c4586bdb33/docs/maintenance-log.md)
   describes observed instant-free checkout replacing the old button label.
5. [10000ge10000 service](https://github.com/10000ge10000/epic-kiosk/blob/4e81e534e4c06843cb9aed5d19340c7357b8a135/app/services/epic_games_service.py)
   also checks the product CTA and warns against page-wide ownership text;
   recognizes `#webPurchaseContainer iframe` and purchase frames.
6. [Magerko service](https://github.com/Magerko/Epic-Claimer/blob/1d6f32c59e34f5b8376c59a2e24577a9354abca0/app/services/epic_games_service.py)
   uses the same CTA and `Add to library` confirmation.

The Python sources do not supply a sufficiently verified exact final-total,
tax and complete line-item evidence contract. Their broad selectors, title or
namespace-only ownership shortcuts, and API-free-price assumptions are not
adopted. `Add to library` is now a recognized submit label, but is subject to
exactly the same pre-submit identity/amount/tax guard as `Place Order`.

## Still blocked: safe authenticated checkout support

The checkout extractor still requires its earlier **synthetic** structural
contract. That contract has never been established on actual Epic checkout.
Automatic claiming therefore remains experimental and may stop as unsupported
at checkout. Do not advertise this work as a functioning end-to-end claimer.

To close the gap, an operator-authorized, redacted capture is required of:

- The exact active checkout frame URL and current product identity.
- The complete order lines, including offer namespace/ID and quantity (or a
  documented first-party response that binds those lines to that checkout).
- The authoritative final amount including taxes, currency, and any fees;
  original/list price, catalog zero price and `THIS IS FREE` are not enough.
- Fresh pre-submit evidence and actual post-submit entitlement/primary CTA state.
- Any new terms or challenge state, which must remain manual.

No such authenticated capture was available in this task. No private API call
was replayed, token obtained, or account action attempted. Public structured
data solves product identification only; it cannot establish account-specific
entitlement or final checkout totals.

## Tests and tooling distinction

`npm test`: 23 passing tests at this change, including the captured public DOM,
identity/CTA mutations, packed URLs, button labels, and unchanged checkout safety
regressions. jsdom tests are not Chromium integration tests.

A separate live read-only test used the supported cloud browser API via
`cua.createBrowserTab('cdp', URL)` and `tab.playwright.evaluate`.
The API documentation explicitly identifies `cdp` as the remote cloud browser.
This succeeded independently of any local Playwright Chromium launch issues;
no user's desktop was selected. The live test verified product extraction only.
