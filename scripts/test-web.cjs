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
const text = (node) => render(node).replace(/<[^>]*>/g, '');

test('empty quotas leave the cell blank; zero is a valid quota', () => {
  for (const trafficQuota of [undefined, null, '', '  ']) {
    assert.equal(render({ trafficQuota, totalUpload: 1024 }), '');
  }
  assert.equal(text({ trafficQuota: '0', trafficQuotaUnit: 'MB' }), '0 MB剩 0 B');
});

test('TB is the default; explicit units remain unchanged', () => {
  assert.equal(text({ trafficQuota: '1', totalUpload: 512 * 1024 ** 3 }), '1 TB剩 512 GB');
  for (const [index, unit] of ['B', 'KB', 'MB', 'GB', 'TB', 'PB'].entries()) {
    assert.equal(text({ trafficQuota: '2', trafficQuotaUnit: unit, totalUpload: 1024 ** index }), `2 ${unit}剩 ${index === 0 ? '1' : '1.00'} ${unit}`);
  }
});

test('decimals, exhausted quotas, and overages use total upload only', () => {
  assert.match(text({ trafficQuota: '1.5', trafficQuotaUnit: 'TB', totalUpload: 1024 ** 4, totalDownload: 9 * 1024 ** 4 }), /剩 512 GB/);
  assert.match(text({ trafficQuota: '1', totalUpload: 1024 ** 4 }), /剩 0 B/);
  assert.match(text({ trafficQuota: '1', totalUpload: 1.5 * 1024 ** 4 }), /剩 −512 GB/);
  assert.match(text({ trafficQuota: '0', trafficQuotaUnit: 'MB', totalUpload: 1024 }), /剩 −1.00 KB/);
});

test('remaining traffic is recalculated after uploads and resets', () => {
  const node = { trafficQuota: '1', totalUpload: 0 };
  assert.match(text(node), /剩 1.00 TB/);
  node.totalUpload = 256 * 1024 ** 3;
  assert.match(text(node), /剩 768 GB/);
  node.totalUpload = 0;
  assert.match(text(node), /剩 1.00 TB/);
});

test('invalid quota metadata is not rendered', () => {
  for (const node of [{ trafficQuota: '-1' }, { trafficQuota: 'NaN' }, { trafficQuota: '1e1000' }, { trafficQuota: '1', trafficQuotaUnit: 'XB' }]) {
    assert.equal(render(node), '');
  }
});

test('manual exhausted markers work with or without quota and can be cleared', () => {
  const node = { trafficQuota: '1', trafficQuotaUnit: 'TB', totalUpload: 0, trafficExhausted: true };
  assert.match(render(node), /1 TB<\/span><small class="quota-exhausted">流量已用完/);
  assert.doesNotMatch(text(node), /剩/);
  assert.equal(render({ trafficExhausted: true }), '<small class="quota-exhausted">流量已用完</small>');
  node.totalUpload = 2 * 1024 ** 4;
  assert.match(render(node), /流量已用完/);
  node.trafficExhausted = false;
  assert.match(text(node), /剩 −1.00 TB/);
  assert.doesNotMatch(render(node), /流量已用完/);
});

test('expiry dates use Beijing time to the minute and remaining full days', () => {
  const info = context.formatExpiry('2026-10-27T12:00:00Z');
  assert.equal(info.date, '2026-10-27 20:00');
  assert.equal(info.label, '剩3天');
  assert.equal(info.urgent, false);
  assert.equal(context.formatExpiry(''), null);
});

test('expiry warnings retain the less-than-two-day and expired boundaries', () => {
  for (const [date, label, urgent] of [
    ['2026-10-26T12:00:00Z', '剩2天', false],
    ['2026-10-26T11:59:59Z', '剩1天', true],
    ['2026-10-25T11:59:59Z', '剩不足1天', true],
    ['2026-10-24T12:00:00Z', '已到期', true],
  ]) {
    const info = context.formatExpiry(date);
    assert.equal(info.label, label);
    assert.equal(info.urgent, urgent);
  }
});

test('remaining text, values, and units have separate styling hooks', () => {
  const html = render({ trafficQuota: '1', totalUpload: 512 * 1024 ** 3 });
  assert.match(html, /<span class="remaining-label">剩<\/span> <span class="remaining-number">512<\/span> <span class="remaining-unit">GB<\/span>/);
  const expiry = context.formatExpiry('2026-10-27T12:00:00Z');
  assert.match(expiry.html, /remaining-label">剩<\/span> <span class="remaining-number">3<\/span> <span class="remaining-unit">天/);
  assert.equal(context.formatExpiry('2026-10-24T12:00:00Z').html, '<span class="remaining-label">已到期</span>');
});
