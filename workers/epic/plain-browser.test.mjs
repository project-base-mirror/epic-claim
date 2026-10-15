import test from 'node:test';
import assert from 'node:assert/strict';
import { browserArgs, browserEnv, jpegSplitter, xdotoolCommands, SCREEN } from './plain-browser.mjs';

test('plain browser has no automation flags; proxy/timezone are configurable', () => {
  const a = browserArgs('/p', 'https://www.epicgames.com/id/login', {});
  assert.ok(!a.some(x => /enable-automation|remote-debugging|headless/.test(x)));
  assert.ok(browserArgs('/p', 'u', { EPIC_BROWSER_PROXY: 'direct' }).includes('--no-proxy-server'));
  assert.ok(browserArgs('/p', 'u', { EPIC_BROWSER_PROXY: 'http://h:7890' }).includes('--proxy-server=http://h:7890'));
  assert.ok(!browserArgs('/p', 'u', { EPIC_BROWSER_PROXY: 'http://h:7890 --evil' }).some(x => x.includes('evil')));
  const e = browserEnv({ EPIC_BROWSER_PROXY: 'direct', HTTPS_PROXY: 'x', EPIC_BROWSER_TZ: 'Asia/Shanghai' });
  assert.equal(e.HTTPS_PROXY, undefined); assert.equal(e.TZ, 'Asia/Shanghai');
});
test('MJPEG stream is split into whole JPEGs across chunk boundaries', () => {
  const frames = []; const push = jpegSplitter(f => frames.push(f.toString('hex')));
  push(Buffer.from('00ffd8aabb', 'hex')); push(Buffer.from('ccffd9ffd8', 'hex')); push(Buffer.from('11ffd9', 'hex'));
  assert.deepEqual(frames, ['ffd8aabbccffd9', 'ffd811ffd9']);
});
test('input maps fractions to X coordinates; text goes via stdin', () => {
  assert.deepEqual(xdotoolCommands({ type: 'click', x: 0.5, y: 1 }), [['mousemove', String(Math.round(0.5 * (SCREEN.w - 1))), String(SCREEN.h - 1), 'click', '1']]);
  assert.deepEqual(xdotoolCommands({ type: 'key', key: 'Enter' }), [['key', 'Return']]);
  assert.deepEqual(xdotoolCommands({ type: 'text', text: 'a b' }), [['type', '--file', '-', 'a b']]);
  assert.deepEqual(xdotoolCommands({ type: 'key', key: 'F13' }), []);
});
