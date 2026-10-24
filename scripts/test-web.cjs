// Run with: node --test scripts/test-web.cjs
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');

const source = readFileSync(path.join(__dirname, '../internal/assets/web/app.js'), 'utf8');
// Exercise the actual DOM-independent formatting functions without browser globals.
const context = vm.createContext({});
vm.runInContext([
  source.match(/const trafficUnits = .*;/)[0],
  source.slice(source.indexOf('function bytes('), source.indexOf('function megabits(')),
  source.slice(source.indexOf('const escapeHTML ='), source.indexOf('\nfunction toast(')),
  source.slice(source.indexOf('function reportTime('), source.indexOf('function parseExpiryInput(')),
  'Date.now = () => Date.parse("2026-10-24T12:00:00Z"); this.formatExpiry = expiryInfo;',
  'this.formatQuota = trafficQuotaHTML;',
].join('\n'), context);
const render = context.formatQuota;

test('empty quotas leave the cell blank; zero is a valid quota', () => {
  for (const trafficQuota of [undefined, null, '', '  ']) {
    assert.equal(render({ trafficQuota, totalUpload: 1024 }), '');
  }
  assert.match(render({ trafficQuota: '0', trafficQuotaUnit: 'MB' }), /0 MB<\/span><small[^>]*>剩余 0 B/);
});

test('TB is the default; explicit units remain unchanged', () => {
  assert.match(render({ trafficQuota: '1', totalUpload: 512 * 1024 ** 3 }), /1 TB<\/span><small[^>]*>剩余 512 GB/);
  for (const [index, unit] of ['B', 'KB', 'MB', 'GB', 'TB', 'PB'].entries()) {
    assert.match(render({ trafficQuota: '2', trafficQuotaUnit: unit, totalUpload: 1024 ** index }), new RegExp(`2 ${unit}</span><small[^>]*>剩余 ${index === 0 ? '1' : '1.00'} ${unit}`));
  }
});

test('decimals, exhausted quotas, and overages use total upload only', () => {
  assert.match(render({ trafficQuota: '1.5', trafficQuotaUnit: 'TB', totalUpload: 1024 ** 4, totalDownload: 9 * 1024 ** 4 }), /剩余 512 GB/);
  assert.match(render({ trafficQuota: '1', totalUpload: 1024 ** 4 }), /剩余 0 B/);
  assert.match(render({ trafficQuota: '1', totalUpload: 1.5 * 1024 ** 4 }), /quota-remaining exceeded[^>]*>剩余 −512 GB/);
  assert.match(render({ trafficQuota: '0', trafficQuotaUnit: 'MB', totalUpload: 1024 }), /剩余 −1.00 KB/);
});

test('remaining traffic is recalculated after uploads and resets', () => {
  const node = { trafficQuota: '1', totalUpload: 0 };
  assert.match(render(node), /剩余 1.00 TB/);
  node.totalUpload = 256 * 1024 ** 3;
  assert.match(render(node), /剩余 768 GB/);
  node.totalUpload = 0;
  assert.match(render(node), /剩余 1.00 TB/);
});

test('invalid quota metadata is not rendered', () => {
  for (const node of [{ trafficQuota: '-1' }, { trafficQuota: 'NaN' }, { trafficQuota: '1e1000' }, { trafficQuota: '1', trafficQuotaUnit: 'XB' }]) {
    assert.equal(render(node), '');
  }
});

test('expiry dates use Beijing time to the minute and remaining full days', () => {
  const info = context.formatExpiry('2026-10-27T12:00:00Z');
  assert.equal(info.date, '2026-10-27 20:00');
  assert.equal(info.label, '剩余3天');
  assert.equal(info.urgent, false);
  assert.equal(context.formatExpiry(''), null);
});

test('expiry warnings retain the less-than-two-day and expired boundaries', () => {
  for (const [date, label, urgent] of [
    ['2026-10-26T12:00:00Z', '剩余2天', false],
    ['2026-10-26T11:59:59Z', '剩余1天', true],
    ['2026-10-25T11:59:59Z', '不足1天', true],
    ['2026-10-24T12:00:00Z', '已到期', true],
  ]) {
    const info = context.formatExpiry(date);
    assert.equal(info.label, label);
    assert.equal(info.urgent, urgent);
  }
});
