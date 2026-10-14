#!/usr/bin/env node
import { validateRequest, claimGame } from './core.mjs';
import { EpicBrowser } from './browser.mjs';
import { acquireProfile } from './profile.mjs';
let context, lock;
const cleanup = async () => { await context?.close().catch(() => {}); await lock?.release().catch(() => {}); };
for (const signal of ['SIGTERM', 'SIGINT']) process.once(signal, async () => { await cleanup(); process.exit(1); });
try {
  let input = '';
  for await (const chunk of process.stdin) { input += chunk; if (Buffer.byteLength(input) > 1048576) throw Error('Request too large'); }
  const req = validateRequest(JSON.parse(input));
  lock = await acquireProfile(req.account_id);
  const { chromium } = await import('playwright');
  context = await chromium.launchPersistentContext(lock.profile, {
    headless: true, locale: 'en-US', acceptDownloads: false,
    viewport: { width: 1280, height: 900 },
  });
  context.setDefaultTimeout(15000);
  const driver = new EpicBrowser(context);
  const results = [];
  for (const game of req.games) results.push(await claimGame(driver, game));
  process.stdout.write(JSON.stringify(results));
} catch {
  // Never echo page contents, Playwright diagnostics, URLs, cookies or secrets.
  process.stderr.write('Epic worker could not safely complete the request.\n');
  process.exitCode = 1;
} finally { await cleanup(); }
