#!/usr/bin/env node
// Operator-only interactive browser. The program never reads/enters credentials,
// codes, CAPTCHA answers or terms acceptance. Close the browser when finished.
import { acquireProfile } from './profile.mjs';
import { loginOptions } from './login-options.mjs';
let lock, context, cleaning;
async function cleanup() {
  // Closing the window and receiving a terminal signal can race. Release once.
  cleaning ??= (async () => {
    await context?.close().catch(() => {});
    await lock?.release().catch(() => {});
  })();
  return cleaning;
}
for (const [signal, code] of [['SIGINT', 130], ['SIGTERM', 143]]) {
  process.once(signal, async () => { await cleanup(); process.exit(code); });
}
try {
  const { accountID, destination } = loginOptions(process.argv.slice(2));
  lock = await acquireProfile(accountID);
  const { chromium } = await import('playwright');
  context = await chromium.launchPersistentContext(lock.profile, { headless: false, locale: 'en-US', acceptDownloads: false });
  const closed = new Promise(resolve => context.once('close', resolve));
  const page = context.pages()[0] || await context.newPage();
  process.stderr.write('Sign in manually. You may navigate to a game and manually resolve checkout challenges or terms. Close the browser when finished; then rerun the account claim.\n');
  await page.goto(destination, { waitUntil: 'domcontentloaded', timeout: 45000 });
  await closed;
} catch {
  process.stderr.write('Could not open the account browser. Usage: node login.mjs <positive-account-id> [https://store.epicgames.com/...]. Check the display and profile lock.\n');
  process.exitCode = 1;
} finally { await cleanup(); }
