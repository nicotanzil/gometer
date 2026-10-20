const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../assets/theme.js'), 'utf8');

function boot(saved = new Map(), systemDark = false) {
  const listeners = {}, pickerListeners = {};
  const root = {dataset: {}};
  const picker = {value: '', addEventListener: (type, fn) => { pickerListeners[type] = fn; }};
  const media = {matches: systemDark, addEventListener: (type, fn) => { listeners[type] = fn; }};
  const storage = {
    getItem: key => saved.get(key) ?? null,
    setItem: (key, value) => saved.set(key, value),
    removeItem: key => saved.delete(key),
  };
  const document = {
    documentElement: root,
    addEventListener: (type, fn) => { listeners[type] = fn; },
    getElementById: id => id === 'theme-select' ? picker : null,
  };
  vm.runInNewContext(source, {window: {matchMedia: () => media, dispatchEvent: () => {}}, document, localStorage: storage, Event});
  return {
    root, picker, saved,
    ready: () => listeners.DOMContentLoaded(),
    select: value => { picker.value = value; pickerListeners.change(); },
    setSystemDark: value => { media.matches = value; listeners.change(); },
  };
}

test('saved theme applies before the picker loads and persists after changing it', () => {
  const saved = new Map([['gometer.theme.v1', 'dark']]);
  const page = boot(saved);
  assert.equal(page.root.dataset.theme, 'dark');
  page.ready();
  assert.equal(page.picker.value, 'dark');
  page.select('light');
  assert.equal(page.root.dataset.theme, 'light');
  assert.equal(saved.get('gometer.theme.v1'), 'light');
  assert.equal(boot(saved, true).root.dataset.theme, 'light');
});

test('system theme follows appearance changes and resumes after an override is cleared', () => {
  const page = boot();
  page.ready();
  assert.equal(page.picker.value, 'system');
  assert.equal(page.root.dataset.theme, 'light');
  page.setSystemDark(true);
  assert.equal(page.root.dataset.theme, 'dark');
  page.select('light');
  page.setSystemDark(false);
  page.setSystemDark(true);
  assert.equal(page.root.dataset.theme, 'light');
  page.select('system');
  assert.equal(page.root.dataset.theme, 'dark');
  assert.equal(page.saved.has('gometer.theme.v1'), false);
});
