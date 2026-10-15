#!/usr/bin/env node
// 测试连接: launches the plain browser with the effective route (env) on a throwaway
// profile, reads the exit IP and whether Epic's login page gets past Cloudflare.
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { PlainBrowser } from './plain-browser.mjs';
const sleep = ms => new Promise(r => setTimeout(r, ms));
const prof = await fs.mkdtemp(path.join(os.tmpdir(), 'nettest-'));
const { chromium } = await import('playwright');
const pb = new PlainBrowser({ executable: chromium.executablePath(), profile: prof, url: 'https://ipinfo.io/json' });
const out = { ip: '', country: '', city: '', cloudflare: 'unknown', title: '' };
try {
  await pb.start();
  for (let i = 0; i < 20 && !out.ip; i++) {
    await sleep(1000);
    const t = await pb.eval('document.body ? document.body.innerText : ""').catch(() => '');
    try { const j = JSON.parse(t); Object.assign(out, { ip: String(j.ip || ''), country: String(j.country || ''), city: String(j.city || '') }); } catch {}
  }
  await pb.navigate('https://www.epicgames.com/id/login?lang=en-US');
  const end = Date.now() + 35000;
  let sawCF = false;
  while (Date.now() < end) {
    await sleep(1500);
    const s = await pb.eval(`({t:document.title,cf:/^just a moment/i.test(document.title)||!!document.querySelector('input[name="cf-turnstile-response"]')||/one more step/i.test(document.body?document.body.innerText:''),email:!!document.querySelector('input#email,input[type=email]')})`).catch(() => null);
    if (!s) continue;
    out.title = String(s.t).slice(0, 80);
    if (s.cf) sawCF = true;
    if (s.email && !s.cf) { out.cloudflare = sawCF ? 'passed_after_check' : 'passed'; break; }
    out.cloudflare = s.cf ? 'stuck' : 'unknown';
  }
} catch { out.cloudflare = 'error'; }
finally { await pb.stop().catch(() => {}); await fs.rm(prof, { recursive: true, force: true }).catch(() => {}); }
process.stdout.write(JSON.stringify(out));
