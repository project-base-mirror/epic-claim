// Claim driver for the plain (non-automated) browser: page state is read with
// stateless DevTools evaluation; every click is real X input. Implements the
// driver interface used by core.claimGame (which enforces the zero-total gates).
const sleep = ms => new Promise(r => setTimeout(r, ms));

export function productURL(g) {
  const u = new URL(g.url); const slug = u.pathname.match(/\/p\/([^/]+)/)?.[1];
  return `https://store.epicgames.com/en-US/p/${slug}`;
}

// Runs inside the page. Returns a compact description of the visible state.
export const STATE_JS = `(() => {
  const vis = el => { const r = el.getBoundingClientRect(); return r.width > 2 && r.height > 2 && getComputedStyle(el).visibility !== 'hidden' && r.bottom > 0 && r.top < innerHeight; };
  const rect = el => { const r = el.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; };
  const btns = [...document.querySelectorAll('button, a[role=button], [role=button]')].filter(vis);
  const byText = re => btns.filter(b => re.test((b.innerText || b.textContent || '').trim()));
  const frames = [...document.querySelectorAll('iframe')].filter(vis).map(f => ({ src: f.src, ...f.getBoundingClientRect().toJSON() }));
  const cf = /^just a moment/i.test(document.title) || !!document.querySelector('input[name="cf-turnstile-response"]');
  const hcap = frames.some(f => /hcaptcha|talon|arkose/.test(f.src) && f.width >= 200 && f.height >= 200);
  const purchase = frames.find(f => /purchase/.test(f.src) && f.width > 200);
  const signIn = byText(/^sign in$/i).length > 0;
  const owned = byText(/^(in library|owned)$/i).length > 0;
  const get = byText(/^get$/i);
  const cont = byText(/^(continue|i understand|accept)$/i).filter(b => b.closest('[role=dialog], [aria-modal=true], .modal'));
  return { url: location.href, title: document.title, cf, hcap, signIn, owned,
    get: get.length === 1 ? rect(get[0]) : null, getCount: get.length,
    dialogContinue: cont.length === 1 ? { ...rect(cont[0]), text: cont[0].innerText.trim() } : null,
    purchase: purchase ? { src: purchase.src, left: purchase.left, top: purchase.top } : null,
    text: document.body ? document.body.innerText.replace(/\\s+/g, ' ').slice(0, 600) : '' };
})()`;

// Runs inside the purchase document.
export const CHECKOUT_JS = `(() => {
  const vis = el => { const r = el.getBoundingClientRect(); return r.width > 2 && r.height > 2 && getComputedStyle(el).visibility !== 'hidden'; };
  const rect = el => { const r = el.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; };
  const text = document.body ? document.body.innerText.replace(/\\s+/g, ' ') : '';
  const btns = [...document.querySelectorAll('button')].filter(vis);
  const place = btns.filter(b => /^(place order|add to library)$/i.test(b.innerText.trim()));
  const accept = btns.filter(b => /^(i accept|accept|i agree|agree|continue)$/i.test(b.innerText.trim()));
  const total = (text.match(/\\bTotal\\b\\s*:?\\s*([^\\s]+(?:\\s?[0-9.,]+)?)/i) || [])[1] || '';
  return { url: location.href, text: text.slice(0, 1500), total,
    place: place.length === 1 ? { ...rect(place[0]), disabled: place[0].disabled } : null, placeCount: place.length,
    accept: accept.length === 1 ? { ...rect(accept[0]), text: accept[0].innerText.trim() } : null,
    thanks: /thank you for (buying|your order)|thanks for your order|order (complete|confirmed)/i.test(text),
    needsPayment: /(select|choose|add) (a )?payment method|card number|credit card/i.test(text),
    loading: !!document.querySelector('[class*=loading i], [aria-busy=true]') && place.length === 0 };
})()`;

export function zeroTotal(total) {
  const t = String(total || '').trim();
  return /^(free|(US\$|\$|€|£|¥|CA\$|A\$)?\s?0(?:[.,]00?)?(\s?[A-Z]{3})?)$/i.test(t);
}

export class PlainDriver {
  // waitForUser: in a live session, blockers wait (up to 10 min) for the user to resolve them.
  constructor(pb, { waitForUser = false, emit = () => {}, log = () => {} } = {}) { Object.assign(this, { pb, waitForUser, emit, log }); }
  async state() { return this.pb.eval(STATE_JS); }
  async open(g) {
    await this.pb.navigate(productURL(g));
    for (let i = 0; i < 30; i++) { await sleep(1000); const s = await this.state().catch(() => null); if (s && (s.cf || s.owned || s.get || s.signIn || s.hcap) && i >= 3) break; }
  }
  async detectBlock() {
    const s = await this.state();
    if (s.cf || s.hcap) return 'captcha_required';
    if (s.signIn) return 'login_required';
    if (s.purchase) { const c = await this.checkoutState().catch(() => null); if (c?.needsPayment) return 'price_changed'; }
    return null;
  }
  async blocker() {
    const end = Date.now() + (this.waitForUser ? 10 * 60 * 1000 : 0);
    for (;;) {
      const b = await this.detectBlock();
      if (!b || b === 'price_changed' || !this.waitForUser || Date.now() > end) return b;
      this.emit(b === 'login_required' ? 'claim_login' : 'claim_challenge');
      await sleep(2500);
    }
  }
  async owned() { return (await this.state()).owned; }
  async get() {
    const s = await this.state();
    if (!s.get) throw Object.assign(Error('no unique Get button'), { code: 'UNSUPPORTED_DOM' });
    await this.pb.clickPage(s.get.x, s.get.y);
    // Interstitials before checkout: mature-content / device warnings ("Continue").
    for (let i = 0; i < 20; i++) {
      await sleep(1500);
      const st = await this.state();
      if (st.purchase || st.cf || st.hcap) return;
      if (st.dialogContinue) { this.log('dialog: ' + st.dialogContinue.text); await this.pb.clickPage(st.dialogContinue.x, st.dialogContinue.y); }
    }
  }
  async purchaseTarget() {
    const ts = await (await import('./cdp.mjs')).targets(await this.pb.port());
    return ts.find(t => (t.type === 'iframe' || t.type === 'page') && /\/purchase/.test(t.url));
  }
  async checkoutState() {
    const s = await this.state();
    if (!s.purchase) return null;
    const cdp = await import('./cdp.mjs');
    const t = await this.purchaseTarget();
    let c;
    if (t) c = await cdp.evaluate(t.webSocketDebuggerUrl, CHECKOUT_JS);
    else c = await this.pb.eval(`(() => { const f = [...document.querySelectorAll('iframe')].find(f => /purchase/.test(f.src)); const w = f && f.contentWindow; return w ? w.eval(${JSON.stringify(CHECKOUT_JS)}) : null; })()`);
    return c && { ...c, frameLeft: s.purchase.left, frameTop: s.purchase.top, src: s.purchase.src };
  }
  async checkout(g) {
    for (let i = 0; i < 40; i++) {
      const c = await this.checkoutState().catch(() => null);
      if (c?.accept && !c.place) { this.emit('claim_terms'); if (!this.waitForUser) return null; await sleep(2500); continue; }
      if (c?.place && !c.place.disabled && c.total) {
        this.current = c;
        const identity = c.src.includes(g.id) || c.src.includes(g.namespace) || c.text.includes(g.title);
        return { identityVerified: identity, itemCount: 1, finalTotal: zeroTotal(c.total) ? '0.00' : c.total, tax: 'Included', needsPayment: c.needsPayment };
      }
      await sleep(1500);
    }
    return null;
  }
  async placeOrder() {
    const c = await this.checkoutState();
    if (!c?.place || c.needsPayment || !zeroTotal(c.total)) throw Error('checkout changed');
    await this.pb.clickPage(c.frameLeft + c.place.x, c.frameTop + c.place.y);
    for (let i = 0; i < 30; i++) {
      await sleep(2000);
      const s = await this.state().catch(() => null);
      if (s && (s.hcap || s.cf)) { if (!this.waitForUser) return; this.emit('claim_challenge'); continue; }
      const k = await this.checkoutState().catch(() => null);
      if (!k || k.thanks) return;
    }
  }
}
