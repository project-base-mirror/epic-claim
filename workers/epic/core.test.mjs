import test from 'node:test';
import assert from 'node:assert/strict';
import { claimGame, validateRequest, checkoutMatches, isNumericZero } from './core.mjs';
const g = { id: 'offer', namespace: 'ns', title: 'Game', url: 'https://store.epicgames.com/p/game', startDate: '2026-01-01T00:00:00Z', endDate: '2027-01-01T00:00:00Z' };
const now = () => Date.parse('2026-10-14T00:00:00Z');
function fake(opts = {}) {
  let opens = 0, clicks = 0;
  return { get clicks() { return clicks; }, open: async () => { opens++; }, blocker: async () => opts.block || null,
    owned: async () => opts.alreadyOwned || (opens > 1 && !opts.unconfirmed), get: async () => {},
    checkout: async () => ({ identityVerified: true, itemCount: 1, finalTotal: '$0.00', tax: '$0.00', ...opts.checkout }),
    placeOrder: async () => { clicks++; if (opts.throwOrder) throw Error('timeout'); },
  };
}
test('only explicit numeric zero accepted', () => {
  for (const amount of ['$0.00', 'USD 0.00', '0', '0,00 EUR', '€0.00']) assert.equal(isNumericZero(amount), true, amount);
  for (const amount of ['Free', '$0.01', '$10.00', '-0', '0.001', '0.00 + tax', '$0.00 $12.00', '', 'zero', '0,000.00', '0e0', 'USD 0 EUR', 'USD 0 USD', '$0.00 USD']) assert.equal(isNumericZero(amount), false, amount);
});
test('request rejects credential fields, unsafe URLs, duplicate IDs, invalid dates', () => {
  assert.doesNotThrow(() => validateRequest({ account_id: 1, games: [g] }));
  for (const request of [ { account_id: 0, games: [g] }, { account_id: 1, password: 'no', games: [g] }, { account_id: 1, games: [g, g] }, { account_id: 1, games: [{ ...g, url: 'https://evil.invalid/p/game' }] }, { account_id: 1, games: [{ ...g, endDate: '' }] } ]) assert.throws(() => validateRequest(request));
});
test('checkout exact origin, namespace and single offer required', () => {
  assert.equal(checkoutMatches('https://www.epicgames.com/purchase?namespace=ns&offers=offer', g), true);
  for (const url of ['https://evil.invalid/purchase?namespace=ns&offers=offer', 'https://www.epicgames.com/purchase?namespace=ns&offers=other', 'https://www.epicgames.com/purchase?namespace=ns&offers=offer&offers=extra', 'https://www.epicgames.com/purchase?offers=offer']) assert.equal(checkoutMatches(url, g), false);
});
test('verified ownership after order is necessary for claimed', async () => {
  for (const [opts, status, clicks] of [[{}, 'claimed', 1], [{ alreadyOwned: true }, 'owned', 0], [{ unconfirmed: true }, 'unconfirmed', 1], [{ throwOrder: true }, 'unconfirmed', 1]]) {
    const d = fake(opts); assert.equal((await claimGame(d, g, now)).status, status); assert.equal(d.clicks, clicks);
  }
});
test('every manual blocker stops without submitting', async () => {
  for (const block of ['login_required', 'captcha_required', 'two_factor_required', 'terms_required']) {
    const d = fake({ block }); assert.equal((await claimGame(d, g, now)).status, block); assert.equal(d.clicks, 0);
  }
});
test('paid, absent or ambiguous totals, tax and identities never submit', async () => {
  for (const checkout of [{ finalTotal: '$0.01' }, { finalTotal: 'Free' }, { finalTotal: '' }, { tax: undefined }, { tax: '$1.00' }, { identityVerified: false }, { itemCount: 2 }]) {
    const d = fake({ checkout }); const r = await claimGame(d, g, now); assert.ok(['failed', 'price_changed', 'unsupported'].includes(r.status)); assert.equal(d.clicks, 0);
  }
});
test('future, expired, and expiring-during-checkout offers never submit', async () => {
  for (const date of ['2025-12-31', '2027-01-01', '2028-01-01']) { const d = fake(); assert.equal((await claimGame(d, g, () => Date.parse(date))).status, 'failed'); assert.equal(d.clicks, 0); }
  const d = fake(); let n = 0;
  assert.equal((await claimGame(d, g, () => ++n === 1 ? now() : Date.parse(g.endDate))).status, 'failed'); assert.equal(d.clicks, 0);
});

test('post-submission blockers never become success', async () => {
  for (const status of ['login_required', 'captcha_required', 'two_factor_required', 'terms_required']) {
    const d = fake();
    d.blocker = async () => d.clicks ? status : null;
    assert.equal((await claimGame(d, g, now)).status, status);
    assert.equal(d.clicks, 1);
  }
});
test('ownership lookup failure after submission remains unconfirmed', async () => {
  const d = fake();
  d.owned = async () => { if (d.clicks) throw Error('network lost'); return false; };
  assert.equal((await claimGame(d, g, now)).status, 'unconfirmed');
  assert.equal(d.clicks, 1);
});

test('unsupported DOM stops pre-submit and remains unconfirmed post-submit', async () => {
  for (const after of [false, true]) {
    const d = fake();
    d.owned = async () => {
      if (!after || d.clicks) { const e = Error('unsupported'); e.code = 'UNSUPPORTED_DOM'; throw e; }
      return false;
    };
    assert.equal((await claimGame(d, g, now)).status, after ? 'unconfirmed' : 'unsupported');
    assert.equal(d.clicks, after ? 1 : 0);
  }
});

test('source-backed packed checkout identity retains exact count and quantity checks', () => {
  const prefix = 'https://www.epicgames.com/store/purchase';
  assert.equal(checkoutMatches(`${prefix}?highlightColor=0078f2&offers=1-ns-offer&orderId&purchaseToken&showNavigation=true`, g), true);
  assert.equal(checkoutMatches(`${prefix}?offers=1-game-ns-base-offer`, {namespace:'game-ns',id:'base-offer'}), true);
  for (const suffix of [
    '?offers=2-ns-offer', '?offers=0-ns-offer', '?offers=01-ns-offer',
    '?offers=1-other-offer', '?offers=1-ns-other', '?offers=1-ns-offer,1-ns-extra',
    '?offers=1-ns-offer&offers=1-ns-extra', '?offers=1-ns-offer&offers=1-ns-offer',
    '?namespace=ns&offers=1-ns-offer', '?namespace=ns&namespace=ns&offers=offer',
    '?namespace=ns&namespace=other&offers=offer', '?offers=1-ns-offer#other',
  ]) assert.equal(checkoutMatches(prefix + suffix, g), false, suffix);
  for (const url of [
    'https://www.epicgames.com.evil.invalid/store/purchase?offers=1-ns-offer',
    'https://user@www.epicgames.com/store/purchase?offers=1-ns-offer',
    'https://www.epicgames.com:8443/store/purchase?offers=1-ns-offer',
    'https://www.epicgames.com/store/purchase/confirm-order?offers=1-ns-offer',
  ]) assert.equal(checkoutMatches(url, g), false, url);
});
