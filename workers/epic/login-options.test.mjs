import test from 'node:test';
import assert from 'node:assert/strict';
import { loginOptions } from './login-options.mjs';
test('manual login validates account and destination before opening profile', () => {
  assert.deepEqual(loginOptions(['12']), { accountID: 12, destination: 'https://store.epicgames.com/en-US/' });
  assert.equal(loginOptions(['1', 'https://store.epicgames.com/en-US/p/example']).accountID, 1);
  for (const args of [[], ['0'], ['-1'], ['1.2'], ['1e2'], ['9007199254740992'], ['1','https://evil.example'], ['1','http://store.epicgames.com'], ['1','https://a:b@store.epicgames.com'], ['1','https://store.epicgames.com:123'], ['1','https://store.epicgames.com','extra']]) assert.throws(() => loginOptions(args));
});
