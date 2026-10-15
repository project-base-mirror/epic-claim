// End-to-end test of login-headless.mjs against a LOCAL fake login page (not Epic).
// Needs a Playwright Chromium; skipped when unavailable. Run: node --test login-headless.e2e.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { spawn } from 'node:child_process';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

const page = body => `<!doctype html><html><body><form method="post">${body}</form></body></html>`;
function server() {
  return http.createServer((req, res) => {
    let data = '';
    req.on('data', c => { data += c; });
    req.on('end', () => {
      const f = new URLSearchParams(data);
      res.setHeader('Content-Type', 'text/html');
      if (req.url.startsWith('/cf')) return res.end('<html><head><title>Just a moment...</title></head><body>One more step<input type="hidden" name="cf-turnstile-response"></body></html>');
      if (req.url.startsWith('/account')) return res.end('<h1>Account</h1>');
      if (f.get('email') && !f.get('password')) return res.end(page(`<input type="hidden" name="email" value="${f.get('email')}"><input id="password" name="password" type="password"><button id="sign-in" type="submit">Sign in</button>`));
      if (f.get('password') === 'wrong') return res.end(page(`<div role="alert">Sorry the credentials you are using are invalid.</div>`));
      if (f.get('password')) return res.end(page(`<input id="code" name="code"><button id="continue" type="submit">Continue</button>`));
      if (f.get('code') === '424242') { res.statusCode = 303; res.setHeader('Location', '/account/personal'); return res.end(); }
      if (f.get('code')) return res.end(page(`<div role="alert">Invalid code</div>`));
      res.end(page(`<input id="email" name="email" type="email"><button id="continue" type="submit">Continue</button>`));
    });
  });
}
async function run(creds, onStatus, start = '/login') {
  const srv = server(); await new Promise(r => srv.listen(0, '127.0.0.1', r));
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'epic-login-'));
  const child = spawn(process.execPath, [new URL('./login-headless.mjs', import.meta.url).pathname], {
    env: { ...process.env, EPIC_PROFILE_ROOT: root, EPIC_LOGIN_URL_FOR_TESTS: `http://127.0.0.1:${srv.address().port}${start}` }, stdio: ['pipe', 'pipe', 'ignore'] });
  child.stdin.write(JSON.stringify({ account_id: 1, ...creds }) + '\n');
  const statuses = []; let buf = '';
  child.stdout.on('data', d => { buf += d; let i; while ((i = buf.indexOf('\n')) >= 0) { const s = JSON.parse(buf.slice(0, i)).status; buf = buf.slice(i + 1); statuses.push(s); onStatus?.(s, child); } });
  await new Promise(r => child.on('exit', r));
  srv.close(); await fs.rm(root, { recursive: true, force: true });
  return statuses;
}
let hasBrowser = true;
try { const { chromium } = await import('playwright'); await fs.access(chromium.executablePath()); } catch { hasBrowser = false; }

test('email → password → emailed code → ready', { skip: !hasBrowser && 'no Playwright browser', timeout: 120000 }, async () => {
  const s = await run({ email: 'user@example.com', password: 'right' }, (st, child) => { if (st === 'code_required') child.stdin.write('{"code":"424242"}\n'); });
  assert.deepEqual([s[0], s.at(-1)], ['logging_in', 'ready']);
  assert.ok(s.includes('code_required') && s.includes('code_submitted'));
});
test('wrong password is reported', { skip: !hasBrowser && 'no Playwright browser', timeout: 120000 }, async () => {
  const s = await run({ email: 'user@example.com', password: 'wrong' });
  assert.equal(s.at(-1), 'login_failed');
});
test('Cloudflare challenge is reported as captcha, not bypassed', { skip: !hasBrowser && 'no Playwright browser', timeout: 120000 }, async () => {
  const s = await run({ email: 'user@example.com', password: 'right' }, undefined, '/cf');
  assert.equal(s.at(-1), 'captcha_required');
});
