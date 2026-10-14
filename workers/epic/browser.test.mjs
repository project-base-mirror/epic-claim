import test from 'node:test';
import assert from 'node:assert/strict';
import { EpicBrowser } from './browser.mjs';
const game = { startDate: new Date(Date.now() - 60000).toISOString(), endDate: new Date(Date.now() + 60000).toISOString() };
const zero = { identityVerified: true, itemCount: 1, finalTotal: '$0.00', tax: '$0.00' };
function fixture(fresh = zero, block = null) {
  let clicks = 0;
  const d = new EpicBrowser({ pages: () => [] });
  d.blocker = async () => block;
  d.checkout = async () => fresh;
  d.page = { waitForTimeout: async () => {} };
  d.checkoutFrame = { getByRole: () => ({ all: async () => [{ isVisible: async () => true, click: async () => { clicks++; } }] }) };
  return { d, clicks: () => clicks };
}
test('driver rechecks price, identity, terms and expiry immediately before click', async () => {
  for (const fresh of [null, { ...zero, finalTotal: '$1.00' }, { ...zero, tax: '$1.00' }, { ...zero, itemCount: 2 }, { ...zero, identityVerified: false }]) {
    const f = fixture(fresh); await assert.rejects(() => f.d.placeOrder(game, zero)); assert.equal(f.clicks(), 0);
  }
  for (const block of ['captcha_required', 'terms_required', 'login_required', 'two_factor_required']) {
    const f = fixture(zero, block); await assert.rejects(() => f.d.placeOrder(game, zero)); assert.equal(f.clicks(), 0);
  }
  const f = fixture(); await assert.rejects(() => f.d.placeOrder({ ...game, endDate: new Date(0).toISOString() }, zero)); assert.equal(f.clicks(), 0);
});
test('driver permits only unchanged verified zero checkout', async () => {
  const f = fixture(); await f.d.placeOrder(game, zero); assert.equal(f.clicks(), 1);
});

test('checkout action labels include the evidenced instant-free flow only', async () => {
  const { CHECKOUT_ACTION } = await import('./browser.mjs');
  for (const name of ['Place Order', 'PLACE ORDER', 'Add to library', 'ADD TO LIBRARY']) assert.equal(CHECKOUT_ACTION.test(name), true);
  for (const name of ['Buy Now', 'Purchase', 'Get', 'Add to library for $1', 'Confirm']) assert.equal(CHECKOUT_ACTION.test(name), false);
  const f = fixture();
  f.d.checkoutFrame.getByRole = (role, {name}) => {
    assert.equal(role, 'button'); assert.equal(name.test('Add to library'), true);
    return {all: async () => [{isVisible: async () => true, click: async () => {}}]};
  };
  await f.d.placeOrder(game, zero);
});
