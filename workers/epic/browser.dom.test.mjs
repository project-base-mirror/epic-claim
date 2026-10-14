// Synthetic HTML contracts, NOT captured or asserted-current Epic markup.
// jsdom supplies real DOM parsing/querySelector semantics. It has no layout, so
// these fixtures shim geometry/innerText only; browser compatibility is untested.
import test from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';
import { readFileSync } from 'node:fs';
import { EpicBrowser, extractDOMEvidence } from './browser.mjs';
const game = { id: 'base-offer', namespace: 'game-ns', title: 'Example Game', startDate: new Date(Date.now() - 60000).toISOString(), endDate: new Date(Date.now() + 60000).toISOString() };
const identity = 'data-namespace="game-ns" data-offer-id="base-offer"';
const purchase = (body, attrs = identity) => `<main><h1>Example Game</h1><section data-testid="purchase-section" ${attrs}>${body}</section></main>`;
const item = (attrs = identity, extra = '') => `<article data-testid="order-line-item" ${attrs} data-quantity="1"><h3 data-testid="order-item-title">Example Game</h3>${extra}</article>`;
const order = (rows = item(), extra = '') => `<section class="payment-order-summary"><div data-testid="order-items">${rows}</div><div data-testid="final-order-total"><span>Total</span><span data-testid="final-order-total-amount" id="amount">$0.00</span></div><div data-testid="order-tax"><span>Tax</span><span data-testid="order-tax-amount">$0.00</span></div>${extra}</section><button>Place Order</button>`;
function fixture(html, url, requested = game) {
  const dom = new JSDOM(html, { runScripts: 'outside-only', ...(url ? {url} : {}) });
  const w = dom.window;
  Object.defineProperty(w.HTMLElement.prototype, 'innerText', { get() { return this.textContent.replace(/\s+/g, ' ').trim(); } });
  w.HTMLElement.prototype.getClientRects = function () {
    for (let p = this; p; p = p.parentElement) if (p.hidden || w.getComputedStyle(p).display === 'none') return [];
    return [{}];
  };
  const evaluate = async (fn, args) => w.eval(`(${fn.toString()})`)(args);
  return { dom, evaluate, extract: kind => evaluate(extractDOMEvidence, { kind, game: requested }), close: () => w.close() };
}
const unsupported = e => e?.code === 'UNSUPPORTED_DOM';

test('ownership extraction requires exact primary offer and ignores unrelated edition Owned', async () => {
  for (const [html, expected] of [
    [purchase('<button>Owned</button>'), true],
    [purchase('<button>Get</button>') + '<section data-offer-id="deluxe"><button>Owned</button></section>', false],
    [purchase('<button>Owned</button><button>Get</button>'), false],
    [purchase('<button>Owned</button><button>Owned</button>'), false],
  ]) {
    const f = fixture(html); const d = new EpicBrowser({}); d.productVerified = true; d.page = f;
    try { assert.equal(await d.owned(game), expected); } finally { f.close(); }
  }
});

test('missing, mismatched, nested and ambiguous primary identities stop as unsupported', async () => {
  const layouts = [
    '<main><h1>Example Game</h1><button>Owned</button></main>',
    purchase('<button>Owned</button>', ''),
    purchase('<button>Owned</button>', 'data-namespace="game-ns" data-offer-id="deluxe"'),
    purchase('<button>Owned</button>', 'data-namespace="other-ns" data-offer-id="base-offer"'),
    purchase('<div data-namespace="game-ns" data-offer-id="deluxe"><button>Owned</button></div>'),
    purchase('<button>Owned</button>') + purchase('<button>Owned</button>'),
  ];
  for (const html of layouts) {
    const f = fixture(html); const d = new EpicBrowser({}); d.productVerified = true; d.page = f;
    try { await assert.rejects(() => d.owned(game), unsupported); await assert.rejects(() => d.get(game), unsupported); } finally { f.close(); }
  }
});

test('checkout extraction enumerates rows, exact identity and quantity', async () => {
  const f = fixture(order());
  try { assert.deepEqual(JSON.parse(JSON.stringify(await f.extract('checkout'))), { identityVerified: true, itemCount: 1, finalTotal: '$0.00', tax: '$0.00' }); } finally { f.close(); }
  const layouts = [
    order(item() + item('data-namespace="other" data-offer-id="extra"')),
    order(item() + '<article>Unmarked bonus game</article>'),
    order(item('')),
    order(item('data-namespace="game-ns" data-offer-id="deluxe"')),
    order(item('data-namespace="other" data-offer-id="base-offer"')),
    order(item()).replace('data-quantity="1"', 'data-quantity="2"'),
    order(item()).replace('data-quantity="1"', ''),
    order(item(identity, '<span data-testid="item-quantity">2</span>')),
    order(item(identity, '<div data-namespace="other" data-offer-id="extra">Extra</div>')),
    order(item()) + item('data-namespace="other" data-offer-id="extra"'),
    order(item(), '<div><span>Total</span> <span>$0.00</span></div>'),
    order(item()).replace('Tax', 'Unrecognized tax label'),
    '<div>Example Game</div><div>Total $0.00</div>',
  ];
  for (const html of layouts) {
    const f = fixture(html); const d = new EpicBrowser({});
    d.checkoutFrame = { evaluate: f.evaluate, url: () => 'https://www.epicgames.com/purchase?namespace=game-ns&offers=base-offer' };
    try { assert.equal(await f.extract('checkout'), null); await assert.rejects(() => d.checkout(game), unsupported); } finally { f.close(); }
  }
});

test('actual checkout extraction detects inserted item and changed amount before order click', async () => {
  for (const change of [
    w => { w.document.querySelector('#amount').textContent = '$1.00'; },
    w => { w.document.querySelector('[data-testid="order-items"]').insertAdjacentHTML('beforeend', item('data-namespace="other" data-offer-id="extra"')); },
  ]) {
    const f = fixture(order()); const d = new EpicBrowser({}); let clicks = 0;
    d.blocker = async () => null; d.page = { waitForTimeout: async () => {} };
    d.checkoutFrame = { evaluate: f.evaluate, url: () => 'https://www.epicgames.com/purchase?namespace=game-ns&offers=base-offer', getByRole: () => ({ all: async () => [{ isVisible: async () => true, click: async () => { clicks++; } }] }) };
    try {
      const previous = await d.checkout(game); change(f.dom.window);
      await assert.rejects(() => d.placeOrder(game, previous)); assert.equal(clicks, 0);
    } finally { f.close(); }
  }
});

test('item-level zero totals cannot replace final order total or hide amount due', async () => {
  const itemFinancials = '<div><span>Total</span> <span>$0.00</span></div><div><span>Tax</span> <span>$0.00</span></div>';
  const layouts = [
    `<section class="payment-order-summary"><div data-testid="order-items">${item(identity, itemFinancials)}</div><aside>Amount due <span>$10.00</span></aside></section>`,
    order(item(identity, itemFinancials), '<aside>Amount due <span>$10.00</span></aside>'),
    order(item()) + '<aside>Amount due <span>$10.00</span></aside>',
    order(item(), '<aside>$10.00</aside>'),
    order(item(), '<div data-testid="final-order-total"><span>Total</span><span data-testid="final-order-total-amount">$10.00</span></div>'),
    order(item(), '<div data-testid="order-tax"><span>Tax</span><span data-testid="order-tax-amount">$10.00</span></div>'),
    order(item()).replace('<span data-testid="final-order-total-amount" id="amount">$0.00</span>', '<span>$0.00</span>'),
    order(item()).replace('<span data-testid="final-order-total-amount" id="amount">$0.00</span>', '<span data-testid="final-order-total-amount">$0.00</span><span>$10.00</span>'),
  ];
  for (const html of layouts) {
    const f = fixture(html);
    try { assert.equal(await f.extract('checkout'), null); } finally { f.close(); }
  }
  const f = fixture(order(item(identity, itemFinancials)).replace('id="amount">$0.00', 'id="amount">$10.00'));
  try { assert.equal((await f.extract('checkout')).finalTotal, '$10.00'); } finally { f.close(); }
});


const liveURL = 'https://store.epicgames.com/p/fortnite?lang=en-US';
const liveGame = {namespace: 'fn', id: '09176f4ff7564bbbb499bbe20bd6348f', title: 'Fortnite', url: 'https://store.epicgames.com/en-US/p/fortnite'};
const publicHTML = readFileSync(new URL('./fixtures/fortnite-public.html', import.meta.url), 'utf8');
test('captured public Epic schema and unique CTA identify the exact product', async () => {
  const f = fixture(publicHTML, liveURL, liveGame);
  try {
    const evidence = await f.extract('purchase');
    assert.equal(evidence.identityVerified, true); assert.equal(evidence.owned, false);
    assert.equal(evidence.getCount, 1); assert.equal(evidence.source, 'epic-product-schema');
    // Public catalog markup cannot prove checkout identity or final amount.
    assert.equal(await f.extract('checkout'), null);
  } finally {f.close();}
});
test('mutations of captured public evidence fail closed on identity and CTA ambiguity', async () => {
  for (const [html,url,g] of [
    [publicHTML,liveURL,{...liveGame,id:'other'}],
    [publicHTML,liveURL,{...liveGame,namespace:'other'}],
    [publicHTML,liveURL,{...liveGame,title:'Other'}],
    [publicHTML,liveURL,{...liveGame,url:'https://store.epicgames.com/p/other'}],
    [publicHTML, 'https://store.epicgames.com/p/other', liveGame],
    [publicHTML.replace('"mainEntityOfPage":true','"mainEntityOfPage":false'),liveURL,liveGame],
    [publicHTML.replace('"@type":"Product"','"@type":"Offer"'),liveURL,liveGame],
    [publicHTML + '<button data-testid="purchase-cta-button">Owned</button>',liveURL,liveGame],
    [publicHTML + '<h1>Fortnite</h1>',liveURL,liveGame],
    [publicHTML + '<link rel="canonical" href="https://store.epicgames.com/p/other">',liveURL,liveGame],
    [publicHTML.replace('>Get<','>Buy Now<'),liveURL,liveGame],
  ]) {
    const f=fixture(html,url,g); try {assert.equal(await f.extract('purchase'),null);} finally {f.close();}
  }
});
test('simulated ownership label is scoped to identified CTA, never unrelated page text', async () => {
  // This label mutation is synthetic; no signed-in ownership capture was taken.
  for (const [html,owned] of [[publicHTML+'<button>Owned</button>',false],[publicHTML.replace('>Get<','>In Library<'),true]]) {
    const f=fixture(html,liveURL,liveGame); try {assert.equal((await f.extract('purchase')).owned,owned);} finally {f.close();}
  }
});
