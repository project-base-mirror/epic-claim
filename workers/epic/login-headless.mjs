#!/usr/bin/env node
// Headless Epic login driven by the web console. Protocol (JSON lines):
//   stdin  line 1: {"account_id":N,"email":"…","password":"…","totp_secret":"…"}
//   stdin  later : {"code":"123456"}   (only after we emit code_required)
//   stdout       : {"status":"logging_in"|"code_required"|"code_submitted"|"ready"|
//                   "captcha_required"|"two_factor_required"|"login_failed"|"error"}
// Credentials are never written to stdout/stderr, argv or the environment.
// No CAPTCHA solving or stealth: a challenge is reported, never bypassed.
import readline from 'node:readline';
import { acquireProfile } from './profile.mjs';
import { totp } from './totp.mjs';
import { classify, SELECTORS, SUCCESS_URL, loginURL } from './login-flow.mjs';

const emit = status => process.stdout.write(JSON.stringify({ status }) + '\n');
const rl = readline.createInterface({ input: process.stdin });
const lines = rl[Symbol.asyncIterator]();
const nextLine = async ms => {
  let t; const timeout = new Promise(r => { t = setTimeout(() => r({ done: true }), ms); });
  const r = await Promise.race([lines.next(), timeout]); clearTimeout(t);
  return r.done ? null : r.value;
};
const sleep = ms => new Promise(r => setTimeout(r, ms));
let context, lock;
const cleanup = async () => { await context?.close().catch(() => {}); await lock?.release().catch(() => {}); rl.close(); };
for (const s of ['SIGTERM', 'SIGINT']) process.once(s, async () => { await cleanup(); process.exit(1); });

async function main() {
  const first = await nextLine(10000);
  const req = JSON.parse(first || 'null');
  if (!req || !Number.isSafeInteger(req.account_id) || req.account_id <= 0 || typeof req.email !== 'string' || typeof req.password !== 'string') throw Error('bad request');
  lock = await acquireProfile(req.account_id);
  const { chromium } = await import('playwright');
  context = await chromium.launchPersistentContext(lock.profile, {
    headless: process.env.EPIC_LOGIN_HEADFUL !== '1', locale: 'en-US', acceptDownloads: false,
    viewport: { width: 1280, height: 900 },
  });
  context.setDefaultTimeout(15000);
  const page = context.pages()[0] || await context.newPage();
  emit('logging_in');
  await page.goto(loginURL(), { waitUntil: 'domcontentloaded', timeout: 45000 });
  const deadline = Date.now() + 12 * 60 * 1000;
  const done = { email: false, password: false, totp: 0, codes: 0 };
  let unknownSince = Date.now();
  while (Date.now() < deadline) {
    await sleep(1500);
    const state = await classify(page);
    if (state !== 'unknown') unknownSince = Date.now();
    switch (state) {
      case 'ready': return 'ready';
      case 'captcha': return 'captcha_required';
      case 'bad_credentials': return 'login_failed';
      case 'email':
        if (done.email) break;
        await page.locator(SELECTORS.email).first().fill(req.email);
        await page.locator(SELECTORS.continue).first().click().catch(() => page.keyboard.press('Enter'));
        done.email = true; break;
      case 'password':
        if (done.password) break;
        if (!done.email && await page.locator(SELECTORS.email).first().isVisible().catch(() => false)) await page.locator(SELECTORS.email).first().fill(req.email);
        await page.locator(SELECTORS.password).first().fill(req.password);
        await page.locator(SELECTORS.signIn).first().click().catch(() => page.keyboard.press('Enter'));
        done.password = true; break;
      case 'code': {
        let code;
        if (req.totp_secret && done.totp < 2) {
          if (done.totp) await sleep(31000); // retry once in the next 30 s window
          code = totp(req.totp_secret); done.totp++;
        } else {
          if (done.codes >= 3) return 'two_factor_required';
          emit('code_required');
          const line = await nextLine(10 * 60 * 1000);
          if (!line) return 'two_factor_required';
          code = String(JSON.parse(line).code || '');
          if (!/^[A-Za-z0-9]{4,10}$/.test(code)) return 'two_factor_required';
          done.codes++;
        }
        await enterCode(page, code);
        emit('code_submitted');
        await sleep(3000);
        break;
      }
      default:
        if (Date.now() - unknownSince > 60000) return 'error';
    }
  }
  return 'error';
}

async function enterCode(page, code) {
  const split = page.locator(SELECTORS.codeSplit);
  if (await split.count() > 1) {
    await split.first().click();
    await page.keyboard.type(code, { delay: 60 });
  } else {
    await page.locator(SELECTORS.code).first().fill(code);
  }
  const submit = page.locator(SELECTORS.codeSubmit).first();
  if (await submit.isVisible().catch(() => false)) await submit.click().catch(() => {});
  else await page.keyboard.press('Enter');
}

try { emit(await main()); }
catch { process.stderr.write('Epic login worker failed.\n'); emit('error'); process.exitCode = 1; }
finally { await cleanup(); }
