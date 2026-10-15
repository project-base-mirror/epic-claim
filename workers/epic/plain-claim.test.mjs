import test from 'node:test';
import assert from 'node:assert/strict';
import { zeroTotal, productURL } from './plain-claim.mjs';
test('zero total gate', () => {
  for (const t of ['$0.00', 'Free', 'US$0.00', '€0,00', '0']) assert.ok(zeroTotal(t), t);
  for (const t of ['$14.99', '$0.01', '', '¥10', 'Total']) assert.ok(!zeroTotal(t), t);
});
test('product url is normalised to en-US', () => {
  assert.equal(productURL({ url: 'https://store.epicgames.com/p/out-of-sight-b96ca8' }), 'https://store.epicgames.com/en-US/p/out-of-sight-b96ca8');
});
