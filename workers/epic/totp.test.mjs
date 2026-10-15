import test from 'node:test';
import assert from 'node:assert/strict';
import { totp, base32Decode } from './totp.mjs';
test('RFC 6238 SHA-1 vectors', () => {
  // Secret "12345678901234567890" in base32.
  const s = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ';
  assert.equal(base32Decode(s).toString(), '12345678901234567890');
  assert.equal(totp(s, 59000, 30, 8), '94287082');
  assert.equal(totp(s, 1111111109000, 30, 8), '07081804');
  assert.equal(totp(s, 20000000000000, 30, 8), '65353130');
});
