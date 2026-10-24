'use strict';
// Two isolated browser sessions against a temporary personal workspace. No workload.
const assert=require('node:assert/strict'),fs=require('node:fs'),os=require('node:os'),path=require('node:path');
const {spawn}=require('node:child_process'),{chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
async function main(){
 const dir=fs.mkdtempSync(path.join(os.tmpdir(),'gometer-definitions-'));
 const child=spawn(process.env.GOMETER_BINARY||path.resolve(__dirname,'../../../bin/gometer'),['ui','-listen','127.0.0.1:0','-data',dir],{stdio:['ignore','pipe','pipe']});let browser;
 try{
  const url=await new Promise((resolve,reject)=>{let out='',err='';const timer=setTimeout(()=>reject(Error(err||'Startup timeout')),15000);child.stderr.on('data',c=>err+=c);child.stdout.on('data',c=>{out+=c;const m=out.match(/http:\/\/127\.0\.0\.1:\d+/);if(m){clearTimeout(timer);resolve(m[0]);}});child.once('exit',code=>{clearTimeout(timer);reject(Error('Startup exit '+code));});});
  browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});const a=await browser.newPage(),b=await browser.newPage(),errors=[];
  for(const page of [a,b])page.on('pageerror',e=>errors.push(e.message));
  await a.goto(url);await a.waitForFunction(()=>document.querySelector('#config-json').value.includes('"url"'));
  await a.locator('#test-new').click();await a.locator('#advanced').evaluate(e=>e.open=true);await a.locator('#config-json').fill(JSON.stringify({name:'Shared definition',url:'http://127.0.0.1:1',mode:'rate',rate:1,duration:'1s'}));await a.locator('#save-scenario').click();await a.waitForFunction(()=>document.querySelector('#notice').textContent.includes('Scenario saved'));
  await b.goto(url);await b.waitForFunction(()=>document.querySelector('#library').textContent.includes('Shared definition'));await b.locator('#tests-search').fill('Shared definition');await b.locator('#tests-list').getByRole('button',{name:'Shared definition',exact:true}).click();await b.locator('#test-detail').getByRole('button',{name:'Edit test',exact:true}).click();await b.waitForFunction(()=>document.querySelector('#name').value==='Shared definition');
  await a.locator('#name').fill('Latest definition');await a.locator('#save-scenario').click();await a.waitForFunction(()=>document.querySelector('#library').textContent.includes('Latest definition'));
  await b.locator('#name').fill('My conflicting draft');await b.locator('#save-scenario').click();await b.waitForFunction(()=>document.querySelector('#notice').textContent.includes('changed after you opened'));
  assert.equal(JSON.parse(await b.locator('#config-json').inputValue()).name,'My conflicting draft');
  await b.locator('#save-copy').click();await b.waitForFunction(()=>document.querySelector('#library').textContent.includes('My conflicting draft'));
  const original=a.locator('#library .library-item').filter({has:a.getByRole('button',{name:'Latest definition',exact:true})});await original.getByRole('button',{name:'Use a copy',exact:true}).click();await a.waitForFunction(()=>document.querySelector('#notice').textContent.includes('Independent test copy saved'));
  const state=await a.evaluate(async()=> (await(await api('/api/state')).json()));assert.equal(state.scenarios.length,3);assert.equal(state.runs.length,0);assert.equal(state.scenarios.filter(s=>s.source?.revision===2&&s.revision===1).length,1);
  await a.locator('#name').fill('Unfinished draft');const before=await a.locator('#config-json').inputValue();await a.locator('#library .library-item').first().getByRole('button',{name:'Use a copy',exact:true}).click();await a.waitForFunction(()=>document.querySelector('#notice').textContent.includes('Your current work has been kept'));assert.equal(await a.locator('#config-json').inputValue(),before);
  await a.locator('.sidebar [data-view=tests]').click();await a.waitForFunction(()=>document.querySelector('#tests-count').textContent==='3 of 3 tests');
  await a.locator('#test-new').click();assert.match(await a.locator('#notice').innerText(),/current draft/);assert.equal(await a.locator('#view-tests').isVisible(),true);
  await a.locator('#tests-search').fill('missing test');assert.match(await a.locator('#tests-list').innerText(),/No matching/);await a.locator('#tests-search').fill('');
  for(const theme of ['light','dark','system']){await a.locator('#theme-select').selectOption(theme);for(const width of [1440,768,390,320]){await a.setViewportSize({width,height:1000});assert.equal(await a.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,`Tests overflow at ${width} (${theme})`);}}
  if(process.env.GOMETER_TESTS_SCREENSHOT){await a.setViewportSize({width:1440,height:1000});await a.locator('#theme-select').selectOption('light');await a.locator('.sidebar [data-view=tests]').click();await a.screenshot({path:process.env.GOMETER_TESTS_SCREENSHOT,fullPage:true});}
  assert.deepEqual(errors,[]);console.log('Definition browser checks passed: two sessions, revision conflict retains draft, save-copy recovery, independent persisted copy, dirty-copy guard, and no runs created.');
 }finally{if(browser)await browser.close();if(child.exitCode===null){const exited=new Promise(resolve=>child.once('exit',resolve));child.kill('SIGTERM');await exited;}fs.rmSync(dir,{recursive:true,force:true});}
}
main().catch(e=>{console.error(e);process.exitCode=1;});
