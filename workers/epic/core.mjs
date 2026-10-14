// Conservative English-language DOM adapter. No CAPTCHA solvers, stealth plugins,
// credential input, private checkout APIs, or automatic acceptance of terms.
export const BLOCKED = ['login_required', 'captcha_required', 'two_factor_required', 'terms_required'];
export function validateRequest(r) {
  if (!r || !Number.isSafeInteger(r.account_id) || r.account_id <= 0 || !Array.isArray(r.games) || r.games.length > 100 || Object.keys(r).some(k => !['account_id', 'games'].includes(k))) throw Error('Invalid request');
  const seen = new Set();
  for (const g of r.games) {
    if (!g || typeof g.id !== 'string' || typeof g.namespace !== 'string' || !g.id || !g.namespace || /[\x00-\x1f]/.test(g.id + g.namespace) || typeof g.title !== 'string' || !g.title.trim()) throw Error('Invalid offer');
    const key = `${g.namespace}\0${g.id}`;
    if (seen.has(key)) throw Error('Duplicate offer');
    seen.add(key);
    const u = new URL(g.url);
    if (u.protocol !== 'https:' || u.hostname !== 'store.epicgames.com' || u.port || u.username || u.password || !/^\/(?:[a-z]{2}(?:-[A-Z]{2})?\/)?p\/[^/]+\/?$/.test(u.pathname) || u.search || u.hash) throw Error('Invalid product URL');
    if (!Number.isFinite(Date.parse(g.startDate)) || !Number.isFinite(Date.parse(g.endDate)) || Date.parse(g.startDate) >= Date.parse(g.endDate)) throw Error('Invalid promotion window');
  }
  return r;
}
export function active(g, now = Date.now()) { return Date.parse(g.startDate) <= now && now < Date.parse(g.endDate); }
// Only an explicit zero amount is accepted. FREE, rounded values, negatives,
// unknown separators, mixed currencies and multiple amounts are rejected.
export function isNumericZero(text) {
  if (typeof text !== 'string') return false;
  return /^(?:(?:[A-Z]{3}|US\$|CA\$|AU\$|[$€£¥])\s*0(?:[.,]0{1,3})?|0(?:[.,]0{1,3})?(?:\s*[A-Z]{3})?)$/.test(text.trim());
}
// Source-backed URL formats; neither format proves the rendered order contents.
// See docs/epic-frontend-evidence.md. Compare the entire encoded offer tuple,
// rather than splitting on '-' (identifiers may themselves contain hyphens).
export function checkoutMatches(url, game) {
  try {
    const u = new URL(url);
    if (u.protocol !== 'https:' || u.username || u.password || u.port || u.hash ||
        !['www.epicgames.com', 'store.epicgames.com'].includes(u.hostname) ||
        !['/purchase', '/purchase/', '/store/purchase', '/store/purchase/'].includes(u.pathname)) return false;
    const namespaces = u.searchParams.getAll('namespace');
    const offers = u.searchParams.getAll('offers');
    if (offers.length !== 1) return false;
    // Reject mixed representations and duplicate namespace parameters, even
    // when their values agree. Quantity must be exactly one.
    if (namespaces.length === 0) return offers[0] === `1-${game.namespace}-${game.id}`;
    return namespaces.length === 1 && namespaces[0] === game.namespace && offers[0] === game.id;
  } catch { return false; }
}

function result(g, status, message) { return { offer_id: g.id, namespace: g.namespace, status, message }; }
export async function claimGame(driver, g, now = () => Date.now()) {
  let submitted = false;
  try {
    if (!active(g, now())) return result(g, 'failed', 'Promotion is not active.');
    await driver.open(g);
    let block = await driver.blocker();
    if (block) return result(g, block, 'Manual action required.');
    if (await driver.owned(g)) return result(g, 'owned', 'Ownership verified.');
    await driver.get(g);
    block = await driver.blocker();
    if (block) return result(g, block, 'Manual action required.');
    const checkout = await driver.checkout(g);
    if (!checkout || !checkout.identityVerified || checkout.itemCount !== 1) return result(g, 'unsupported', 'Exact checkout offer evidence is unavailable.');
    if (!isNumericZero(checkout.finalTotal) || !(isNumericZero(checkout.tax) || checkout.tax === 'Included')) return result(g, 'price_changed', 'Zero final total including tax not verified.');
    block = await driver.blocker();
    if (block) return result(g, block, 'Manual action required.');
    if (!active(g, now())) return result(g, 'failed', 'Promotion expired before submission.');
    // The driver rechecks the same checkout immediately before the click.
    submitted = true;
    await driver.placeOrder(g, checkout);
    // A thank-you message is insufficient. Reload the product and require its
    // account-specific In Library/Owned purchase control.
    await driver.open(g);
    block = await driver.blocker();
    if (block) return result(g, block, 'Manual action required.');
    if (await driver.owned(g)) return result(g, 'claimed', 'Ownership verified after checkout.');
    return result(g, 'unconfirmed', 'Ownership was not verified.');
  } catch (error) {
    if (!submitted && error?.code === 'UNSUPPORTED_DOM') return result(g, 'unsupported', 'Exact offer evidence is unavailable; manual review is required.');
    return result(g, submitted ? 'unconfirmed' : 'failed', 'Browser operation could not be verified.');
  }
}
