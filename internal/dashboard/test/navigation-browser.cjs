'use strict';

const assert = require('node:assert/strict');

// Runs against the same disposable dashboard as the journey integration suite.
module.exports = async function verifyNavigation(page) {
  const initialConfig = await page.locator('#config-json').inputValue();
  const expanded = await page.locator('main').boundingBox();
  await page.getByRole('button', {name:'Collapse sidebar', exact:true}).click();
  assert.equal(await page.locator('#sidebar-toggle').getAttribute('aria-expanded'), 'false');
  assert.ok((await page.locator('main').boundingBox()).width > expanded.width + 100);
  await page.locator('.sidebar [data-view=history]').focus();
  await page.locator('#navigation-tooltip').waitFor({state:'visible'});
  assert.equal(await page.locator('#navigation-tooltip').innerText(), 'Runs');
  await page.keyboard.press('Escape');
  assert.equal(await page.locator('#navigation-tooltip').isVisible(), false);
  await page.locator('.sidebar [data-view=guide]').hover();
  await page.locator('#navigation-tooltip').waitFor({state:'visible'});
  assert.equal(await page.locator('#navigation-tooltip').innerText(), 'Guide');
  await page.reload();
  await page.waitForFunction(() => document.querySelector('#config-json').value.includes('"url"'));
  assert.equal(await page.locator('html').getAttribute('data-sidebar'), 'collapsed');
  await page.locator('#sidebar-toggle').press('Enter');
  assert.equal(await page.locator('#sidebar-toggle').getAttribute('aria-expanded'), 'true');

  for (const theme of ['light', 'dark', 'system']) {
    await page.locator('#theme-select').selectOption(theme);
    if (theme === 'system') {
      await page.emulateMedia({colorScheme:'dark'});
      await page.waitForFunction(() => document.documentElement.dataset.theme === 'dark');
      await page.emulateMedia({colorScheme:'light'});
      await page.waitForFunction(() => document.documentElement.dataset.theme === 'light');
    } else assert.equal(await page.locator('html').getAttribute('data-theme'), theme);
  }
  await page.locator('#sidebar-toggle').click();
  for (const width of [768, 390, 320]) {
    await page.setViewportSize({width, height:1000});
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `Shell overflow at ${width}px`);
    const opener = page.getByRole('button', {name:'Open navigation', exact:true});
    await opener.click();
    assert.equal(await page.locator('main').evaluate(e => e.inert), true);
    assert.equal(await page.locator('#sidebar-toggle').getAttribute('aria-expanded'), 'true');
    const focusable = page.locator('.sidebar a[href], .sidebar button');
    await focusable.last().focus();
    await page.keyboard.press('Tab');
    assert.equal(await focusable.first().evaluate(e => e === document.activeElement), true);
    await page.keyboard.press('Shift+Tab');
    assert.equal(await focusable.last().evaluate(e => e === document.activeElement), true);
    await page.keyboard.press('Escape');
    assert.equal(await opener.evaluate(e => e === document.activeElement), true);
    await opener.click();
    await page.locator('.sidebar [data-view=guide]').click();
    assert.equal(await page.locator('#view-guide').isVisible(), true);
    assert.equal(await page.locator('main').evaluate(e => e.inert), false);
    await opener.click();
    await page.locator('.sidebar [data-shortcuts]').click();
    await page.locator('#keyboard-shortcuts-dialog').waitFor({state:'visible'});
    await page.keyboard.press('Escape');
    assert.equal(await opener.evaluate(e => e === document.activeElement), true);
    await opener.click();
    await page.locator('.sidebar [data-view=tests]').click();await page.locator('#test-resume').click();
  }
  // Resizing while the drawer is open must restore desktop navigation and editing.
  await page.locator('#navigation-open').click();
  await page.setViewportSize({width:1440, height:1100});
  await page.waitForFunction(() => !document.querySelector('main').inert);
  assert.equal(await page.locator('html').getAttribute('data-sidebar'), 'collapsed');
  assert.equal(await page.locator('#navigation-open').isVisible(), false);
  assert.equal(await page.locator('#config-json').inputValue(), initialConfig, 'Navigation changed scenario configuration.');
  await page.locator('#sidebar-toggle').click();
  await page.locator('#theme-select').selectOption('light');
};
