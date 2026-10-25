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
  let debugPage;
  try {
    const context = await browser.newContext({ignoreHTTPSErrors: true});
    const page = await context.newPage(); debugPage=page;
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    page.on('console', message => {
      // These intentional negative calls have their HTTP statuses asserted below.
      const expectedAPIError = (message.location().url.startsWith(url + '/api/secret-references') ||
        message.location().url.startsWith(url + '/api/tests') ||
        message.location().url.startsWith(url + '/api/drafts')) &&
        /\b(400|403|409)\b/.test(message.text());
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
    const waitStatus = text => page.locator('#status').filter({hasText: text}).waitFor();
    const keep = async p => {
      const response = p.waitForResponse(r => /\/api\/drafts(?:\/[a-f0-9]+)?$/.test(r.url()) && ['POST','PUT'].includes(r.request().method()));
      await p.locator('#keep-draft').click();
      const result=await response;assert.ok([200,201].includes(result.status()),`write status ${result.status()}: ${await result.text()}`);
      await p.locator('#draft-status').filter({hasText:'Private draft saved'}).waitFor();
    };
    const save = async p => {
      const response=p.waitForResponse(r => /\/api\/tests(?:\/[a-f0-9]+\/document)?$/.test(r.url()) && ['POST','PUT'].includes(r.request().method()));
      await p.locator('#save-test').click();
      const result=await response;assert.ok([200,201].includes(result.status()),`write status ${result.status()}: ${await result.text()}`);
      await p.locator('#status').filter({hasText:'Test saved to the shared library'}).waitFor();
    };
    await page.setViewportSize({width:1440,height:1000});
    await page.locator('#new-test').click();
    await page.locator('#name').fill('UI checkout smoke');
    await page.locator('#url').fill('https://target.example');
    await keep(page);
    await save(page);
    await page.locator('#name').fill('UI private recovery');
    await keep(page);
    await page.reload();
    await page.getByRole('button',{name:'Resume UI private recovery',exact:true}).click();
    await page.waitForFunction(()=>document.getElementById('name').value==='UI private recovery');
    assert.equal(await page.locator('#name').inputValue(),'UI private recovery');

    const second=await context.newPage();
    second.on('pageerror',e=>errors.push(e.message));
    await second.goto(url);
    await second.locator('.test-card').filter({hasText:'UI checkout smoke'}).click();
    await second.locator('#name').fill('Teammate newer name');
    await save(second);
    await page.locator('#name').fill('Local conflict kept');
    const rejected=page.waitForResponse(r=>r.request().method()==='PUT'&&/\/document$/.test(r.url())&&r.status()===409);
    await page.locator('#save-test').click(); await rejected;
    await page.locator('#conflict').waitFor({state:'visible'});
    assert.equal(await page.locator('#name').inputValue(),'Local conflict kept');
    await page.locator('#compare').click();
    await page.locator('#compare-dialog[open]').waitFor();
    assert.ok((await page.locator('#compare-local').inputValue()).includes('Local conflict kept'));
    assert.ok((await page.locator('#compare-remote').inputValue()).includes('Teammate newer name'));
    await page.getByRole('button',{name:'Close comparison'}).click();
    await page.locator('#save-new').click();await waitStatus('Test saved to the shared library');
    const names=await page.evaluate(async()=> (await(await fetch('/api/tests')).json()).tests.map(t=>t.name));
    assert.ok(names.includes('Local conflict kept')&&names.includes('Teammate newer name'));

    await page.locator('#name').fill('Cross tab draft'); await keep(page);
    await second.reload(); await second.getByRole('button',{name:'Resume Cross tab draft',exact:true}).click();
    await second.waitForFunction(()=>document.getElementById('name').value==='Cross tab draft');
    await page.locator('#name').fill('First tab draft'); await keep(page);
    await second.locator('#name').fill('Second tab draft');
    const rejectedDraft=second.waitForResponse(r=>r.request().method()==='PUT'&&/\/api\/drafts\//.test(r.url())&&r.status()===409);
    await second.locator('#keep-draft').click(); await rejectedDraft;
    await second.locator('#fork-draft').waitFor({state:'visible'}); await second.locator('#fork-draft').click();
    await second.locator('#draft-status').filter({hasText:'Private draft saved'}).waitFor();
    const drafts=await second.evaluate(async()=> (await(await fetch('/api/drafts')).json()).drafts.map(d=>d.name));
    assert.ok(drafts.includes('First tab draft')&&drafts.includes('Second tab draft'));
    await second.close();

    // Invalid unfinished text is kept in the tab and blocks navigation rather
    // than being copied to durable drafts or silently discarded.
    const original=await page.locator('#public-json').inputValue();
    await page.locator('#advanced>summary').click();
    await page.locator('#public-json').fill('{');
    await page.locator('#new-test').click();
    await page.getByRole('button',{name:'Keep draft and continue',exact:true}).click();
    await page.locator('#status.error').waitFor();
    assert.equal(await page.locator('#public-json').inputValue(),'{');
    await page.locator('#public-json').fill(original); await keep(page);

    // Restore a public-only journey and verify real canvas editing. A bound
    // journey remains a preview so positional credentials cannot move silently.
    await page.locator('#new-test').click();
    await page.locator('#name').fill('UI journey design');
    await page.locator('#url').fill('https://target.example');
    await page.locator('#journey-section>summary').click();
    await page.locator('#journey').getByRole('button',{name:'Create journey from current request'}).click();
    await page.locator('#journey .je-node').first().click();
    const stepURL=page.locator('#journey [data-field="url"]');
    await stepURL.fill('/checkout');await stepURL.blur();
    await keep(page); await save(page);
    assert.ok(JSON.parse(await page.locator('#public-json').inputValue()).scenarios.length>0);

    const enabledRef=await page.evaluate(async()=>{
      const session=await(await fetch('/api/session')).json();
      const response=await fetch('/api/secret-references',{method:'POST',headers:{'Content-Type':'application/json','X-Gometer-CSRF':session.csrf_token},body:JSON.stringify({name:'UI credential',source_key:'PRIVATE_UI_SOURCE'})});
      if(response.status!==201)throw Error('UI reference fixture failed');return response.json();
    });
    await page.locator('#refresh').click();
    await page.locator('#status').filter({hasText:'Library and references refreshed'}).waitFor();
    await page.locator('#add-binding').click();
    await page.locator('#bindings select').selectOption(`${enabledRef.id}/${enabledRef.revision}`);
    await keep(page);await save(page);
    assert.equal(await page.locator('#journey').getByRole('button',{name:'Save test',exact:true}).count(),0);
    assert.ok((await page.locator('#journey .je-node').first().getAttribute('aria-label')).startsWith('Inspect'));
    assert.equal((await page.locator('#public-json').inputValue()).includes('PRIVATE_UI_SOURCE'),false);
    await page.locator('#sidebar-toggle').click();
    assert.equal(await page.locator('#sidebar-toggle').getAttribute('aria-expanded'),'false');
    await page.locator('#sidebar-toggle').click();
    await page.locator('#theme').selectOption('light');
    assert.equal(await page.locator('html').getAttribute('data-theme'),'light');
    await page.locator('#theme').selectOption('dark');
    assert.equal(await page.locator('html').getAttribute('data-theme'),'dark');
    await page.locator('#theme').selectOption('system');
    for (const colorScheme of ['light','dark']) {
      await page.emulateMedia({colorScheme});
      for (const width of [1440,768,390,320]) {
        await page.setViewportSize({width,height:1000});
        assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,`workspace overflow: ${colorScheme}/${width}`);
      }
    }
    const download=page.waitForEvent('download');await page.locator('#export-test').click();
    assert.equal((await download).suggestedFilename(),'shared-test.json');
    const browserData=await page.evaluate(()=>({local:Object.keys(localStorage),session:Object.keys(sessionStorage)}));
    assert.deepEqual(browserData,{local:[],session:[]});

    const viewerContext=await browser.newContext({ignoreHTTPSErrors:true});
    await viewerContext.addCookies([{name:'__Host-gometer-session',value:process.env.GOMETER_TEAM_VIEWER_TOKEN,url,httpOnly:true,secure:true,sameSite:'Lax'}]);
    const viewerPage=await viewerContext.newPage();viewerPage.on('pageerror',e=>errors.push(e.message));
    await viewerPage.goto(url);await viewerPage.locator('#status').filter({hasText:'Viewer access'}).waitFor();
    assert.equal(await viewerPage.locator('#new-test').isDisabled(),true);
    await viewerPage.locator('.test-card').filter({hasText:'UI journey design'}).click();
    assert.equal(await viewerPage.locator('#save-test').isDisabled(),true);
    assert.equal(await viewerPage.locator('#draft-count').textContent(),'0');
    await viewerPage.waitForFunction(()=>!document.getElementById('export-test').disabled);
    assert.equal(await viewerPage.locator('#export-test').isEnabled(),true);
    assert.equal(await viewerPage.locator('#journey').getByRole('button',{name:'Start run',exact:true}).count(),0);
    await viewerContext.close();
    // A tab opened by one actor must hide its private draft after the cookie
    // belongs to another actor, even if a previous request lost its session.
    const swapped=await browser.newContext({ignoreHTTPSErrors:true});
    await swapped.addCookies(await context.cookies());
    const swappedPage=await swapped.newPage();await swappedPage.goto(url);
    let releaseDraft;
    const held=new Promise(resolve=>{releaseDraft=resolve;});
    let markHeld;const authorized=new Promise(resolve=>{markHeld=resolve;});
    await swappedPage.route('**/api/drafts/*',async route=>{
      const response=await route.fetch();markHeld();await held;await route.fulfill({response});
    });
    await swappedPage.getByRole('button',{name:'Resume First tab draft',exact:true}).click();
    await authorized;
    await swapped.addCookies([{name:'__Host-gometer-session',value:process.env.GOMETER_TEAM_VIEWER_TOKEN,url,httpOnly:true,secure:true,sameSite:'Lax'}]);
    await swappedPage.evaluate(()=>window.dispatchEvent(new Event('focus')));
    await swappedPage.locator('#status').filter({hasText:'account changed'}).waitFor();
    const lateDraft=swappedPage.waitForResponse(r=>/\/api\/drafts\//.test(r.url()));releaseDraft();await lateDraft;
    await swappedPage.locator('#status').filter({hasText:'account changed'}).waitFor();
    assert.equal(await swappedPage.locator('#editor').isHidden(),true);
    assert.equal(await swappedPage.locator('#public-json').inputValue(),'');
    await swapped.close();

    const cookies = await context.cookies();
    const session = cookies.find(cookie => cookie.name === '__Host-gometer-session');
    assert.ok(session?.secure && session?.httpOnly && session?.sameSite === 'Lax');
    assert.equal(cookies.some(cookie => cookie.name === '__Host-gometer-login'), false);
    await page.reload();
    await page.getByRole('heading', {name: 'Welcome, Alice Team'}).waitFor();
    if (process.env.GOMETER_TEAM_SCREENSHOT) {
      await page.setViewportSize({width: 1440, height: 900});
      await page.emulateMedia({colorScheme: 'light'});
      await page.locator('.test-card').filter({hasText:'UI journey design'}).click();
      await page.waitForFunction(()=>document.getElementById('name').value==='UI journey design');
      await page.locator('#journey-section>summary').click();
      await page.locator('#journey .je-node').first().waitFor();
      await page.locator('#journey').screenshot({path:process.env.GOMETER_TEAM_SCREENSHOT.replace(/\.png$/, '-canvas.png')});
      await page.evaluate(()=>window.scrollTo(0,0));
      await page.screenshot({path: process.env.GOMETER_TEAM_SCREENSHOT, fullPage: true});
    }
    await page.getByRole('button', {name: 'Sign out of GoMeter'}).click();
    await page.getByRole('heading', {name: 'Sign in to your workspace'}).waitFor();
    assert.equal((await context.cookies()).some(cookie => cookie.name === '__Host-gometer-session'), false);
    assert.deepEqual(errors, []);
    console.log('Team browser checks passed: TLS OIDC, shared Tests UI, private draft recovery, two-tab test/draft conflicts, public canvas edits, viewer controls, copy/export, CSRF, reload/logout, two themes and four widths.');
  } catch(error) {
    await debugPage?.screenshot({path:'/private/tmp/gometer-team-tests-failure.png',fullPage:true}).catch(()=>{});
    throw error;
  } finally {
    await browser.close();
  }
}
main().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
