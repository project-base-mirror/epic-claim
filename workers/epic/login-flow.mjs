// Page-state detection for Epic's login (https://www.epicgames.com/id/login).
// Selectors are best-effort from Epic's public login markup and are NOT verified
// against a live login with real credentials; unknown states fail closed.
export const SUCCESS_URL = 'https://www.epicgames.com/account/personal';
export function loginURL() {
  // Test hook: only loopback URLs may replace the real Epic login page.
  const o = process.env.EPIC_LOGIN_URL_FOR_TESTS;
  if (o) { const u = new URL(o); if (!['127.0.0.1', 'localhost'].includes(u.hostname)) throw Error('test URL must be loopback'); return u.href; }
  return 'https://www.epicgames.com/id/login?lang=en-US&noHostRedirect=true&redirectUrl=' + encodeURIComponent(SUCCESS_URL);
}
export const SELECTORS = {
  email: 'input#email, input[name="email"], input[type="email"]',
  continue: 'button#continue, button#sign-in, button[type="submit"]',
  password: 'input#password, input[name="password"], input[type="password"]',
  signIn: 'button#sign-in, button#continue, button[type="submit"]',
  codeSplit: 'input[name^="code-input-"]',
  code: 'input#code, input[name="code"], input[autocomplete="one-time-code"], input[name^="code-input-"]',
  codeSubmit: 'button#continue, button#verify, button[type="submit"]',
  error: '#email-error, #password-error, [role="alert"], .error-message',
  captchaFrame: 'iframe[src*="hcaptcha.com"], iframe[src*="arkoselabs"], iframe[src*="funcaptcha"], iframe[src*="talon"], iframe[title*="challenge" i]',
};
const visible = async loc => { for (const l of await loc.all()) if (await l.isVisible().catch(() => false)) return l; return null; };

export function isSuccessURL(u) {
  try {
    const url = new URL(u);
    if (url.hostname === '127.0.0.1' || url.hostname === 'localhost') return process.env.EPIC_LOGIN_URL_FOR_TESTS ? url.pathname.startsWith('/account') : false;
    return url.protocol === 'https:' && url.hostname === 'www.epicgames.com' && url.pathname.startsWith('/account');
  } catch { return false; }
}

// Returns: ready | captcha | bad_credentials | code | password | email | unknown
export async function classify(page) {
  if (isSuccessURL(page.url())) return 'ready';
  // Cloudflare "Just a moment… / One more step" interstitial (observed on
  // 2026-10-15 for headless Chromium on the deployment host) is a challenge too.
  if (/^just a moment/i.test(await page.title().catch(() => '')) || await page.locator('input[name="cf-turnstile-response"], #challenge-form, #cf-challenge-running').count().catch(() => 0)) return 'captcha';
  for (const f of await page.locator(SELECTORS.captchaFrame).all()) {
    const box = await f.boundingBox().catch(() => null);
    if (box && box.width >= 200 && box.height >= 200) return 'captcha';
  }
  const err = await visible(page.locator(SELECTORS.error));
  // Epic's anti-bot rejection ("Incorrect response. Please refresh the page.") is not a password error.
  if (err && /incorrect response|refresh the page/i.test(await err.textContent().catch(() => '') || '')) return 'captcha';
  if (err && /incorrect|invalid|wrong|doesn.t match|not match/i.test(await err.textContent().catch(() => '') || '')) return 'bad_credentials';
  if (await visible(page.locator(SELECTORS.code))) return 'code';
  if (await visible(page.locator(SELECTORS.password))) return 'password';
  if (await visible(page.locator(SELECTORS.email))) return 'email';
  return 'unknown';
}
