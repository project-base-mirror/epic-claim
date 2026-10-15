import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { acquireProfile } from './profile.mjs';

test('profile lock: live holder blocks, stale/foreign/legacy locks are reclaimed', async () => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'prof-'));
  process.env.EPIC_PROFILE_ROOT = root;
  const a = await acquireProfile(1);
  await assert.rejects(acquireProfile(1), e => e.code === 'PROFILE_BUSY');
  await a.release();
  // Lock left by a recreated container (other hostname) + Chromium singleton from that host.
  await fs.mkdir(path.join(root, 'account-1.lock'));
  await fs.writeFile(path.join(root, 'account-1.lock', 'owner'), 'old-container 158\n');
  await fs.symlink('old-container-158', path.join(root, 'account-1', 'SingletonLock'));
  const b = await acquireProfile(1);
  await assert.rejects(fs.lstat(path.join(root, 'account-1', 'SingletonLock')));
  await b.release();
  // Same host, dead pid.
  await fs.mkdir(path.join(root, 'account-1.lock'));
  await fs.writeFile(path.join(root, 'account-1.lock', 'owner'), `${os.hostname()} 2147483646\n`);
  await (await acquireProfile(1)).release();
  // Legacy lock without owner: fresh = busy, old = stale.
  await fs.mkdir(path.join(root, 'account-1.lock'));
  await assert.rejects(acquireProfile(1), e => e.code === 'PROFILE_BUSY');
  const old = new Date(Date.now() - 20 * 60 * 1000);
  await fs.utimes(path.join(root, 'account-1.lock'), old, old);
  await (await acquireProfile(1)).release();
  await fs.rm(root, { recursive: true, force: true });
});
