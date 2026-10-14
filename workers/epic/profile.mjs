import fs from 'node:fs/promises';
import path from 'node:path';
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
  // Both interactive login and scheduled claims acquire the same lock. A stale
  // lock requires operator inspection; never steal a possibly live session.
  await fs.mkdir(lock, { mode: 0o700 });
  return { profile, release: () => fs.rmdir(lock) };
}
