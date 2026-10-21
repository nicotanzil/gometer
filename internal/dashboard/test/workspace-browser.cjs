'use strict';

const assert = require('node:assert/strict');

module.exports = async function verifyWorkspaceScreens(page) {
  async function navigate(view) {
    if (await page.locator('#navigation-open').isVisible()) await page.locator('#navigation-open').click();
    await page.locator(`.sidebar [data-view="${view}"]`).click();
  }
  await navigate('history');
  const choices = page.locator('#history .history-select:not([disabled])');
  await choices.first().waitFor();
  assert.ok(await choices.count() >= 3);
  await choices.nth(0).check();
  await choices.nth(1).check();
  await page.locator('#compare').click();
  await page.locator('#comparison').waitFor({state:'visible'});
  assert.match(await page.locator('#comparison-table').innerText(), /B − A/);
  assert.match(await page.locator('#comparison').innerText(), /do not establish a regression/);
  await page.locator('#history-search').fill('not-a-real-fixture-run');
  assert.match(await page.locator('#history').innerText(), /No runs match/);
  await page.locator('#history-search').fill('');

  for (const theme of ['light', 'dark']) {
    await page.locator('#theme-select').selectOption(theme);
    for (const width of [1440, 768, 390, 320]) {
      await page.setViewportSize({width, height:1000});
      for (const view of ['live', 'history', 'guide', 'coverage']) {
        await navigate(view);
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `${view} overflow at ${width}px (${theme})`);
      }
    }
  }
  await page.setViewportSize({width:1440, height:1100});
  await navigate('coverage');
  await page.locator('#coverage-search').fill('HTTP');
  await page.locator('#coverage-status').selectOption('available');
  assert.match(await page.locator('#coverage-table').innerText(), /HTTP/);
  await page.locator('#coverage-search').fill('');
  await page.locator('#coverage-status').selectOption('');

  await navigate('build');
  await page.locator('#import-jmx').click();
  await page.getByRole('button', {name:'Review sample plan', exact:true}).click();
  await page.locator('.jmx-components').waitFor({state:'visible'});
  assert.match(await page.locator('#view-jmx').innerText(), /runtime policies/);
  for (const width of [1440, 768, 390, 320]) {
    await page.setViewportSize({width, height:1000});
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `JMX review overflow at ${width}px`);
  }
  await navigate('build');
  await page.locator('#builder-settings-tab').click();
  await page.locator('#starter').selectOption('csv');
  await page.locator('#builder-journey-tab').click();
  const canvas = page.locator('#journey-canvas');
  // Mobile Canvas / Inspector controls must expose the real journey settings.
  await canvas.getByRole('button', {name:'Journey settings & data', exact:true}).click();
  await canvas.getByRole('button', {name:'Edit CSV dataset', exact:true}).click();
  await page.locator('.csv-dialog').waitFor({state:'visible'});
  assert.match(await page.locator('.csv-dialog').innerText(), /No workload traffic|no workload traffic|sends no workload traffic/);
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'CSV dialog overflow');
  await page.locator('#csv-cancel').click();
  await page.setViewportSize({width:1440, height:1100});
  await page.locator('#theme-select').selectOption('light');
};
