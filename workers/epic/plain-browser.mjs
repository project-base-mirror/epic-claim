// A plain, un-instrumented Chromium for the user's manual login: launched as an
// ordinary process (no Playwright/CDP attached, no --enable-automation, so
// navigator.webdriver is false), shown on the private Xvfb display, streamed by
// grabbing the X framebuffer (ffmpeg x11grab) and driven by X input (xdotool).
// The user solves any challenge themselves; nothing here solves captchas.
import { spawn } from 'node:child_process';
import fs from 'node:fs/promises';
import path from 'node:path';
import * as cdp from './cdp.mjs';

export const SCREEN = { w: 1280, h: 960 };
const XKEYS = { Enter: 'Return', Backspace: 'BackSpace', Tab: 'Tab', Escape: 'Escape', Delete: 'Delete', ArrowLeft: 'Left', ArrowRight: 'Right', ArrowUp: 'Up', ArrowDown: 'Down', Home: 'Home', End: 'End', PageUp: 'Prior', PageDown: 'Next', ' ': 'space' };

export function browserArgs(profile, url, env = process.env) {
  const args = [
    `--user-data-dir=${profile}`, '--no-first-run', '--no-default-browser-check', '--password-store=basic',
    '--disable-features=Translate,MediaRouter', '--window-position=0,0', `--window-size=${SCREEN.w},${SCREEN.h}`,
    `--lang=${env.EPIC_BROWSER_LANG || 'zh-CN'}`,
    // Containers have no user namespaces for Chromium's sandbox (same as Playwright's default);
    // --test-type only hides the resulting "unsupported flag" infobar. Pages cannot observe either.
    '--no-sandbox', '--test-type',
    // Local-only DevTools endpoint for reading page state/cookies (port picked by Chromium,
    // written to <profile>/DevToolsActivePort). Never exposed outside the container.
    '--remote-debugging-address=127.0.0.1', '--remote-debugging-port=0',
  ];
  // Default: direct (the proxy exit IP gets stuck on Cloudflare). 'env' = use HTTP(S)_PROXY.
  const proxy = env.EPIC_BROWSER_PROXY || 'direct';
  if (proxy === 'direct') args.push('--no-proxy-server');
  else if (/^(https?|socks5):\/\/[^\s]+$/.test(proxy)) args.push(`--proxy-server=${proxy}`);
  args.push(url);
  return args;
}

export function browserEnv(env = process.env) {
  const out = { ...env, GOOGLE_API_KEY: 'no', GOOGLE_DEFAULT_CLIENT_ID: 'no', GOOGLE_DEFAULT_CLIENT_SECRET: 'no' };
  out.TZ = env.EPIC_BROWSER_TZ || 'Asia/Shanghai';
  if ((env.EPIC_BROWSER_PROXY || 'direct') === 'direct') for (const k of ['HTTP_PROXY', 'HTTPS_PROXY', 'http_proxy', 'https_proxy', 'ALL_PROXY', 'all_proxy']) delete out[k];
  return out;
}

// Split an MJPEG byte stream into JPEG images (0xFFD8 … 0xFFD9; 0xFF is stuffed inside scan data).
export function jpegSplitter(onFrame) {
  let buf = Buffer.alloc(0);
  return chunk => {
    buf = Buffer.concat([buf, chunk]);
    for (;;) {
      const s = buf.indexOf(Buffer.from([0xff, 0xd8]));
      if (s < 0) { buf = Buffer.alloc(0); return; }
      const e = buf.indexOf(Buffer.from([0xff, 0xd9]), s + 2);
      if (e < 0) { buf = buf.subarray(s); if (buf.length > 8 << 20) buf = Buffer.alloc(0); return; }
      onFrame(buf.subarray(s, e + 2));
      buf = buf.subarray(e + 2);
    }
  };
}

export function xdotoolCommands(i) {
  const x = Math.round(Math.max(0, Math.min(1, Number(i.x) || 0)) * (SCREEN.w - 1));
  const y = Math.round(Math.max(0, Math.min(1, Number(i.y) || 0)) * (SCREEN.h - 1));
  switch (i.type) {
    case 'move': return [['mousemove', String(x), String(y)]];
    case 'down': return [['mousemove', String(x), String(y), 'mousedown', '1']];
    case 'up': return [['mousemove', String(x), String(y), 'mouseup', '1']];
    case 'click': return [['mousemove', String(x), String(y), 'click', '1']];
    case 'wheel': { const n = Math.min(10, Math.max(1, Math.round(Math.abs(Number(i.dy) || 0) / 100))); return [['click', '--repeat', String(n), (Number(i.dy) || 0) > 0 ? '5' : '4']]; }
    case 'key': {
      const k = String(i.key || '');
      if (XKEYS[k]) return [['key', XKEYS[k]]];
      if ((i.ctrl || i.meta) && /^[acvxz]$/i.test(k)) return [['key', 'ctrl+' + k.toLowerCase()]];
      if ([...k].length === 1) return [['type', '--file', '-', k]];
      return [];
    }
    case 'text': return [['type', '--file', '-', String(i.text || '').slice(0, 512)]];
  }
  return [];
}

export class PlainBrowser {
  constructor({ executable, profile, url, onFrame }) { Object.assign(this, { executable, profile, url, onFrame }); this.queue = Promise.resolve(); }
  // Epic's login cookie (EPIC_SESSION_AP …) is a session cookie that Chromium does not
  // write to disk. We keep the account's cookies in <profile>/epic-session.json (0600,
  // same sensitivity as the profile itself) and restore them on the next launch.
  get sessionFile() { return path.join(this.profile, 'epic-session.json'); }
  async saveSession() {
    try {
      const port = await cdp.devtoolsPort(this.profile, 3000);
      const { cookies } = await cdp.call(await cdp.browserWs(port), 'Storage.getCookies');
      const keep = cookies.filter(c => /(^|\.)epicgames\.com$/.test(c.domain.replace(/^\./, '')) || /epicgames\.com$/.test(c.domain));
      await fs.writeFile(this.sessionFile, JSON.stringify(keep), { mode: 0o600 });
      return keep.length;
    } catch { return 0; }
  }
  async restoreSession() {
    try {
      const saved = JSON.parse(await fs.readFile(this.sessionFile, 'utf8'));
      if (!Array.isArray(saved) || !saved.length) return 0;
      const port = await cdp.devtoolsPort(this.profile);
      const cookies = saved.map(({ name, value, domain, path, secure, httpOnly, sameSite, expires }) => ({ name, value, domain, path, secure, httpOnly, sameSite, ...(expires > 0 ? { expires } : {}) }));
      await cdp.call(await cdp.browserWs(port), 'Storage.setCookies', { cookies });
      return cookies.length;
    } catch { return 0; }
  }
  async reloadPage() { const t = await this.page(); return cdp.call(t.webSocketDebuggerUrl, 'Page.reload', { ignoreCache: true }); }
  // Forget Epic/hCaptcha/Cloudflare state (cookies + site storage) so the next login starts clean.
  async resetEpicState() {
    const ws = await cdp.browserWs(await this.port());
    const { cookies } = await cdp.call(ws, 'Storage.getCookies');
    const keep = cookies.filter(c => !/(epicgames|hcaptcha|unrealengine|cloudflare)\./.test(c.domain));
    await cdp.call(ws, 'Storage.clearCookies');
    if (keep.length) await cdp.call(ws, 'Storage.setCookies', { cookies: keep.map(({ name, value, domain, path, secure, httpOnly, sameSite, expires }) => ({ name, value, domain, path, secure, httpOnly, sameSite, ...(expires > 0 ? { expires } : {}) })) });
    for (const origin of ['https://www.epicgames.com', 'https://store.epicgames.com', 'https://epicgames.com', 'https://talon-website-prod.ecosec.on.epicgames.com', 'https://newassets.hcaptcha.com', 'https://hcaptcha.com'])
      await cdp.call(ws, 'Storage.clearDataForOrigin', { origin, storageTypes: 'all' }).catch(() => {});
    await fs.rm(this.sessionFile, { force: true });
  }
  // Cookies exported from the user's own browser (Cookie-Editor JSON or Netscape, normalised by the server).
  async importCookies(list) {
    const ws = await cdp.browserWs(await this.port());
    const cookies = list.filter(c => c && c.name && /epicgames\.com$/.test(String(c.domain || '').replace(/^\./, ''))).map(c => ({ name: String(c.name), value: String(c.value ?? ''), domain: String(c.domain), path: c.path || '/', secure: c.secure !== false, httpOnly: !!c.httpOnly, ...(c.expires > 0 ? { expires: c.expires } : { expires: Math.floor(Date.now() / 1000) + 30 * 86400 }) }));
    if (cookies.length) await cdp.call(ws, 'Storage.setCookies', { cookies });
    return cookies.length;
  }
  async port() { return cdp.devtoolsPort(this.profile); }
  async page() { return cdp.pageTarget(await this.port()); }
  async eval(expression) { const t = await this.page(); return cdp.evaluate(t.webSocketDebuggerUrl, expression); }
  async navigate(url) { const t = await this.page(); return cdp.call(t.webSocketDebuggerUrl, 'Page.navigate', { url }); }
  // Click a point given in page CSS pixels using real X input.
  async clickPage(x, y) {
    const o = await this.eval('({sx:screenX,sy:screenY,ow:outerWidth,oh:outerHeight,iw:innerWidth,ih:innerHeight,dpr:devicePixelRatio})');
    const left = o.sx + (o.ow - o.iw) / 2, top = o.sy + (o.oh - o.ih);
    const X = Math.round(left + x), Y = Math.round(top + y);
    await this.xdo(['mousemove', String(X), String(Y)]); await new Promise(r => setTimeout(r, 150 + Math.random() * 200));
    await this.xdo(['click', '1']);
  }
  async start() {
    await fs.rm(path.join(this.profile, 'DevToolsActivePort'), { force: true }).catch(() => {});
    this.chrome = spawn(this.executable, browserArgs(this.profile, this.url), { env: browserEnv(), stdio: 'ignore' });
    this.exited = new Promise(r => this.chrome.once('exit', r));
    const fps = String(Number(process.env.EPIC_LIVE_FPS) || 5);
    this.ffmpeg = spawn('ffmpeg', ['-loglevel', 'error', '-f', 'x11grab', '-draw_mouse', '1', '-framerate', fps, '-video_size', `${SCREEN.w}x${SCREEN.h}`, '-i', process.env.DISPLAY,
      '-f', 'image2pipe', '-vcodec', 'mjpeg', '-q:v', '7', '-'], { stdio: ['ignore', 'pipe', 'ignore'] });
    this.ffmpeg.stdout.on('data', jpegSplitter(f => this.onFrame?.(f)));
    if (await this.restoreSession() && this.reloadAfterRestore !== false) {
      const t = await this.page().catch(() => null);
      if (t) await cdp.call(t.webSocketDebuggerUrl, 'Page.reload', {}).catch(() => {});
    }
  }
  // Serialize X input so events keep their order. Text goes via stdin, never argv.
  input(i) { for (const cmd of xdotoolCommands(i)) this.xdo(cmd); }
  typeSecret(text) { this.xdo(['type', '--delay', '40', '--file', '-', String(text)]); }
  xdo(cmd) {
    this.queue = this.queue.then(() => new Promise(resolve => {
      cmd = [...cmd]; const stdinText = cmd.includes('--file') ? cmd.pop() : null;
      const p = spawn('xdotool', cmd, { stdio: [stdinText === null ? 'ignore' : 'pipe', 'ignore', 'ignore'] });
      if (stdinText !== null) p.stdin.end(stdinText);
      p.once('exit', resolve); p.once('error', resolve);
    }));
    return this.queue;
  }
  async title() {
    return new Promise(resolve => {
      let out = '';
      const p = spawn('xdotool', ['search', '--onlyvisible', '--class', 'chromium', 'getwindowname', '%@'], { stdio: ['ignore', 'pipe', 'ignore'] });
      p.stdout.on('data', d => { out += d; }); p.once('exit', () => resolve(out.slice(0, 2000))); p.once('error', () => resolve(''));
    });
  }
  // Graceful SIGTERM lets Chromium flush cookies to the profile before we read it.
  async stop() {
    if (this.chrome && this.chrome.exitCode === null) await this.saveSession();
    this.ffmpeg?.kill('SIGTERM');
    if (this.chrome && this.chrome.exitCode === null) {
      this.chrome.kill('SIGTERM');
      await Promise.race([this.exited, new Promise(r => setTimeout(r, 15000))]);
      if (this.chrome.exitCode === null) this.chrome.kill('SIGKILL');
    }
    await this.queue.catch(() => {});
  }
}
