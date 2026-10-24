'use strict';

// Real runs against a counted local fixture, using a disposable data directory.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const {spawn} = require('node:child_process');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const repo = path.resolve(__dirname, '../../..');

async function main() {
  const workspace = fs.mkdtempSync(path.join(os.tmpdir(),'gometer-results-'));
  let browser, dashboard, attempts=0, failures=0, stderr='';
  const target=http.createServer((req,res)=>{
    const index=++attempts;
    const failed=req.url==='/mixed' && index%7===0;
    if(failed)failures++;
    setTimeout(()=>{res.statusCode=failed?503:200;res.end('fixture');},5+(index%5)*8);
  });
  try {
    await new Promise(resolve=>target.listen(0,'127.0.0.1',resolve));
    dashboard=spawn(process.env.GOMETER_BINARY || path.join(repo,'bin/gometer'),['ui','-listen','127.0.0.1:0','-data',workspace],{stdio:['ignore','pipe','pipe']});
    dashboard.stderr.on('data',chunk=>{stderr+=chunk;});
    const url=await new Promise((resolve,reject)=>{
      let output=''; const timer=setTimeout(()=>reject(new Error(`Startup timed out: ${stderr}`)),15000);
      dashboard.stdout.on('data',chunk=>{output+=chunk;const match=output.match(/http:\/\/127\.0\.0\.1:\d+/);if(match){clearTimeout(timer);resolve(match[0]);}});
      dashboard.once('exit',code=>{clearTimeout(timer);reject(new Error(`Dashboard exited ${code}: ${stderr}`));});
    });
    browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
    const page=await browser.newPage({viewport:{width:1440,height:1100}}), errors=[];
    page.on('pageerror',error=>errors.push(error.message));
    page.on('console',message=>{if(message.type()==='error')errors.push(message.text());});
    await page.goto(url);await page.waitForFunction(()=>document.querySelector('#config-json').value.includes('"url"'));
    async function reopenSavedRun(targetPage,name='Result visualization fixture',status='FAILED') {
      if(await targetPage.locator('#navigation-open').isVisible())await targetPage.locator('#navigation-open').click();
      await targetPage.locator('.sidebar [data-view="history"]').click();
      await targetPage.locator('#history tbody tr').filter({hasText:name}).getByRole('button',{name:'View',exact:true}).click();
      await targetPage.waitForFunction(expected=>document.querySelector('#run-status').textContent===expected,status);
    }
    async function start(config) {
      if(await page.locator('#navigation-open').isVisible())await page.locator('#navigation-open').click();
      await page.locator('.sidebar [data-view="tests"]').click();await page.locator('#test-resume').click();
      await page.locator('#builder-settings-tab').click();
      await page.locator('#advanced').evaluate(e=>{e.open=true;});
      await page.locator('#config-json').fill(JSON.stringify(config));
      await page.locator('#config-json').dispatchEvent('change');
      await page.locator('#start').click();
      await page.locator('#run-content').waitFor({state:'visible'});
    }
    const config={name:'Result visualization fixture',url:`http://127.0.0.1:${target.address().port}/mixed`,mode:'users',vus:2,iterations:30,startup_delay:'1500ms',max_in_flight:4,timeout:'1s',diagnostics:true,thresholds:{p95:'200ms',max_error_percent:0,max_drop_percent:0}};
    await start(config);
    await page.waitForFunction(()=>document.querySelector('#run-status').textContent==='RUNNING');
    assert.match(await page.locator('#result-overview').innerText(),/No completed latency samples/);
    await page.waitForFunction(()=>document.querySelector('#run-status').textContent==='FAILED',null,{timeout:20000});
    const run=await page.evaluate(()=>({id:current.id,result:current.result,samples:current.samples}));
    assert.equal(run.result.completed,60);assert.equal(attempts,60);assert.equal(run.result.failed,failures);assert.ok(failures>0);
    assert.match(await page.locator('#checks .rv-check').first().innerText(),/Failed.*Request errors/s);
    assert.match(await page.locator('#checks').innerText(),/≤ 200 ms/);
    assert.match(await page.locator('.rv-outcome-list').innerText(),new RegExp(`Failed\\s+${failures}`));
    assert.equal(await page.locator('.rv-percentiles>div').count(),3);
    const chart=page.locator('#latency-chart');await chart.focus();await chart.press('End');
    const last=run.samples.at(-1), lastLabel=Number(last.requests.latency.p99_ms).toLocaleString('en-US',{maximumFractionDigits:3});
    assert.match(await page.locator('#latency-chart-reading').textContent(),new RegExp(`p99: ${lastLabel.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')} ms`));
    const end=await page.locator('#latency-chart-reading').textContent();await chart.press('Home');
    assert.match(await page.locator('#latency-chart-reading').textContent(),/No sample/);
    await chart.press('ArrowRight');assert.notEqual(await page.locator('#latency-chart-reading').textContent(),end);
    await chart.press('Escape');assert.equal(await page.locator('#latency-chart-reading').textContent(),'');
    const section=chart.locator('xpath=../..'), toggle=section.getByRole('button',{name:'Show p99',exact:true});
    await toggle.click();assert.equal(await toggle.getAttribute('aria-pressed'),'false');
    await chart.focus();await chart.press('End');assert.doesNotMatch(await page.locator('#latency-chart-reading').textContent(),/p99:/);
    await toggle.click();assert.equal(await toggle.getAttribute('aria-pressed'),'true');
    for(const theme of ['light','dark','system']) {
      await page.locator('#theme-select').selectOption(theme);
      for(const width of [1440,768,390,320]) {
        await page.setViewportSize({width,height:1100});
        await chart.scrollIntoViewIfNeeded();
        const rect=await chart.boundingBox();await page.mouse.move(rect.x+rect.width/2,rect.y+rect.height/2);
        assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,`Results overflow at ${width}px (${theme})`);
        assert.equal(await section.locator('.rv-chart-tooltip').isVisible(),true);
      }
    }
    const touch=await browser.newContext({viewport:{width:390,height:900},hasTouch:true,isMobile:true});
    const mobile=await touch.newPage();await mobile.goto(url);
    await reopenSavedRun(mobile);
    await mobile.locator('#traffic-chart').tap();assert.match(await mobile.locator('#traffic-chart-reading').textContent(),/elapsed/);
    await touch.close();
    // The same saved run still provides exact values after reload and view changes.
    await page.setViewportSize({width:1440,height:1100});await page.locator('#theme-select').selectOption('light');
    await page.reload();await reopenSavedRun(page);
    await page.locator('#latency-chart').focus();await page.locator('#latency-chart').press('End');
    assert.match(await page.locator('#latency-chart-reading').textContent(),/p99:/);
    if(process.env.GOMETER_RESULTS_FAILED_SCREENSHOT)await page.screenshot({path:process.env.GOMETER_RESULTS_FAILED_SCREENSHOT,fullPage:true});
    // A passing rate run provides an independent check of delivery and demand units.
    await start({...config,name:'Local API baseline',url:config.url.replace('/mixed','/work'),mode:'rate',rate:20,duration:'3s',vus:undefined,iterations:undefined,startup_delay:undefined,thresholds:{p95:'200ms',max_error_percent:0,max_drop_percent:0,min_started_rps:19}});
    await page.waitForFunction(()=>document.querySelector('#run-title').textContent==='Local API baseline'&&document.querySelector('#run-status').textContent==='PASSED',null,{timeout:20000});
    assert.equal(attempts,120);
    assert.match(await page.locator('#checks-summary').innerText(),/6 passed · 0 failed/);
    await page.locator('#traffic-chart').focus();await page.locator('#traffic-chart').press('End');
    assert.match(await page.locator('#traffic-chart-reading').textContent(),/req\/s/);
    if(process.env.GOMETER_RESULTS_SCREENSHOT){await reopenSavedRun(page,'Local API baseline','PASSED');await page.setViewportSize({width:1440,height:1360});await page.locator('#run-title').click();await page.evaluate(()=>scrollTo(0,0));await page.screenshot({path:process.env.GOMETER_RESULTS_SCREENSHOT,fullPage:process.env.GOMETER_RESULTS_FULL_PAGE==='1',type:process.env.GOMETER_RESULTS_SCREENSHOT.endsWith('.jpg')?'jpeg':'png',...(process.env.GOMETER_RESULTS_SCREENSHOT.endsWith('.jpg')?{quality:90}:{})});}
    assert.deepEqual(errors,[]);
    console.log('Result browser checks passed: 120 counted HTTP attempts, live empty state, failed/passed checks, exact p99 readouts, keyboard/mouse/touch inspection, series toggles, reload, three themes and four widths.');
  } finally {
    if(browser)await browser.close();
    if(dashboard&&dashboard.exitCode===null){dashboard.kill('SIGTERM');await new Promise(resolve=>dashboard.once('exit',resolve));}
    await new Promise(resolve=>target.close(resolve));
    fs.rmSync(workspace,{recursive:true,force:true});
  }
}
main().catch(error=>{console.error(error);process.exitCode=1;});
