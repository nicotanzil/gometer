'use strict';

// Invoked only by the isolated Go/PostgreSQL fixture. No real company account.
const assert = require('node:assert/strict');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');

async function main() {
  const url = process.env.GOMETER_TEAM_FIXTURE_URL;
  if (!/^https:\/\/127\.0\.0\.1:\d+$/.test(url || '')) {
    throw Error('A loopback TLS team fixture is required.');
  }
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.CHROME_PATH ? {executablePath: process.env.CHROME_PATH} : {}),
  });
  try {
    const context = await browser.newContext({ignoreHTTPSErrors: true});
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    page.on('console', message => {
      if (message.type() === 'error' && !message.text().includes('favicon.ico')) errors.push(message.text());
    });
    await page.goto(url);
    await page.getByRole('heading', {name: 'Sign in to your workspace'}).waitFor();
    for (const colorScheme of ['light', 'dark']) {
      await page.emulateMedia({colorScheme});
      for (const width of [1440, 768, 390, 320]) {
        await page.setViewportSize({width, height: 900});
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
      }
    }
    await page.getByRole('button', {name: /Sign in with company account/}).click();
    try {
      await page.getByRole('heading', {name: 'Welcome, Alice Team'}).waitFor();
    } catch (error) {
      console.error({path: new URL(page.url()).pathname, body: await page.locator('body').innerText(), errors});
      throw error;
    }
    assert.equal(new URL(page.url()).search, '', 'authorization code retained in landing URL');
    const cookies = await context.cookies();
    const session = cookies.find(cookie => cookie.name === '__Host-gometer-session');
    assert.ok(session?.secure && session?.httpOnly && session?.sameSite === 'Lax');
    assert.equal(cookies.some(cookie => cookie.name === '__Host-gometer-login'), false);
    await page.reload();
    await page.getByRole('heading', {name: 'Welcome, Alice Team'}).waitFor();
    if (process.env.GOMETER_TEAM_SCREENSHOT) {
      await page.setViewportSize({width: 1440, height: 900});
      await page.emulateMedia({colorScheme: 'light'});
      await page.screenshot({path: process.env.GOMETER_TEAM_SCREENSHOT, fullPage: true});
    }
    await page.getByRole('button', {name: 'Sign out of GoMeter'}).click();
    await page.getByRole('heading', {name: 'Sign in to your workspace'}).waitFor();
    assert.equal((await context.cookies()).some(cookie => cookie.name === '__Host-gometer-session'), false);
    assert.deepEqual(errors, []);
    console.log('Team browser checks passed: TLS OIDC navigation, secure cookies, reload, CSRF form logout, two themes and four viewport widths.');
  } finally {
    await browser.close();
  }
}
main().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
