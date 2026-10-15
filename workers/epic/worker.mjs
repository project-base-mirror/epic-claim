#!/usr/bin/env node
// Scheduled claim worker. With an X display (xvfb-run) it uses the plain browser
// (no automation flags; DevTools only reads state; clicks are real X input).
import { validateRequest, claimGame } from './core.mjs';
import { acquireProfile } from './profile.mjs';
let context, lock, plain;
const cleanup = async () => { await plain?.stop().catch(() => {}); await context?.close().catch(() => {}); await lock?.release().catch(() => {}); };
for (const signal of ['SIGTERM', 'SIGINT']) process.once(signal, async () => { await cleanup(); process.exit(1); });
try {
  let input = '';
  for await (const chunk of process.stdin) { input += chunk; if (Buffer.byteLength(input) > 1048576) throw Error('Request too large'); }
  const req = validateRequest(JSON.parse(input));
  lock = await acquireProfile(req.account_id);
  const { chromium } = await import('playwright');
  const results = [];
  if (process.env.DISPLAY) {
    const { PlainBrowser } = await import('./plain-browser.mjs');
    const { PlainDriver } = await import('./plain-claim.mjs');
    plain = new PlainBrowser({ executable: chromium.executablePath(), profile: lock.profile, url: 'https://store.epicgames.com/en-US/' });
    await plain.start();
    await new Promise(r => setTimeout(r, 4000));
    const driver = new PlainDriver(plain, { log: m => process.stderr.write(m.slice(0, 80) + '\n') });
    for (const game of req.games) results.push(await claimGame(driver, game));
  } else {
    const { EpicBrowser } = await import('./browser.mjs');
    context = await chromium.launchPersistentContext(lock.profile, { headless: true, locale: 'en-US', acceptDownloads: false, viewport: { width: 1280, height: 900 } });
    context.setDefaultTimeout(15000);
    const driver = new EpicBrowser(context);
    for (const game of req.games) results.push(await claimGame(driver, game));
  }
  process.stdout.write(JSON.stringify(results));
} catch (e) {
  // Never echo page contents, URLs, cookies or secrets.
  process.stderr.write(`Epic worker could not safely complete the request: ${String(e?.name || '')} ${String(e?.code || '')}\n`);
  process.exitCode = 1;
} finally { await cleanup(); }
