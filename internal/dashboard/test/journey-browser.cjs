'use strict';

// Optional integration suite: a disposable dashboard, browser, and counted local target.
// No user workspace, browser profile, credentials, or external target is used.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const {spawn} = require('node:child_process');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const repo = path.resolve(__dirname, '../../..');

async function main() {
  const workspace = fs.mkdtempSync(path.join(os.tmpdir(), 'gometer-canvas-'));
  let attempts = 0, dashboard, browser;
  const target = http.createServer(async (req, res) => {
    attempts++;
    let raw = ''; for await (const part of req) raw += part;
    const body = raw ? JSON.parse(raw) : {};
    res.setHeader('Content-Type', 'application/json');
    if (req.url === '/login') {
      const token = Buffer.from(body.username).toString('base64url');
      res.setHeader('Set-Cookie', `gometer_session=${token}; Path=/; HttpOnly`);
      res.setHeader('X-Demo-Session', token);
      res.end(JSON.stringify({token, user:{id:body.username}})); return;
    }
    const token = req.headers.authorization?.replace(/^Bearer /, '');
    if (!token || !req.headers.cookie?.includes(`gometer_session=${token}`)) { res.statusCode=401; res.end('{}'); return; }
    if (req.url.startsWith('/profile')) res.end(JSON.stringify({authenticated:true,user:{id:Buffer.from(token,'base64url').toString()}}));
    else res.end(JSON.stringify({ok:true,item:body.item,order_id:'test-order'}));
  });
  try {
    await new Promise(resolve => target.listen(0, '127.0.0.1', resolve));
    dashboard = spawn(path.join(repo,'bin/gometer'), ['ui','-listen','127.0.0.1:0','-data',workspace], {stdio:['ignore','pipe','pipe']});
    const url = await new Promise((resolve,reject) => {
      const timeout=setTimeout(()=>reject(new Error('Dashboard startup timed out.')),15000);
      let output=''; dashboard.stdout.on('data',chunk=>{output+=chunk;const match=output.match(/http:\/\/127\.0\.0\.1:\d+/);if(match){clearTimeout(timeout);resolve(match[0]);}});
      dashboard.once('exit',code=>{clearTimeout(timeout);reject(new Error(`Dashboard exited: ${code}`));});
    });
    browser = await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})});
    const page = await browser.newPage({viewport:{width:1440,height:1100}}), errors=[], failedResponses=[], apiWrites=[];
    page.on('pageerror',e=>errors.push(e.message));
    page.on('console',m=>{if(m.type()==='error'&&!m.text().startsWith('Failed to load resource:'))errors.push(m.text());});
    page.on('response',r=>{if(r.status()>=400)failedResponses.push([new URL(r.url()).pathname,r.status()]);});
    page.on('request',r=>{if(r.method()==='POST')apiWrites.push(new URL(r.url()).pathname);});
    await page.goto(url); await page.waitForFunction(()=>document.querySelector('#config-json').value.includes('"url"'));
    await require('./navigation-browser.cjs')(page);
    const canvas=page.locator('#journey-canvas');
    const config = JSON.parse(fs.readFileSync(path.join(repo,'examples/journey.json'),'utf8'));
    config.url=`http://127.0.0.1:${target.address().port}`;
    const read = () => page.locator('#config-json').inputValue().then(JSON.parse);
    async function load(c) {
      await page.locator('#builder-settings-tab').click();
      await page.locator('#advanced').evaluate(e=>{e.open=true;});
      await page.locator('#config-json').fill(JSON.stringify(c,null,2));
      await page.locator('#builder-journey-tab').click();
    }
    async function edit(label,value) {const input=canvas.getByLabel(label,{exact:true});await input.fill(value);await input.press('Tab');}
    async function request(name) {await canvas.locator('.je-outline-step').filter({hasText:name}).click();}
    const action = name => canvas.getByRole('button',{name,exact:true});
    await load({name:'Convert a POST request',url:config.url+'/login',method:'POST',body:'{"username":"alice"}',headers:{'Content-Type':'application/json'},mode:'rate',rate:1,duration:'1s',max_in_flight:2});
    await action('Create journey from current request').click();await action('Validate').click();
    await page.waitForFunction(()=>document.querySelector('#journey-canvas .je-message').textContent.startsWith('Valid configuration'));
    assert.equal((await read()).scenarios[0].steps[0].method,'POST');
    assert.equal((await read()).method,'GET');assert.equal((await read()).body,undefined);
    await load(config);
    assert.equal(await canvas.locator('.je-node').count(),3);
    await canvas.getByLabel('Data flow',{exact:true}).check();
    assert.equal(await canvas.locator('.je-data-edge').count(),2);
    await action('Preview sequence').click(); await action('Preview failure here').click();
    assert.match(await canvas.locator('.je-preview').innerText(),/Profile \[skipped\]/);
    await request('Profile'); await action('Move up').click();
    assert.equal((await read()).scenarios[0].steps[0].name,'Profile');
    assert.match(await canvas.locator('.je-footer').innerText(),/needs \{\{token\}\}/);
    await action('Validate').click();
    await page.waitForFunction(()=>document.querySelector('#journey-canvas .je-message').classList.contains('je-bad'));
    await action('Undo').click();
    assert.equal((await read()).scenarios[0].steps[0].name,'Login');
    await canvas.locator('.je-outline-step').filter({hasText:'Login'}).dragTo(canvas.locator('.je-outline-step').filter({hasText:'Checkout'}));
    assert.deepEqual((await read()).scenarios[0].steps.map(s=>s.name),['Profile','Checkout','Login']);
    await action('Undo').click();
    await request('Login');
    await canvas.getByLabel('Step name',{exact:true}).fill('Temporary');
    await canvas.getByLabel('Step name',{exact:true}).fill('Login');
    await canvas.getByLabel('Step name',{exact:true}).press('Tab');
    await edit('Pause after success','20ms');
    assert.equal((await read()).scenarios[0].steps[0].pause,'20ms');
    await action('+ Think time').click();
    assert.equal((await read()).scenarios[0].steps[0].pause,'20ms','Adding think time must preserve an existing pause.');
    assert.equal(await canvas.locator('.je-pause').count(),1);
    await action('Variables').click();
    assert.equal(await canvas.locator('.je-rule').count(),2);
    await action('+ Add extraction').click();
    await action('Save scenario').click();
    assert.match(await canvas.locator('.je-message').innerText(),/Name the new extraction/);
    const extraction=canvas.locator('.je-rule').last();
    await extraction.getByLabel('Variable name',{exact:true}).fill('root_value');
    await extraction.getByLabel('Variable name',{exact:true}).press('Tab');
    await extraction.getByLabel('Selector',{exact:true}).fill('');
    await extraction.getByLabel('Selector',{exact:true}).press('Tab');
    assert.deepEqual((await read()).scenarios[0].steps[0].extract.root_value,{json_pointer:''});
    await extraction.getByLabel('Selector',{exact:true}).fill('/user/id');
    await extraction.getByLabel('Selector',{exact:true}).press('Tab');
    await action('Checks').click();
    await action('+ Add assertion').click();
    const assertion=canvas.locator('.je-rule').last();
    await assertion.getByLabel('Check type',{exact:true}).selectOption('body_contains');
    await assertion.getByLabel('Selector',{exact:true}).fill('token');
    await assertion.getByLabel('Selector',{exact:true}).press('Tab');
    assert.deepEqual((await read()).scenarios[0].steps[0].assertions.at(-1),{body_contains:'token'});
    await action('Journey settings & data').click();
    await edit('Dataset rows · JSON','{unfinished');
    const beforeInvalidActions=apiWrites.length;
    await action('Save scenario').click();
    await action('Validate').click();await action('Start run').click();
    assert.match(await canvas.locator('.je-message').innerText(),/JSON|position|property/i);
    // Switching the authoring tab cannot discard an invalid dataset draft.
    await page.locator('#builder-settings-tab').click();await page.locator('#builder-journey-tab').click();
    assert.equal(apiWrites.length,beforeInvalidActions,'Unfinished drafts cannot validate, save, or start a serialized older test.');
    assert.equal(await canvas.getByLabel('Dataset rows · JSON',{exact:true}).inputValue(),'{unfinished');
    await page.locator('#sidebar-toggle').click();
    assert.equal(await canvas.getByLabel('Dataset rows · JSON',{exact:true}).inputValue(),'{unfinished','Collapsing navigation discarded the inspector draft.');
    await page.locator('#sidebar-toggle').click();
    await edit('Dataset rows · JSON',JSON.stringify(config.scenarios[0].data));
    await edit('Mix weight','3');
    assert.equal((await read()).scenarios[0].weight,3);
    const minimum=canvas.getByLabel('Minimum started journeys / s',{exact:true});
    await minimum.fill('');await minimum.pressSequentially('e');await minimum.press('Tab');
    assert.match(await canvas.locator('.je-message').innerText(),/number/);
    await minimum.fill('');await minimum.press('Tab');
    await action('Validate').click();
    await page.waitForFunction(()=>document.querySelector('#journey-canvas .je-message').textContent.startsWith('Valid configuration'));
    assert.equal(attempts,0,'Editing, validation, and preview must not send workload traffic.');
    const savedDraft=await read();
    await action('Save scenario').click();
    await page.waitForFunction(()=>document.querySelector('#notice').textContent==='Scenario saved to the local library.');
    await page.locator('#builder-settings-tab').click();
    await page.locator('#library .library-item button').first().click();
    assert.deepEqual((await read()).scenarios,savedDraft.scenarios);
    // Literal user content must not become HTML or script.
    await request('Login'); await edit('Step name','<img src=x onerror=alert(1)>');
    assert.equal(await canvas.locator('img').count(),0);await action('Undo').click();
    // Every supported narrow layout must stay within the page.
    for(const width of [1440,1024,768,400,360,320]) {
      await page.setViewportSize({width,height:1000});
      await page.waitForTimeout(100);
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,`Canvas overflow at ${width}px`);
    }
    await page.setViewportSize({width:1440,height:1100});
    await request('Login'); await action('Fit').click();
    if(process.env.GOMETER_SCREENSHOT)await canvas.screenshot({path:process.env.GOMETER_SCREENSHOT});
    await action('Start run').click();
    await page.waitForFunction(()=>document.querySelector('#run-status').textContent==='PASSED',null,{timeout:20000});
    assert.equal(attempts,60);assert.equal(await page.locator('#run-journey-canvas .je-node').count(),3);
    assert.match(await page.locator('#run-journey-canvas .je-inspector').innerText(),/HTTP step measurements/);
    assert.equal(await page.locator('#run-journey-canvas input').count(),1,'Results must have only the navigation toggle, no editing fields.');
    assert.match(await page.locator('#run-journey-canvas .je-footer').innerText(),/20 completed journeys/);
    // Deliberate verification failure must show subsequent steps skipped with no fake latency.
    await page.locator('.nav[data-view="build"]').click();
    const failed=structuredClone(config);failed.scenarios[0].steps[0].assertions[0].equals='deliberate mismatch';
    await load(failed);await action('Start run').click();
    await page.waitForFunction(()=>document.querySelector('#run-status').textContent==='FAILED',null,{timeout:20000});
    assert.equal(attempts,80);assert.equal(await page.locator('#run-journey-canvas .je-skipped').count(),2);
    await page.locator('#run-journey-canvas .je-outline-step').filter({hasText:'Profile'}).click();
    assert.match(await page.locator('#run-journey-canvas .je-inspector').innerText(),/No latency samples/);
    // Edit executable controls entirely through the visual editor.
    await page.locator('.nav[data-view="build"]').click();await load(config);await request('Login');
    await action('+ Branch').click();await edit('Condition variable','item');await edit('Comparison value','coffee');
    let changed=await read();assert.equal(changed.scenarios[0].steps[1].if.condition.variable,'item');assert.equal(changed.scenarios[0].steps[1].if.condition.value,'coffee');
    await action('+ True loop').click();await edit('Repeat count','3');await edit('Iteration variable','visit');
    changed=await read();assert.equal(changed.scenarios[0].steps[1].if.then[1].loop.count,3);
    await canvas.getByLabel('Loop mode',{exact:true}).selectOption('while');
    await edit('Condition variable','item');await edit('Comparison value','coffee');await edit('Maximum iterations','4');
    changed=await read();assert.equal(changed.scenarios[0].steps[1].if.then[1].loop.while.variable,'item');assert.equal(changed.scenarios[0].steps[1].if.then[1].loop.max_iterations,4);
    await canvas.getByLabel('Loop mode',{exact:true}).selectOption('count');await edit('Repeat count','2');
    await request('Branch');await action('+ False request').click();
    assert.equal((await read()).scenarios[0].steps[1].if.else.length,1);
    await canvas.getByLabel('Move to path',{exact:true}).selectOption('steps');
    assert.equal((await read()).scenarios[0].steps.at(-1).name,'Request');await action('Undo').click();
    assert.equal((await read()).scenarios[0].steps[1].if.else[0].name,'Request');
    const flow=JSON.parse(fs.readFileSync(path.join(repo,'examples/flow.json'),'utf8'));flow.url=config.url;flow.duration='200ms';
    await load(flow);await action('Validate').click();
    await page.waitForFunction(()=>document.querySelector('#journey-canvas .je-message').textContent.startsWith('Valid configuration'));
    assert.equal(await canvas.locator('.je-node[data-kind="branch"]').count(),2);assert.equal(await canvas.locator('.je-node[data-kind="loop"]').count(),1);
    await action('Preview sequence').click();assert.match(await canvas.locator('.je-preview').innerText(),/Both branch paths/);
    assert.equal(attempts,80,'Control edits and preview must not send target traffic.');
    for(const width of [1440,768,400,320]){await page.setViewportSize({width,height:1100});await page.waitForTimeout(100);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,`Flow overflow at ${width}px`);}
    await page.setViewportSize({width:1600,height:1200});await request('Browse twice');
    assert.ok(parseInt(await canvas.locator('.je-zoom').innerText(),10)>=85,'Selecting a node must zoom to a readable size.');await action('Fit').click();
    if(process.env.GOMETER_FLOW_SCREENSHOT)await canvas.screenshot({path:process.env.GOMETER_FLOW_SCREENSHOT});
    await action('Start run').click();await page.waitForFunction(()=>document.querySelector('#run-status').textContent==='PASSED',null,{timeout:20000});
    assert.equal(attempts,88);assert.equal(await page.locator('#run-journey-canvas .je-node').count(),8);
    await page.locator('#run-journey-canvas .je-outline-step').filter({hasText:'First visit?'}).click();
    assert.match(await page.locator('#run-journey-canvas .je-inspector').innerText(),/Condition true: 2/);
    assert.match(await page.locator('#run-journey-canvas .je-inspector').innerText(),/Condition false: 2/);
    await page.locator('#run-journey-canvas .je-outline-step').filter({hasText:'Coffee checkout'}).click();
    assert.match(await page.locator('#run-journey-canvas .je-inspector').innerText(),/Skipped by condition/);
    await require('./workspace-browser.cjs')(page);
    assert.equal(attempts,88,'Navigation, comparison, JMX review and CSV preview must not send workload traffic.');
    assert.deepEqual(errors,[]);
    assert.equal(failedResponses.filter(([p,s])=>p==='/api/validate'&&s===400).length,1);
    assert.deepEqual(failedResponses.filter(([p,s])=>!(p==='/api/validate'&&s===400)&&!(p==='/favicon.ico'&&s===404)),[]);
    console.log('Browser integration passed: navigation/focus/themes, editing/drafts/dependencies, save/reload, XSS, six widths, counted journeys, nested branches/loops, history comparison, Guide/Coverage, JMX review and CSV dialog.');
  } finally {
    if(browser)await browser.close();
    if(dashboard&&dashboard.exitCode===null){dashboard.kill('SIGTERM');await new Promise(resolve=>dashboard.once('exit',resolve));}
    await new Promise(resolve=>target.close(resolve));
    fs.rmSync(workspace,{recursive:true,force:true});
  }
}
main().catch(e=>{console.error(e);process.exitCode=1;});
