// Minimal raw DevTools client for the plain browser. Only stateless calls are
// used (Runtime.evaluate without Runtime.enable, Page.navigate, Storage.*), so
// pages see no automation hooks; all clicks/typing are real X input (xdotool).
import fs from 'node:fs/promises';
import path from 'node:path';

export async function devtoolsPort(profile, timeoutMs = 20000) {
  const end = Date.now() + timeoutMs;
  while (Date.now() < end) {
    try { const [port] = (await fs.readFile(path.join(profile, 'DevToolsActivePort'), 'utf8')).split('\n'); if (+port > 0) return +port; } catch {}
    await new Promise(r => setTimeout(r, 300));
  }
  throw Object.assign(Error('no devtools port'), { code: 'NO_DEVTOOLS' });
}

export async function targets(port) {
  const r = await fetch(`http://127.0.0.1:${port}/json/list`); return r.json();
}

export async function call(wsUrl, method, params = {}, timeoutMs = 20000) {
  const ws = new WebSocket(wsUrl);
  try {
    await new Promise((res, rej) => { ws.onopen = res; ws.onerror = () => rej(Error('cdp connect failed')); setTimeout(() => rej(Error('cdp connect timeout')), 5000); });
    return await new Promise((res, rej) => {
      const t = setTimeout(() => rej(Error('cdp timeout')), timeoutMs);
      ws.onmessage = e => { const m = JSON.parse(e.data); if (m.id === 1) { clearTimeout(t); m.error ? rej(Error(m.error.message)) : res(m.result); } };
      ws.send(JSON.stringify({ id: 1, method, params }));
    });
  } finally { try { ws.close(); } catch {} }
}

export async function browserWs(port) { const r = await fetch(`http://127.0.0.1:${port}/json/version`); return (await r.json()).webSocketDebuggerUrl; }
export async function pageTarget(port) { return (await targets(port)).find(t => t.type === 'page' && !t.url.startsWith('devtools://')); }

// Evaluate an expression (string) in a target; returns the JSON value.
export async function evaluate(wsUrl, expression) {
  const r = await call(wsUrl, 'Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
  if (r.exceptionDetails) throw Error('evaluate failed');
  return r.result.value;
}
