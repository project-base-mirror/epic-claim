import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

// A lock is stale when its owner process is gone: a different hostname means
// the container was recreated (single-instance deployment), a dead PID means
// the worker was killed. Legacy locks without an owner file count as stale
// after 15 minutes (longer than any worker timeout).
async function lockIsStale(lock) {
  let owner;
  try { owner = (await fs.readFile(path.join(lock, 'owner'), 'utf8')).trim(); } catch {
    const st = await fs.stat(lock).catch(() => null);
    return !st || Date.now() - st.mtimeMs > 15 * 60 * 1000;
  }
  const [host, pidText] = owner.split(' ');
  if (host !== os.hostname()) return true;
  const pid = Number(pidText);
  if (!Number.isSafeInteger(pid) || pid <= 0) return true;
  try { process.kill(pid, 0); return false; } catch (e) { return e.code === 'ESRCH'; }
}

// Chromium's own Singleton* links name "<host>-<pid>". After a container
// restart they point at a host that no longer exists and Chromium refuses the
// profile, so remove them once we hold our lock (no Chromium can be using it).
async function clearChromiumSingletons(profile) {
  for (const f of ['SingletonLock', 'SingletonCookie', 'SingletonSocket']) await fs.rm(path.join(profile, f), { force: true }).catch(() => {});
}

export class ProfileBusyError extends Error { constructor() { super('Profile is in use'); this.code = 'PROFILE_BUSY'; } }

export async function acquireProfile(accountID) {
  if (!Number.isSafeInteger(accountID) || accountID <= 0) throw Error('Invalid account ID');
  const root = path.resolve(process.env.EPIC_PROFILE_ROOT || './data/profiles');
  await fs.mkdir(root, { recursive: true, mode: 0o700 });
  if ((await fs.lstat(root)).isSymbolicLink()) throw Error('Profile root must not be a symlink');
  await fs.chmod(root, 0o700);
  const profile = path.join(root, `account-${accountID}`);
  await fs.mkdir(profile, { mode: 0o700 }).catch(e => { if (e.code !== 'EEXIST') throw e; });
  const st = await fs.lstat(profile);
  if (!st.isDirectory() || st.isSymbolicLink()) throw Error('Unsafe profile directory');
  await fs.chmod(profile, 0o700);
  const lock = `${profile}.lock`;
  // Interactive login and scheduled claims share this lock; a live holder is never displaced.
  for (let attempt = 0; ; attempt++) {
    try { await fs.mkdir(lock, { mode: 0o700 }); break; } catch (e) {
      if (e.code !== 'EEXIST' || attempt > 0 || !(await lockIsStale(lock))) throw e.code === 'EEXIST' ? new ProfileBusyError() : e;
      await fs.rm(lock, { recursive: true, force: true });
    }
  }
  await fs.writeFile(path.join(lock, 'owner'), `${os.hostname()} ${process.pid}\n`, { mode: 0o600 });
  await clearChromiumSingletons(profile);
  return { profile, release: () => fs.rm(lock, { recursive: true, force: true }) };
}
