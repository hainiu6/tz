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
