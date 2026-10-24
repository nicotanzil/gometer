'use strict';
// Disposable local workers, dashboard, workspace, and counted target only.
const assert=require('node:assert/strict'),fs=require('node:fs'),os=require('node:os'),path=require('node:path'),http=require('node:http'),crypto=require('node:crypto');
const {spawn}=require('node:child_process');
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const repo=path.resolve(__dirname,'../../..'),binary=process.env.GOMETER_BINARY||path.join(repo,'bin/gometer');
async function main(){
 const workspace=fs.mkdtempSync(path.join(os.tmpdir(),'gometer-workers-')),token=crypto.randomBytes(32).toString('hex'),processes=[];
 let browser,attempts=0;
 const target=http.createServer((req,res)=>{attempts++;res.end('fixture');});
 async function launch(args){
  const child=spawn(binary,args,{env:{...process.env,GOMETER_WORKER_TOKEN:token},stdio:['ignore','pipe','pipe']});processes.push(child);
  const url=await new Promise((resolve,reject)=>{let output='',error='';const timer=setTimeout(()=>reject(new Error('Startup timed out: '+error)),15000);child.stderr.on('data',c=>error+=c);child.stdout.on('data',c=>{output+=c;const match=output.match(/http:\/\/127\.0\.0\.1:\d+/);if(match){clearTimeout(timer);resolve(match[0]);}});child.once('exit',code=>{clearTimeout(timer);reject(new Error(`Startup exit ${code}: ${error}`));});});
  return {child,url};
 }
 async function stop(child){if(child.exitCode!==null)return;const exited=new Promise(resolve=>child.once('exit',resolve));child.kill('SIGTERM');await exited;}
 try{
  await new Promise(resolve=>target.listen(0,'127.0.0.1',resolve));
  const a=await launch(['worker','-listen','127.0.0.1:0','-name','fixture-east']),b=await launch(['worker','-listen','127.0.0.1:0','-name','fixture-west']);
  let dashboard=await launch(['ui','-listen','127.0.0.1:0','-data',workspace]);
  browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
  const page=await browser.newPage({viewport:{width:1440,height:1100}}),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(dashboard.url);await page.waitForFunction(()=>document.querySelector('#config-json').value.includes('"url"'));
  async function navigate(view){if(await page.locator('#navigation-open').isVisible())await page.locator('#navigation-open').click();await page.locator(`.sidebar [data-view="${view==='build'?'tests':view}"]`).click();if(view==='build')await page.locator('#test-resume').click();}
  async function config(c){await navigate('build');await page.locator('#builder-settings-tab').click();await page.locator('#advanced').evaluate(e=>e.open=true);await page.locator('#config-json').fill(JSON.stringify(c));}
  async function json(url,method='GET',body){return page.evaluate(async({url,method,body})=>{const r=await fetch(url,{method,headers:{'X-Gometer-Token':document.querySelector('meta[name=gometer-token]').content,'Content-Type':'application/json'},...(body!==undefined?{body:JSON.stringify(body)}:{})});return {code:r.status,data:await r.json()};},{url,method,body});}
  const c={name:'Worker catalog verification',url:`http://127.0.0.1:${target.address().port}/work`,mode:'rate',rate:20,duration:'1s',timeout:'1s',max_in_flight:4,thresholds:{max_error_percent:0,max_drop_percent:0}};
  await config(c);
  await navigate('workers');await page.locator('#worker-name').fill('East generator');await page.locator('#worker-origin').fill(a.url+'/');await page.locator('#worker-save').click();
  await page.waitForFunction(()=>document.querySelector('#worker-list').textContent.includes('East generator'));
  assert.equal(attempts,0);assert.match(await page.locator('#worker-list').innerText(),/Not checked/);
  await page.getByRole('button',{name:'Check connection',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#worker-message').textContent.includes('Reachable'));
  assert.equal(attempts,0,'Connection check sent workload');
  const first=(await json('/api/workers')).data[0];assert.ok(first.check.checked_at);assert.equal(first.check.state,'reachable');
  // A second browser reads the shared server-side catalog, independent of browser storage.
  const other=await browser.newPage();await other.goto(dashboard.url);await other.waitForFunction(()=>document.querySelector('#config-json').value.includes('"url"'));
  await other.locator('.sidebar [data-view=workers]').click();assert.match(await other.locator('#worker-list').innerText(),/East generator/);await other.close();
  await navigate('build');await page.locator('#execution-settings').evaluate(e=>e.open=true);await page.locator('#execution-workers').check();await page.locator('#validate').click();
  assert.match(await page.locator('#validation').innerText(),/Select at least one worker/);assert.equal(attempts,0);
  await page.locator(`[data-worker="${first.id}"]`).check();assert.deepEqual(JSON.parse(await page.locator('#config-json').inputValue()).workers,[a.url]);
  await page.locator('#validate').click();await page.waitForFunction(()=>document.querySelector('#validation').textContent.includes('Valid'));
  assert.equal(attempts,0);
  // Import a legacy origin: it stays selected, and registration is a deliberate action.
  await config({...c,workers:[a.url,b.url]});assert.match(await page.locator('#worker-unregistered').innerText(),new RegExp(b.url));
  assert.equal((await json('/api/workers')).data.length,1);
  await page.locator('#worker-unregistered').getByRole('button',{name:'Register',exact:true}).click();await page.locator('#worker-name').fill('West generator');await page.locator('#worker-save').click();
  await page.waitForFunction(()=>document.querySelector('#worker-list').textContent.includes('West generator'));
  if(process.env.GOMETER_WORKERS_SCREENSHOT)await page.screenshot({path:process.env.GOMETER_WORKERS_SCREENSHOT,fullPage:true,...(process.env.GOMETER_WORKERS_SCREENSHOT.endsWith('.jpg')?{type:'jpeg',quality:90}:{})});
  if(process.env.GOMETER_WORKERS_NARROW_SCREENSHOT){await page.setViewportSize({width:320,height:1000});await page.locator('#worker-list').scrollIntoViewIfNeeded();await page.screenshot({path:process.env.GOMETER_WORKERS_NARROW_SCREENSHOT});await page.setViewportSize({width:1440,height:1100});}
  await navigate('build');assert.equal(await page.locator('#worker-unregistered').textContent(),'');assert.deepEqual(JSON.parse(await page.locator('#config-json').inputValue()).workers,[a.url,b.url]);
  await page.locator('#save-scenario').click();await page.waitForFunction(()=>document.querySelector('#notice').textContent.includes('saved'));
  const saved=(await json('/api/state')).data.scenarios[0];assert.ok(saved.id);
  // A run freezes names and origins before subsequent catalog edits/removal.
  await page.locator('#start').click();await page.waitForFunction(()=>!document.querySelector('#view-live').hidden);
  const runID=(await json('/api/state')).data.current.id;
  await navigate('workers');const east=page.locator('#worker-list tr').filter({hasText:'East generator'});await east.getByRole('button',{name:'Edit',exact:true}).click();await page.locator('#worker-name').fill('Renamed generator');await page.locator('#worker-save').click();await page.waitForFunction(()=>document.querySelector('#worker-list').textContent.includes('Renamed generator'));
  page.once('dialog',d=>d.accept());await page.locator('#worker-list tr').filter({hasText:'Renamed generator'}).getByRole('button',{name:'Remove',exact:true}).click();await page.waitForFunction(()=>!document.querySelector('#worker-list').textContent.includes('Renamed generator'));
  let run;const deadline=Date.now()+20000;while(Date.now()<deadline){run=(await json('/api/runs/'+runID)).data;if(!['running','stopping'].includes(run.status))break;await new Promise(resolve=>setTimeout(resolve,100));}
  assert.equal(run.status,'passed',run.error);assert.equal(run.result.counts?.completed??run.result.completed,20);assert.equal(attempts,20);assert.deepEqual(run.config.workers,[a.url,b.url]);assert.deepEqual(run.worker_snapshots.map(w=>w.name),['East generator','West generator']);
  const imported=await json('/api/runs/import','POST',run);assert.equal(imported.code,201);assert.equal((await json('/api/runs/'+imported.data.id)).data.worker_snapshots[0].name,'East generator');
  const bad=structuredClone(run);bad.worker_snapshots[0].origin=b.url;assert.equal((await json('/api/runs/import','POST',bad)).code,400);
  const old=structuredClone(run);delete old.worker_snapshots;assert.equal((await json('/api/runs/import','POST',old)).code,201);
  await navigate('history');await page.waitForFunction(()=>document.querySelector('#history').textContent.includes('East generator'));assert.match(await page.locator('#history').innerText(),/East generator/);await page.locator('#history tr').filter({hasText:'Worker catalog verification'}).first().getByRole('button',{name:'Reuse',exact:true}).click();assert.deepEqual(JSON.parse(await page.locator('#config-json').inputValue()).workers,[a.url,b.url]);assert.match(await page.locator('#worker-unregistered').innerText(),new RegExp(a.url));
  await config({...c,mode:'users',rate:0,vus:1,iterations:1,workers:[b.url]});assert.equal(await page.locator('#worker-mode-warning').isVisible(),true);await page.locator('#validate').click();await page.waitForFunction(()=>document.querySelector('#validation').textContent.includes('distributed runs require rate mode'));assert.deepEqual(JSON.parse(await page.locator('#config-json').inputValue()).workers,[b.url]);
  // An unavailable selection fails preflight instead of sending local traffic.
  const closed=http.createServer();await new Promise(resolve=>closed.listen(0,'127.0.0.1',resolve));const unavailable='http://127.0.0.1:'+closed.address().port;await new Promise(resolve=>closed.close(resolve));
  const failed=await json('/api/runs','POST',{...c,workers:[unavailable]});assert.equal(failed.code,202);let unavailableRun;
  for(let i=0;i<100;i++){unavailableRun=(await json('/api/runs/'+failed.data.id)).data;if(!['running','stopping'].includes(unavailableRun.status))break;await new Promise(resolve=>setTimeout(resolve,50));}
  assert.equal(unavailableRun.status,'error');assert.equal(attempts,20,'Unavailable worker fell back to local execution');
  // Stored origins survive restart; status observations remain explicitly dated.
  await stop(dashboard.child);dashboard=await launch(['ui','-listen','127.0.0.1:0','-data',workspace]);await page.goto(dashboard.url);await page.waitForFunction(()=>document.querySelector('#config-json').value.includes('"url"'));
  await navigate('workers');assert.match(await page.locator('#worker-list').innerText(),/West generator/);assert.equal((await json('/api/workers')).data.length,1);
  for(const theme of ['light','dark','system']){await page.locator('#theme-select').selectOption(theme);for(const width of [1440,768,390,320]){await page.setViewportSize({width,height:1000});await navigate('workers');assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,`Workers overflow at ${width} (${theme})`);await navigate('build');await page.locator('#execution-settings').evaluate(e=>e.open=true);await page.locator('#execution-workers').check();assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,`Builder overflow at ${width} (${theme})`);}}
  await page.setViewportSize({width:1440,height:1000});await navigate('workers');await page.locator('#sidebar-toggle').click();await page.locator('.sidebar [data-view=workers]').focus();await page.locator('#navigation-tooltip').waitFor({state:'visible'});assert.equal(await page.locator('#navigation-tooltip').innerText(),'Workers');await page.keyboard.press('Escape');
  assert.deepEqual(errors,[]);assert.equal(attempts,20);console.log('Worker browser checks passed: persistent shared catalog, manual no-work check, strict selection, legacy registration, 20 counted distributed requests, frozen identity, import/reuse, restart, themes and responsive navigation.');
 }finally{if(browser)await browser.close();for(const child of processes.reverse())await stop(child);await new Promise(resolve=>target.close(resolve));fs.rmSync(workspace,{recursive:true,force:true});}
}
main().catch(error=>{console.error(error);process.exitCode=1;});
