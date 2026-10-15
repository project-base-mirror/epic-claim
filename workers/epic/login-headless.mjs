#!/usr/bin/env node
// Epic login driven by the web console. Runs a real (headed) Chromium under
// Xvfb when DISPLAY is set, otherwise headless. Protocol (JSON lines):
//   stdin  line 1: {"account_id":N,"email":"…","password":"…","totp_secret":"…","interactive":bool}
//   stdin  later : {"code":"123456"} | {"input":{…}} | {"done":true} | {"cancel":true}
//   stdout       : {"status":"…"}  and  {"frame":"<base64 jpeg>"} (live view)
// Credentials never reach stdout/stderr/argv/env. Challenges (Cloudflare,
// hCaptcha…) are never solved automatically: the user solves them in the live view.
import readline from 'node:readline';
import { acquireProfile } from './profile.mjs';
import { totp } from './totp.mjs';
import { classify, SELECTORS, SUCCESS_URL, loginURL, isSuccessURL } from './login-flow.mjs';
import { PlainBrowser } from './plain-browser.mjs';

const W = 1280, H = 860;
const out = o => process.stdout.write(JSON.stringify(o) + '\n');
const emit = status => out({ status });
const sleep = ms => new Promise(r => setTimeout(r, ms));
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
let firstResolve; const firstLine = new Promise(r => { firstResolve = r; });
const codes = []; let codeWaiter = null;
const control = { done: false, cancel: false };
let page, context, lock, cdp, streaming = false, plain = null, creds = {};
rl.on('line', line => {
  if (firstResolve) { const f = firstResolve; firstResolve = null; return f(line); }
  let m; try { m = JSON.parse(line); } catch { return; }
  if (m.done) control.done = true;
  else if (m.cancel) control.cancel = true;
  else if (typeof m.code === 'string') { codes.push(m.code); codeWaiter?.(); }
  else if (m.input) handleInput(m.input).catch(() => {});
  else if (m.fill === 'email' && plain && creds.email) plain.typeSecret(creds.email);
  else if (m.fill === 'password' && plain && creds.password) plain.typeSecret(creds.password);
});
rl.on('close', () => { control.cancel = true; firstResolve?.(null); });

const cleanup = async () => { await plain?.stop().catch(() => {}); await context?.close().catch(() => {}); await lock?.release().catch(() => {}); rl.close(); };
for (const s of ['SIGTERM', 'SIGINT']) process.once(s, async () => { await cleanup(); process.exit(1); });

// ---- live view (CDP screencast) and input forwarding ----
async function attach(p) {
  if (page === p && cdp) return;
  await cdp?.send('Page.stopScreencast').catch(() => {});
  await cdp?.detach().catch(() => {});
  page = p; cdp = null;
  if (!streaming) return;
  cdp = await context.newCDPSession(p);
  cdp.on('Page.screencastFrame', f => { out({ frame: f.data }); cdp?.send('Page.screencastFrameAck', { sessionId: f.sessionId }).catch(() => {}); });
  await cdp.send('Page.startScreencast', { format: 'jpeg', quality: 60, maxWidth: W, maxHeight: H, everyNthFrame: 1 });
}
async function startStream() { if (streaming) return; streaming = true; const p = page; page = null; await attach(p); }
const SPECIAL = new Set(['Enter', 'Backspace', 'Tab', 'Escape', 'Delete', 'ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End', 'PageUp', 'PageDown', ' ']);
async function handleInput(i) {
  if (plain) return plain.input(i);
  if (!page || !streaming) return;
  // Clients send coordinates as fractions of the displayed frame (0..1).
  const x = Math.max(0, Math.min(1, Number(i.x) || 0)) * W, y = Math.max(0, Math.min(1, Number(i.y) || 0)) * H;
  switch (i.type) {
    case 'move': return page.mouse.move(x, y);
    case 'down': await page.mouse.move(x, y); return page.mouse.down();
    case 'up': await page.mouse.move(x, y); return page.mouse.up();
    case 'click': return page.mouse.click(x, y);
    case 'wheel': return page.mouse.wheel(0, Math.max(-2000, Math.min(2000, Number(i.dy) || 0)));
    case 'key': {
      const k = String(i.key || '');
      if (SPECIAL.has(k)) return page.keyboard.press(k === ' ' ? 'Space' : k);
      if ((i.ctrl || i.meta) && /^[acvxz]$/i.test(k)) return page.keyboard.press('Control+' + k.toLowerCase());
      if ([...k].length === 1) return page.keyboard.insertText(k);
      return;
    }
    case 'text': return page.keyboard.insertText(String(i.text || '').slice(0, 512));
  }
}

// Hand over to the plain browser: close the automated one first (same profile).
async function switchToPlain(chromium) {
  await cdp?.detach().catch(() => {}); cdp = null; streaming = false;
  await context.close().catch(() => {}); context = null; page = null;
  emit('challenge');
  return plainSession(chromium);
}

async function enterCode(code) {
  const split = page.locator(SELECTORS.codeSplit);
  if (await split.count() > 1) { await split.first().click(); await page.keyboard.type(code, { delay: 60 }); }
  else await page.locator(SELECTORS.code).first().fill(code);
  const submit = page.locator(SELECTORS.codeSubmit).first();
  if (await submit.isVisible().catch(() => false)) await submit.click().catch(() => {});
  else await page.keyboard.press('Enter');
}

async function verifyLoggedIn() {
  if (isSuccessURL(page.url())) return true;
  await page.goto(process.env.EPIC_LOGIN_URL_FOR_TESTS ? new URL('/account/personal', loginURL()).href : SUCCESS_URL, { waitUntil: 'domcontentloaded', timeout: 45000 }).catch(() => {});
  await sleep(3000);
  return isSuccessURL(page.url());
}

// Read-only check after the user is done: a short headless visit with the saved profile.
async function checkLoggedIn(chromium, profile) {
  const ctx = await chromium.launchPersistentContext(profile, { headless: true, locale: 'en-US' });
  try {
    const p = ctx.pages()[0] || await ctx.newPage();
    await p.goto(process.env.EPIC_LOGIN_URL_FOR_TESTS ? new URL('/account/personal', loginURL()).href : SUCCESS_URL, { waitUntil: 'domcontentloaded', timeout: 45000 }).catch(() => {});
    await sleep(3000);
    return isSuccessURL(p.url());
  } finally { await ctx.close().catch(() => {}); }
}

const LOGGED_IN_TITLE = /account\/personal|Personal Details|Account Settings|个人信息|账户设置/i;
async function plainSession(chromium) {
  if (!process.env.DISPLAY) throw Object.assign(Error('no display'), { code: 'NO_DISPLAY' });
  const start = () => { plain = new PlainBrowser({ executable: chromium.executablePath(), profile: lock.profile, url: loginURL(), onFrame: f => out({ frame: f.toString('base64') }) }); plain.start(); };
  start();
  const deadline = Date.now() + 10 * 60 * 1000;
  let lastTitle = 0, restarts = 0;
  while (true) {
    await sleep(1000);
    if (control.cancel) return 'cancelled';
    if (Date.now() > deadline) return 'timeout';
    if (plain.chrome.exitCode !== null) { if (++restarts > 2) return 'error'; await plain.stop(); start(); continue; }
    let auto = false;
    if (Date.now() - lastTitle > 3000) { lastTitle = Date.now(); const t = await plain.title(); auto = LOGGED_IN_TITLE.test(t) && !/sign in|登录/i.test(t); }
    if (control.done || auto) {
      control.done = false;
      emit('checking');
      await plain.stop();
      if (await checkLoggedIn(chromium, lock.profile)) { plain = null; return 'ready'; }
      emit('not_logged_in');
      start();
    }
  }
}

async function main() {
  const req = JSON.parse(await firstLine || 'null');
  if (!req || !Number.isSafeInteger(req.account_id) || req.account_id <= 0) throw Error('bad request');
  const email = typeof req.email === 'string' ? req.email : '', password = typeof req.password === 'string' ? req.password : '';
  creds = { email, password };
  lock = await acquireProfile(req.account_id);
  const { chromium } = await import('playwright');
  // Manual login / 去验证: a plain browser with no automation attached.
  if (req.interactive && process.env.DISPLAY) { emit('interactive'); return await plainSession(chromium); }
  const headed = !!process.env.DISPLAY && process.env.EPIC_LOGIN_HEADLESS !== '1';
  context = await chromium.launchPersistentContext(lock.profile, {
    headless: !headed, locale: 'en-US', acceptDownloads: false, viewport: { width: W, height: H },
    args: headed ? [`--window-size=${W},${H + 100}`] : [],
  });
  context.setDefaultTimeout(15000);
  context.on('page', p => { attach(p).catch(() => {}); });
  await attach(context.pages()[0] || await context.newPage());
  if (req.interactive) { await startStream(); emit('interactive'); } else emit('logging_in');
  await page.goto(loginURL(), { waitUntil: 'domcontentloaded', timeout: 45000 });
  // Total budget ~13 min; a challenge/manual session gets 10 min of user time.
  const deadline = Date.now() + 13 * 60 * 1000;
  let userDeadline = req.interactive ? Date.now() + 10 * 60 * 1000 : 0;
  const done = { email: false, password: false, totp: 0, codes: 0 };
  let unknownSince = Date.now();
  while (Date.now() < deadline) {
    await sleep(1000);
    if (control.cancel) return 'cancelled';
    if (userDeadline && Date.now() > userDeadline) return 'timeout';
    if (control.done) { control.done = false; if (await verifyLoggedIn()) return 'ready'; emit('not_logged_in'); continue; }
    const live = context.pages().filter(p => !p.isClosed());
    if (!live.includes(page) && live.length) await attach(live.at(-1));
    const state = await classify(page);
    if (state !== 'unknown') unknownSince = Date.now();
    switch (state) {
      case 'ready': return 'ready';
      case 'captcha':
        if (process.env.DISPLAY) return await switchToPlain(chromium);
        if (!streaming) { await startStream(); userDeadline = Date.now() + 10 * 60 * 1000; emit('challenge'); }
        break; // the user solves it in the live view; we keep watching
      case 'bad_credentials': if (!streaming) return 'login_failed'; break;
      case 'email':
        if (done.email || !email) break;
        await page.locator(SELECTORS.email).first().fill(email);
        if (password || !streaming) await page.locator(SELECTORS.continue).first().click().catch(() => page.keyboard.press('Enter'));
        done.email = true; break;
      case 'password':
        if (done.password || !password) break;
        if (email && await page.locator(SELECTORS.email).first().isVisible().catch(() => false)) await page.locator(SELECTORS.email).first().fill(email);
        await page.locator(SELECTORS.password).first().fill(password);
        await page.locator(SELECTORS.signIn).first().click().catch(() => page.keyboard.press('Enter'));
        done.password = true; break;
      case 'code': {
        if (req.totp_secret && done.totp < 2) {
          if (done.totp) await sleep(31000);
          done.totp++; await enterCode(totp(req.totp_secret)); emit('code_submitted'); await sleep(3000); break;
        }
        if (done.codes >= 3) { if (!streaming) return 'two_factor_required'; break; }
        emit('code_required');
        const until = Date.now() + 10 * 60 * 1000;
        // In the live view the user may also type the code directly into the page.
        while (!codes.length && Date.now() < until && !control.cancel && !control.done) {
          await Promise.race([new Promise(r => { codeWaiter = r; }), sleep(2000)]);
          if (streaming && await classify(page) !== 'code') break;
        }
        codeWaiter = null;
        if (control.cancel) return 'cancelled';
        if (!codes.length) { if (Date.now() >= until) return 'timeout'; break; }
        const code = codes.shift();
        if (!/^[A-Za-z0-9]{4,10}$/.test(code)) break;
        done.codes++; await enterCode(code); emit('code_submitted'); await sleep(3000);
        break;
      }
      default:
        if (process.env.DISPLAY && Date.now() - unknownSince > 60000) return await switchToPlain(chromium);
        if (!streaming && Date.now() - unknownSince > 60000) { await startStream(); userDeadline = Date.now() + 10 * 60 * 1000; emit('challenge'); }
    }
  }
  return 'timeout';
}

try { emit(await main()); }
catch (e) {
  // Diagnostics without page content, URLs or credentials: error class/code only.
  const busy = e?.code === 'PROFILE_BUSY';
  process.stderr.write(`login worker failed: ${String(e?.name || 'Error').slice(0, 40)} ${String(e?.code || '').slice(0, 40)}${/Target page, context or browser has been closed|Executable doesn't exist|Missing X server|ECONNREFUSED|ERR_[A-Z_]+/.exec(String(e?.message))?.[0] ? ' ' + /Target page, context or browser has been closed|Executable doesn't exist|Missing X server|ECONNREFUSED|ERR_[A-Z_]+/.exec(String(e?.message))[0] : ''}\n`);
  emit(busy ? 'profile_busy' : 'error'); process.exitCode = 1;
}
finally { await cleanup(); }
