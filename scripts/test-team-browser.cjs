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
      // These intentional negative calls have their HTTP statuses asserted below.
      const expectedAPIError = (message.location().url.startsWith(url + '/api/secret-references') ||
        message.location().url.startsWith(url + '/api/tests')) &&
        /\b(403|409)\b/.test(message.text());
      if (message.type() === 'error' && !expectedAPIError && !message.text().includes('favicon.ico')) {
        errors.push(message.text());
      }
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
    const api = await page.evaluate(async () => {
      const session = await (await fetch('/api/session')).json();
      const tests = await fetch('/api/tests?scope=mine');
      const headers = {'Content-Type': 'application/json', 'X-Gometer-CSRF': session.csrf_token};
      const created = await fetch('/api/secret-references', {
        method: 'POST', headers,
        body: JSON.stringify({name: 'Browser fixture credential', source_key: 'PRIVATE_BROWSER_SOURCE'}),
      });
      const tag = created.headers.get('ETag');
      const ref = await created.json();
      const missingCSRF = await fetch('/api/secret-references', {
        method: 'POST', headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({name: 'Denied', source_key: 'token'}),
      });
      const document = {schema_version: 1, public_config: {
        name: 'Browser shared checkout', url: 'http://127.0.0.1:1', headers: {Authorization: ''},
      }, secret_bindings: [{path: '/headers/Authorization', reference_id: ref.id, revision: 1}]};
      const madeTest = await fetch('/api/tests', {method: 'POST', headers, body: JSON.stringify(document)});
      const test = await madeTest.json();
      const testTag = madeTest.headers.get('ETag');
      const documentPath = '/api/tests/' + test.id + '/document';
      const publicRead = await fetch(documentPath);
      const readTest = await publicRead.json();
      const exported = await fetch('/api/tests/' + test.id + '/export');
      const exportedDocument = await exported.json();
      const copy = await fetch('/api/tests/' + test.id + '/copy', {
        method: 'POST', headers: {...headers, 'If-Match': testTag},
      });
      const copiedTest = await copy.json();
      document.public_config.name = 'Browser edited checkout';
      const write = {method: 'PUT', headers: {...headers, 'If-Match': testTag}, body: JSON.stringify(document)};
      const edited = await fetch(documentPath, write);
      const editedTest = await edited.json();
      const staleTest = await fetch(documentPath, write);
      const testWithoutCSRF = await fetch('/api/tests', {
        method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(document),
      });
      const shared = {created: madeTest.status, read: publicRead.status, exported: exported.status,
        copied: copy.status, edited: edited.status, stale: staleTest.status, missingCSRF: testWithoutCSRF.status,
        test, readTest, exportedDocument, copiedTest, editedTest};
      const update = {method: 'PUT', headers: {...headers, 'If-Match': tag},
        body: JSON.stringify({name: ref.name, enabled: false})};
      const updated = await fetch('/api/secret-references/' + ref.id, update);
      const changed = await updated.json();
      const stale = await fetch('/api/secret-references/' + ref.id, update);
      const catalog = await (await fetch('/api/secret-references')).json();
      return {tests: tests.status, created: created.status, missingCSRF: missingCSRF.status,
        updated: updated.status, stale: stale.status, ref, changed, catalog, shared};
    });
    assert.equal(api.tests, 200);
    assert.equal(api.created, 201);
    assert.equal(api.missingCSRF, 403);
    assert.equal(api.updated, 200);
    assert.equal(api.stale, 409);
    assert.equal(api.catalog.references.length, 1);
    assert.equal(api.catalog.references[0].id, api.ref.id);
    assert.equal(api.changed.id, api.ref.id);
    assert.equal(api.changed.enabled, false);
    assert.equal(api.changed.revision, 2);
    assert.equal(JSON.stringify(api).includes('PRIVATE_BROWSER_SOURCE'), false);
    assert.equal(JSON.stringify(api).includes('source_key'), false);
    assert.equal(api.shared.created, 201);
    assert.equal(api.shared.read, 200);
    assert.equal(api.shared.exported, 200);
    assert.equal(api.shared.copied, 201);
    assert.equal(api.shared.edited, 200);
    assert.equal(api.shared.stale, 409);
    assert.equal(api.shared.missingCSRF, 403);
    assert.equal(api.shared.readTest.document.public_config.headers.Authorization, '');
    assert.equal(api.shared.exportedDocument.secret_bindings[0].reference_id, api.ref.id);
    assert.notEqual(api.shared.copiedTest.id, api.shared.test.id);
    assert.equal(api.shared.copiedTest.source.id, api.shared.test.id);
    assert.equal(api.shared.editedTest.revision, 2);
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
    console.log('Team browser checks passed: TLS OIDC navigation, secure cookies, metadata APIs, shared authoring/copy/export, CSRF/revision checks, reload, logout, two themes and four viewport widths.');
  } finally {
    await browser.close();
  }
}
main().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
