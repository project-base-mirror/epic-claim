import { checkoutMatches, isNumericZero, active } from './core.mjs';

// Both labels are evidenced by upstream checkout implementations. The same
// identity, line-item and numeric final-total checks still gate either button.
export const CHECKOUT_ACTION = /^(?:Place Order|Add to library)$/i;

const visible = async locator => { for (const l of await locator.all()) if (await l.isVisible()) return true; return false; };
async function uniqueVisible(locator) {
  const found = [];
  for (const l of await locator.all()) if (await l.isVisible()) found.push(l);
  if (found.length !== 1) throw Error('Ambiguous control');
  return found[0];
}
// Required evidence contract, not a claim about Epic's current live markup:
// a single primary purchase region must carry exact offer + namespace attributes.
// A heading, URL, edition title, or unrelated Owned button is never ownership.
export const PRIMARY_PURCHASE = 'main [data-testid="purchase-section"], main [data-component="PurchaseSection"]';

// This function runs inside the actual browser DOM. Keep it self-contained so
// Playwright evaluate and local HTML-fixture tests exercise identical extraction.
export function extractDOMEvidence({ kind, game }) {
  const visible = el => !!(el.getClientRects().length && getComputedStyle(el).visibility !== 'hidden');
  const all = (root, selector) => Array.from(root.querySelectorAll(selector));
  const rendered = (root, selector) => all(root, selector).filter(visible);
  const identity = el => el.getAttribute('data-namespace') === game.namespace && el.getAttribute('data-offer-id') === game.id;
  if (kind === 'purchase') {
    // Observed on the public Epic storefront on 2026-10-14. JSON-LD identifies
    // the main product; a unique first-party CTA supplies its account state.
    // Catalog prices in this script NEVER substitute for checkout totals.
    const scripts = all(document, 'script[id="_schemaOrgMarkup-Product"][type="application/ld+json"]');
    if (scripts.length) {
      if (scripts.length !== 1 || game.namespace.includes(':') || game.id.includes(':')) return null;
      let product;
      try { product = JSON.parse(scripts[0].textContent); } catch { return null; }
      const productPath = value => {
        try {
          const u = new URL(value);
          if (u.protocol !== 'https:' || u.hostname !== 'store.epicgames.com' || u.port || u.username || u.password || u.hash) return null;
          const params = [...u.searchParams.entries()];
          if (params.length && (params.length !== 1 || params[0][0] !== 'lang' || params[0][1] !== 'en-US')) return null;
          const match = u.pathname.match(/^\/(?:en-US\/)?p\/([^/]+)\/?$/);
          return match ? match[1] : null;
        } catch { return null; }
      };
      const expectedPath = productPath(game.url);
      const canonicals = all(document, 'link[rel="canonical"]');
      if (!expectedPath || canonicals.length !== 1 || productPath(document.URL) !== expectedPath ||
          productPath(canonicals[0].href) !== expectedPath || productPath(product.url) !== expectedPath ||
          productPath(product['@id']) !== expectedPath || product['@type'] !== 'Product' ||
          product.mainEntityOfPage !== true || product.sku !== `${game.namespace}:${game.id}` || product.name !== game.title) return null;
      const headings = rendered(document, 'h1');
      const buttons = rendered(document, 'button[data-testid="purchase-cta-button"]');
      if (headings.length !== 1 || headings[0].textContent.trim() !== game.title || buttons.length !== 1) return null;
      const label = buttons[0].textContent.trim();
      const owned = /^(?:In Library|Owned)$/i.test(label);
      const get = /^Get$/i.test(label);
      if (!owned && !get) return null;
      return { identityVerified: true, owned, getCount: get ? 1 : 0, ownedCount: owned ? 1 : 0, source: 'epic-product-schema' };
    }
    const regions = rendered(document, 'main [data-testid="purchase-section"], main [data-component="PurchaseSection"]');
    if (regions.length !== 1 || !identity(regions[0])) return null;
    const buttons = rendered(regions[0], 'button, [role="button"]');
    const owned = buttons.filter(el => /^(?:In Library|Owned)$/i.test(el.textContent.trim()));
    const get = buttons.filter(el => /^Get$/i.test(el.textContent.trim()));
    // Conflicting primary actions, or nested offer regions for another edition,
    // invalidate the evidence even when one matching button exists.
    const nested = all(regions[0], '[data-offer-id], [data-namespace]');
    if (nested.some(el => !identity(el))) return null;
    return { identityVerified: true, owned: owned.length === 1 && get.length === 0, getCount: get.length, ownedCount: owned.length };
  }
  if (kind !== 'checkout') return null;
  const summaries = rendered(document, '[data-testid="order-summary"], .payment-order-summary');
  const containers = rendered(document, '[data-testid="order-items"], .payment-order-summary__items');
  if (summaries.length !== 1 || containers.length !== 1 || !summaries[0].contains(containers[0])) return null;
  const summary = summaries[0], container = containers[0];
  const itemSelector = '[data-testid="order-line-item"], .payment-order-summary__item';
  // Enumerate the container's actual rows, including rows without identity.
  // Unknown wrappers/extra rows are NOT discarded by an identity-filter selector.
  const rows = Array.from(container.children).filter(visible);
  const markedRows = rendered(document, itemSelector);
  if (rows.length !== 1 || markedRows.length !== rows.length || rows.some(el => !el.matches(itemSelector)) || markedRows.some(el => !rows.includes(el))) return null;
  const item = rows[0];
  if (!identity(item)) return null;
  const nested = all(item, '[data-offer-id], [data-namespace]');
  if (nested.some(el => !identity(el))) return null;
  const titles = rendered(item, '[data-testid="order-item-title"], .payment-order-summary__item-title');
  const quantities = rendered(item, '[data-testid="item-quantity"], .payment-order-summary__item-quantity');
  const quantityAttr = item.getAttribute('data-quantity');
  if (titles.length !== 1 || titles[0].textContent.trim() !== game.title) return null;
  if (quantityAttr !== null && quantityAttr !== '1') return null;
  if (quantities.length > 1 || quantities.some(el => !/^(?:Quantity:\s*)?1$/i.test(el.textContent.trim()))) return null;
  if (quantityAttr === null && quantities.length !== 1) return null;
  // Financial evidence is a closed structural contract. Never infer a final
  // order amount from item-level Total/Tax labels or an arbitrary descendant.
  const totalSelector = '[data-testid="final-order-total"], .payment-order-summary__final-total';
  const taxSelector = '[data-testid="order-tax"], .payment-order-summary__order-tax';
  const totals = rendered(document, totalSelector), taxes = rendered(document, taxSelector);
  if (totals.length !== 1 || taxes.length !== 1) return null;
  const totalRow = totals[0], taxRow = taxes[0];
  if (totalRow.parentElement !== summary || taxRow.parentElement !== summary || container.parentElement !== summary) return null;
  // Only these three order-summary sections are supported. Unknown subtotal,
  // amount-due, fee, balance or extra order-level structures require review.
  const allowed = [container, totalRow, taxRow];
  const summaryChildren = Array.from(summary.children).filter(visible);
  if (summaryChildren.length !== 3 || summaryChildren.some(el => !allowed.includes(el))) return null;
  const directText = el => Array.from(el.childNodes).filter(n => n.nodeType === 3).map(n => n.textContent).join('').trim();
  if (directText(summary) || directText(container)) return null;
  const amount = (row, selector, labelPattern) => {
    if (directText(row)) return null;
    const children = Array.from(row.children).filter(visible);
    const amounts = rendered(row, selector);
    if (children.length !== 2 || amounts.length !== 1 || !children.includes(amounts[0]) || amounts[0].children.length) return null;
    const label = children.find(el => el !== amounts[0]);
    if (label.children.length || !labelPattern.test(label.textContent.trim())) return null;
    return amounts[0].textContent.trim();
  };
  const finalTotal = amount(totalRow, '[data-testid="final-order-total-amount"], .payment-order-summary__final-total-amount', /^(?:Total|Total Price|Order Total|Final Total)$/i);
  const tax = amount(taxRow, '[data-testid="order-tax-amount"], .payment-order-summary__order-tax-amount', /^(?:Tax|Taxes|VAT)$/i);
  if (finalTotal === null || tax === null) return null;
  // A second financial region outside the recognized summary is also ambiguous.
  // Check direct text so a label with a nested amount cannot escape detection.
  const outside = rendered(document, 'body *').filter(el => !summary.contains(el) && el !== summary);
  const financialText = /(?:\b(?:total|subtotal|amount\s+(?:due|payable|charged)|balance\s+due|tax|vat)\b|[$€£¥]\s*\d|\b[A-Z]{3}\s+\d)/i;
  if (outside.some(el => financialText.test(directText(el)))) return null;
  return { identityVerified: true, itemCount: rows.length, finalTotal, tax };
}

export class UnsupportedLayoutError extends Error {
  constructor() { super('Exact offer evidence is unavailable in this page layout.'); this.code = 'UNSUPPORTED_DOM'; }
}
export class EpicBrowser {
  constructor(context) { this.context = context; this.page = null; this.checkoutFrame = null; }
  async open(g) {
    this.page ??= await this.context.newPage();
    this.checkoutFrame = null;
    await this.page.goto(g.url.replace('store.epicgames.com/p/', 'store.epicgames.com/en-US/p/'), { waitUntil: 'domcontentloaded', timeout: 45000 });
    this.productVerified = false;
    try {
      await this.page.getByRole('heading', { name: g.title, exact: true }).first().waitFor({ timeout: 10000 });
      this.productVerified = true;
    } catch { /* blocker() distinguishes manual-action pages after navigation */ }
  }
  async blocker() {
    for (const p of this.context.pages()) {
      for (const frame of p.frames()) {
        const url = frame.url();
        if (/captcha|hcaptcha|recaptcha/i.test(url)) {
          // Only visible challenge frames count; invisible anti-bot scripts do not.
          const element = frame.parentFrame() ? await frame.frameElement().catch(() => null) : null;
          if (!element || await element.isVisible()) return 'captcha_required';
        }
        if (await visible(frame.locator('input[autocomplete="one-time-code"], input[name="code"], input[name="securityCode"]'))) return 'two_factor_required';
        if (await visible(frame.getByText(/^(?:Verify you are human|Please complete the security check)/i))) return 'captcha_required';
        if (await visible(frame.getByText(/^(?:Enter your (?:security|verification) code|Two.factor authentication)/i))) return 'two_factor_required';
        if (await visible(frame.locator('input[type="password"]')) || /\/id\/(?:login|register)/.test(url)) return 'login_required';
        if (await visible(frame.getByRole('checkbox', { name: /agree|terms|license|eula/i })) || await visible(frame.getByRole('button', { name: /^(?:I accept|Accept terms|Agree and continue|I agree)$/i })) || await visible(frame.getByText(/by (?:placing|clicking|completing).{0,80}(?:agree|accept)/i))) return 'terms_required';
      }
    }
    return null;
  }
  async owned(g) {
    if (!this.productVerified) throw new UnsupportedLayoutError();
    const evidence = await this.page.evaluate(extractDOMEvidence, { kind: 'purchase', game: g });
    if (!evidence?.identityVerified) throw new UnsupportedLayoutError();
    return evidence.owned === true;
  }
  async get(g) {
    if (!this.productVerified) throw new UnsupportedLayoutError();
    const evidence = await this.page.evaluate(extractDOMEvidence, { kind: 'purchase', game: g });
    if (!evidence?.identityVerified || evidence.getCount !== 1 || evidence.ownedCount !== 0) throw new UnsupportedLayoutError();
    const get = evidence.source === 'epic-product-schema'
      ? await uniqueVisible(this.page.locator('button[data-testid="purchase-cta-button"]').filter({ hasText: /^Get$/i }))
      : await uniqueVisible((await uniqueVisible(this.page.locator(PRIMARY_PURCHASE))).getByRole('button', { name: /^Get$/i }));
    await get.click({ timeout: 15000 });
    // Wait for either a checkout frame/control or a manual-action state.
    for (let n = 0; n < 30; n++) {
      if (await this.blocker()) return;
      for (const p of this.context.pages()) for (const f of p.frames()) {
        if (await visible(f.getByRole('button', { name: CHECKOUT_ACTION }))) { this.checkoutFrame = f; return; }
      }
      await new Promise(resolve => setTimeout(resolve, 500));
    }
    throw Error('Checkout unavailable');
  }
  async checkout(g) {
    const f = this.checkoutFrame;
    if (!f || !checkoutMatches(f.url(), g)) throw new UnsupportedLayoutError();
    const evidence = await f.evaluate(extractDOMEvidence, { kind: 'checkout', game: g });
    if (!evidence) throw new UnsupportedLayoutError();
    return evidence;
  }

  async placeOrder(g, previous) {
    if (await this.blocker() || !active(g)) throw Error('Unsafe checkout');
    const fresh = await this.checkout(g);
    if (!fresh || !fresh.identityVerified || fresh.itemCount !== 1 || JSON.stringify(fresh) !== JSON.stringify(previous) || !isNumericZero(fresh.finalTotal) || !(isNumericZero(fresh.tax) || fresh.tax === 'Included')) throw Error('Checkout changed');
    const button = await uniqueVisible(this.checkoutFrame.getByRole('button', { name: CHECKOUT_ACTION }));
    await button.click({ timeout: 15000 });
    // No success is inferred here. Allow the server to settle before reloading.
    await this.page.waitForTimeout(3000);
  }
}
