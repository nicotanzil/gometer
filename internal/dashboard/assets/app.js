'use strict';

const $ = id => document.getElementById(id);
const token = document.querySelector('meta[name="gometer-token"]').content;
let examples = [], library = [], runs = [], current = null, selectedScenario = '', selectedRevision = 0, activeID = '', view = 'build';
let selectedRuns = new Set(), timer = null, polling = false, telemetryFormError = '', builderFormError = '', journeyFormError = '';
let coverage = [], editorBaseline = '';
const fmt = (value, digits = 1) => Number(value || 0).toLocaleString(undefined, {maximumFractionDigits: digits, minimumFractionDigits: digits});
const count = value => Number(value || 0).toLocaleString();
const errorActions = {next_iteration:'Start next iteration',continue:'Continue journey',stop_user:'Stop this user',stop_test:'Stop test gracefully',stop_test_now:'Stop test immediately'};
const errorHelp = {next_iteration:'Skip the remaining journey steps, then start the next iteration.',continue:'Attempt later steps and loop iterations. Failed requests and journeys still count as errors. Failed verification publishes no extracted values.',stop_user:'Skip the remaining journey and stop this user’s future iterations. Other users continue. Any user stopped on error fails the run.',stop_test:'End the failing journey and stop new users and iterations. Other admitted journeys finish under their deadlines. The test fails.',stop_test_now:'Cancel active requests, journey pauses, and pending user starts. Keep partial results and fail the test.'};
const stopReasons = {iterations:'iterations completed',duration:'duration reached',interrupted:'interrupted',users_stopped:'users stopped after errors',test_stopped:'test stopped gracefully after error',test_stopped_now:'test stopped immediately after error',data_exhausted:'CSV data exhausted'};
const active = run => run && ['running', 'stopping'].includes(run.status);
const node = (tag, text, className) => { const e = document.createElement(tag); if (text !== undefined) e.textContent = text; if (className) e.className = className; return e; };
const builderGrid = $('builder-settings'), editorPanel = builderGrid.querySelector('.editor-panel');
const builderAside = builderGrid.querySelector('.build-aside'), planPreview = builderAside.querySelector('.plan-preview');
const stackedBuilder = window.matchMedia('(max-width:1159px)');
function placePlanPreview() {
  if (stackedBuilder.matches && planPreview.parentElement !== builderGrid) builderGrid.insertBefore(planPreview, editorPanel);
  if (!stackedBuilder.matches && planPreview.parentElement !== builderAside) builderAside.prepend(planPreview);
}
stackedBuilder.addEventListener('change', placePlanPreview);
placePlanPreview();

async function api(path, method = 'GET', body, headers = {}) {
  const options = {method, headers: {'X-Gometer-Token': token, ...headers}};
  if (body !== undefined) { options.headers['Content-Type'] = 'application/json'; options.body = typeof body === 'string' ? body : JSON.stringify(body); }
  const response = await fetch(path, options);
  if (!response.ok) {
    let error; try { error = (await response.json()).error; } catch { error = `Request failed (${response.status}).`; }
    throw new Error(error);
  }
  return response;
}

function notice(message, bad = false) { $('notice').textContent = message; $('notice').className = bad ? 'bad' : ''; $('notice').hidden = false; }
function validateMessage(message, kind = '') { $('validation').textContent = message; $('validation').className = `validation ${kind}`; }
const workerCatalog = GoMeterWorkers.create({api, onChange: () => updateBuilder(), show: () => showView('workers')});
const testsLibrary = GoMeterTests.create({api, resume:()=>showView('build'), newTest:()=>{
 if (builderFormError||telemetryFormError||journeyFormError||$('config-json').value!==editorBaseline){notice('Save or finish your current draft before creating a new test. Your current work has been kept.',true);return;}
 setEditor(examples[0].config);showView('build');
},open:async(id,copy=false)=>{
 if (builderFormError||telemetryFormError||journeyFormError||$('config-json').value!==editorBaseline){notice('Save or finish your current draft before switching tests. Use Resume draft to continue editing.',true);return;}
 let saved=await(await api('/api/scenarios/'+id)).json();
 if(copy)saved=await(await api('/api/scenarios/'+id+'/copy','POST',undefined,{'If-Match':JSON.stringify(String(saved.revision))})).json();
 setEditor(saved.config,saved.id,saved.revision);showView('build');await refresh();
}});
const csvImport = GoMeterCSV.create({api});
const journeyEditor = GoMeterJourneyEditor.create($('journey-canvas'), {
  read: () => { if (builderFormError || telemetryFormError) throw new Error(builderFormError || telemetryFormError); return readEditor(true); },
  write: c => { $('config-json').value = JSON.stringify(c, null, 2); syncForm(c); validateMessage('Journey changed. Validate before running.'); },
  onDraftError: error => { journeyFormError = error; },
  settings: () => showBuilderTab('settings'),
  csv: (scenario, apply) => csvImport.open(scenario, apply),
  validate: validateEditor,
  save: () => saveScenario(false),
  start: startBuilderRun
});
const runJourneyEditor = GoMeterJourneyEditor.create($('run-journey-canvas'), {readonly:true});
const jmxImport = GoMeterJMX.create($('view-jmx'), {
  api, show:()=>showView('jmx'), close:()=>showView('build'), help:()=>openGuide('guide-jmx'), refresh,
  load:async(config,id)=>{try{const saved=await(await api('/api/scenarios/'+id)).json();setEditor(saved.config,id,saved.revision);showView('build');notice('Imported scenario loaded. Review it before starting a run.');}catch(error){notice(error.message,true);}}
});
$('import-jmx').addEventListener('click',()=>jmxImport.open());
function showBuilderTab(tab) {
  $('builder-settings').hidden = tab !== 'settings'; $('journey-canvas').hidden = tab !== 'journey';
  $('builder-settings-tab').setAttribute('aria-pressed', tab === 'settings'); $('builder-journey-tab').setAttribute('aria-pressed', tab === 'journey');
  if (tab === 'journey') journeyEditor.show();
}
$('builder-settings-tab').addEventListener('click', () => showBuilderTab('settings'));
$('builder-journey-tab').addEventListener('click', () => showBuilderTab('journey'));
$('plan-mobile-toggle').addEventListener('click', () => {
  const expanded = $('plan-mobile-toggle').getAttribute('aria-expanded') !== 'true';
  $('plan-mobile-toggle').setAttribute('aria-expanded', String(expanded));
  $('plan-mobile-toggle').textContent = expanded ? 'Hide full summary ▴' : 'Show full summary ▾';
  document.querySelector('.plan-preview').classList.toggle('expanded', expanded);
});
function showView(next) {
  view = next; $('notice').hidden = true;
  for (const name of ['tests', 'build', 'jmx', 'live', 'history', 'workers', 'guide', 'coverage']) $('view-' + name).hidden = name !== next;
  for (const e of document.querySelectorAll('.nav')) {
    const selected = e.dataset.view === next || ['build','jmx'].includes(next)&&e.dataset.view==='tests';
    e.classList.toggle('active', selected);
    if (selected) e.setAttribute('aria-current', 'page'); else e.removeAttribute('aria-current');
  }
  document.dispatchEvent(new CustomEvent('gometer:viewchange', {detail: next}));
  window.scrollTo(0, 0);
  if(next==='tests')testsLibrary.refresh().catch(e=>notice(e.message,true));
  if (next === 'history') refresh().catch(e => notice(e.message, true));
  if (next === 'live' && current) requestAnimationFrame(drawCharts);
  if (next === 'build' && !$('journey-canvas').hidden) journeyEditor.show();
  if (next === 'live' && !$('run-journey-canvas').hidden) runJourneyEditor.show();
  if (next === 'jmx') jmxImport.show();
}
for (const e of document.querySelectorAll('[data-view]')) e.addEventListener('click', () => showView(e.dataset.view));
function startBuilderRun() {
  if (activeID || $('start').disabled) { notice('A run is already active or starting. Stop it or wait for it to finish.', true); return; }
  if (!$('config-form').checkValidity()) { showBuilderTab('settings'); $('config-form').reportValidity(); return; }
  $('config-form').requestSubmit();
}
async function settleShortcutInput(action,restore=true) {
  const input=document.activeElement;
  if(input?.matches('input,select,textarea,[contenteditable]'))input.blur();
  try { await action(); }
  finally { if(restore&&view==='build'&&input?.isConnected&&input.getClientRects().length)input.focus({preventScroll:true}); }
}
GoMeterShortcuts.create({
  allowed: action => {
    if(['validate','save','start','undo'].includes(action))return view==='build'&&(action!=='start'||!activeID&&!$('start').disabled)&&(action!=='undo'||!$('journey-canvas').hidden&&journeyEditor.canUndo());
    return true;
  },
  perform: action => {
    if(action==='validate')return settleShortcutInput(()=>$('journey-canvas').hidden?validateEditor().catch(e=>validateMessage(e.message,'bad')):journeyEditor.validate());
    if(action==='save')return settleShortcutInput(()=>saveScenario());
    if(action==='start')return settleShortcutInput(startBuilderRun,false);
    if(action==='undo'){journeyEditor.undo();return;}
    showView(action);
    const heading=$('view-'+action).querySelector('h1,h2');
    if(heading){heading.tabIndex=-1;heading.focus({preventScroll:true});}
  },
  onError: error=>notice(error.message,true)
});


function readEditor(ignoreTelemetryError = false) {
  if (builderFormError&&!ignoreTelemetryError) throw new Error(builderFormError);
  if (telemetryFormError&&!ignoreTelemetryError) throw new Error(telemetryFormError);
  if (journeyFormError&&!ignoreTelemetryError) throw new Error(journeyFormError);
  const c = JSON.parse($('config-json').value);
  if (!c || Array.isArray(c) || typeof c !== 'object') throw new Error('The configuration must be a JSON object.');
  return c;
}
function setEditor(config, id = '', revision) {
  if (id !== selectedScenario) selectedRevision = revision || 0; else if (revision) selectedRevision = revision;
  selectedScenario = id;
  $('config-json').value = JSON.stringify(config, null, 2);
  syncForm(config, true); editorBaseline = $('config-json').value;
  showBuilderTab(config.scenarios?.length ? 'journey' : 'settings');
  validateMessage('Configuration changed. Validate or start a run.');
  $('save-scenario').textContent = id ? 'Update scenario' : 'Save scenario';
  $('save-copy').hidden = !id;
}
function syncForm(c, replaceJourneyDraft = false) {
  telemetryFormError=''; builderFormError='';
  const journey = !!c.scenarios?.length, stages = !!c.stages?.length, rateMode = (c.mode || 'users') === 'rate';
  $('name').value = c.name || 'HTTP test'; $('url').value = c.url || '';
  $('mode').value = c.mode || 'users';
  const method = c.method || 'GET';
  if (![...$('method').options].some(o => o.value === method)) $('method').append(node('option', method));
  $('method').value = method;
  $('rate').value = c.rate ?? 20; $('vus').value = c.vus ?? 10;
  $('capacity').value = c.max_in_flight ?? 100; $('duration').value = c.duration || '10s';
  $('iterations').value = c.iterations || 5; $('stop-condition').value = c.iterations > 0 ? (c.duration ? 'both' : 'iterations') : 'duration';
  $('timeout').value = c.timeout || '5s'; $('distribution').value = c.arrival_distribution || 'constant';
  $('body').value = c.body || ''; $('body-field').hidden = journey;
  $('success-statuses').value = (c.success_statuses || []).join(', ');
  $('insecure').checked = !!c.insecure; $('tls-warning').hidden = !c.insecure;
  $('ramp-up').value = c.ramp_up || ''; $('startup-delay').value = c.startup_delay || ''; $('user-schedule-fields').hidden = rateMode;
  $('ramp-up').disabled = rateMode; $('startup-delay').disabled = rateMode;
  $('on-error').value = c.on_error || 'next_iteration'; $('on-error').disabled = rateMode; $('on-error-field').hidden = rateMode;
  $('on-error-help').textContent = errorHelp[c.on_error || 'next_iteration'] || 'Choose a supported failure action.';
  $('think-time').value = c.think_time || ''; $('think-field').hidden = rateMode;
  $('threshold-p95').value = c.thresholds?.p95 || '';
  $('threshold-errors').value = c.thresholds?.max_error_percent === undefined ? 0 : c.thresholds.max_error_percent ?? '';
  $('threshold-drops').value = c.thresholds?.max_drop_percent === undefined ? 0 : c.thresholds.max_drop_percent ?? '';
  $('threshold-rps').value = c.thresholds?.min_started_rps ?? '';
  $('journey-builder-note').hidden = !journey; $('journey-checks-note').hidden = !journey;
  $('model-help').textContent = rateMode ? 'New work arrives at your chosen rate, even when the server slows down.' : 'Each user waits for its response, then repeats. Slower responses reduce request throughput.';
  $('staged-field').hidden = !rateMode; $('staged').checked = stages; $('phase-editor').hidden = !stages;
  $('traffic-options').open = stages || !!c.ramp_up || !!c.startup_delay || !!c.on_error;
  renderHeaderRows(c.headers || {}); renderStageRows(c.stages || []); updatePreview(c);
  $('diagnostics').value = c.diagnostics === false ? 'false' : 'true';
  workerCatalog.load(c.workers || [], c.mode || 'users');
  $('telemetry-url').value=c.telemetry?.url||''; $('telemetry-interval').value=c.telemetry?.interval||'1s'; $('telemetry-timeout').value=c.telemetry?.timeout||'1s'; $('telemetry-auth').value=c.telemetry?.bearer_auth?'true':'false';
  $('telemetry-metrics').value=JSON.stringify(c.telemetry?.metrics||[{name:'process_cpu_seconds_total',kind:'counter',unit:'seconds',title:'Server CPU time'},{name:'go_memstats_heap_alloc_bytes',kind:'gauge',unit:'bytes',title:'Server Go heap'}],null,2);
  $('rate-label').textContent = journey ? 'Journeys / second' : 'Requests / second';
  $('url-hint').textContent = journey ? 'Base origin for every step in your journeys.' : 'Full HTTP or HTTPS endpoint.';
  $('method-field').hidden = journey; $('rate-field').hidden = !rateMode || stages; $('duration-field').hidden = stages; $('traffic-limits').classList.toggle('staged', stages); $('vus-field').hidden = rateMode;
  $('capacity-field').hidden = !rateMode; $('distribution-field').hidden = !rateMode;
  $('duration').disabled = stages; $('rate').disabled = stages || !rateMode; $('vus').disabled = rateMode; $('capacity').disabled = !rateMode; $('distribution').disabled = !rateMode; $('think-time').disabled = rateMode; $('stages-hint').hidden = !stages;
  syncStoppingFields();
  $('phase-summary').hidden = !stages; $('single-phase').hidden = !stages;
  $('phase-summary').replaceChildren(...(c.stages || []).map(s => node('span', `${s.name || 'Phase'} · ${s.rate}/s · ${s.duration}`, 'phase-chip')));
  $('builder-workload-label').textContent = journey ? `${c.scenarios.length} journey(s)` : 'Single request';
  updateInlineHints();
  journeyEditor.load(c, replaceJourneyDraft);
}

function syncStoppingFields() {
  const users = $('mode').value === 'users', stop = $('stop-condition').value;
  $('stop-condition-field').hidden = !users;
  $('iterations-field').hidden = !users || stop === 'duration';
  $('iterations').disabled = !users || stop === 'duration';
  $('duration-field').hidden = (users && stop === 'iterations') || (!users && $('staged').checked);
  $('duration').disabled = $('duration-field').hidden;
}

// Draft fields are parsed together: an invalid draft never silently runs an older config.
function renderHeaderRows(headers) {
  $('header-rows').replaceChildren();
  for (const [name, value] of Object.entries(headers)) addHeaderRow(name, value);
}
function addHeaderRow(name = '', value = '') {
  const row = node('div', undefined, 'header-row');
  for (const [label, content, key] of [['Header name', name, 'header-name'], ['Header value', value, 'header-value']]) {
    const input = node('input'); input.value = content; input.placeholder = label; input.setAttribute('aria-label', label); input.dataset.field = key;
    input.addEventListener('input', updateBuilder); row.append(input);
  }
  const remove = node('button', 'Remove', 'text-button'); remove.type = 'button'; remove.setAttribute('aria-label', 'Remove header');
  remove.addEventListener('click', () => { row.remove(); updateBuilder(); }); row.append(remove); $('header-rows').append(row);
}
function renderStageRows(stages) {
  $('stage-rows').replaceChildren();
  stages.forEach((stage, i) => {
    const row = node('div', undefined, 'stage-row');
    for (const [label, key, value] of [['Name', 'name', stage.name], ['Duration', 'duration', stage.duration], ['Arrivals / s', 'rate', stage.rate]]) {
      const field = node('label', label), input = node('input'); input.dataset.field = key; input.value = value ?? ''; input.setAttribute('aria-label', `Phase ${i+1} ${label}`);
      if (key === 'rate') { input.type = 'number'; input.min = '0'; input.max = '1000000'; input.step = '1'; }
      input.addEventListener('input', updateBuilder); field.append(input); row.append(field);
    }
    const remove = node('button', 'Remove', 'text-button'); remove.type = 'button'; remove.setAttribute('aria-label', `Remove phase ${i+1}`);
    remove.addEventListener('click', () => {
      row.remove();
      if (!$('stage-rows').children.length) {
        $('staged').checked = false; $('phase-editor').hidden = true; $('phase-summary').hidden = true;
        $('duration').disabled = false; $('duration-field').hidden = false; $('rate').disabled = false; $('rate-field').hidden = false; $('traffic-limits').classList.remove('staged'); $('rate').value = 20; $('stages-hint').hidden = true;
      }
      [...$('stage-rows').children].forEach((item,index) => {
        for (const input of item.querySelectorAll('input')) input.setAttribute('aria-label', `Phase ${index+1} ${{name:'Name',duration:'Duration',rate:'Arrivals / s'}[input.dataset.field]}`);
        item.querySelector('button').setAttribute('aria-label', `Remove phase ${index+1}`);
      });
      updateBuilder();
    });
    row.append(remove); $('stage-rows').append(row);
  });
}
function updateBuilder() {
  try {
    const c = readEditor(true);
    for (const key of ['name', 'url', 'method', 'duration', 'timeout']) c[key] = $(key).value;
    for (const [id,key] of [['rate','rate'],['vus','vus'],['capacity','max_in_flight']]) {
      if ($(id).disabled) continue;
      if (!$(id).value.trim() || !Number.isFinite(Number($(id).value))) throw new Error('Enter a number for ' + {rate:'offered rate',vus:'concurrent users',capacity:'concurrency cap'}[id] + '.');
      c[key] = Number($(id).value);
    }
    if (c.mode === 'users') c.rate = 0;
    if (c.mode === 'users' && $('stop-condition').value !== 'duration') {
      const value = Number($('iterations').value);
      if (!$('iterations').value.trim() || !Number.isInteger(value) || value < 1 || value > 1000000) throw new Error('Enter 1 to 1000000 iterations per user.');
      c.iterations = value;
      if ($('stop-condition').value === 'iterations') c.duration = '';
    } else delete c.iterations;
    for (const [id,key] of [['ramp-up','ramp_up'],['startup-delay','startup_delay']]) {
      const value = $(id).value.trim(), seconds = durationSeconds(value, true);
      if (c.mode === 'users' && value) {
        if (seconds === null || seconds > 86400) throw new Error('Use a duration from 0s to 24h for ' + (id === 'ramp-up' ? 'user ramp-up.' : 'startup delay.'));
        c[key] = value;
      } else delete c[key];
    }
    if (c.mode === 'users' && $('on-error').value !== 'next_iteration') c.on_error = $('on-error').value; else delete c.on_error;
    $('on-error-help').textContent = errorHelp[c.on_error || 'next_iteration'];
    c.diagnostics = $('diagnostics').value === 'true'; c.insecure = $('insecure').checked;
    c.arrival_distribution = $('distribution').value;
    if (!c.scenarios?.length) c.body = $('body').value;
    const headers = Object.create(null), names = new Set();
    for (const row of $('header-rows').children) {
      const name = row.querySelector('[data-field="header-name"]').value.trim(), value = row.querySelector('[data-field="header-value"]').value;
      if (!name && !value) continue;
      if (!name) throw new Error('Every header with a value needs a name.');
      if (names.has(name.toLowerCase())) throw new Error('Duplicate header name: ' + name);
      names.add(name.toLowerCase()); headers[name] = value;
    }
    c.headers = headers;
    const statuses = $('success-statuses').value.trim();
    if (statuses) {
      const codes = statuses.split(',').map(x => x.trim());
      if (codes.some(x => !/^\d{3}$/.test(x))) throw new Error('Use comma-separated status codes, for example 200, 201.');
      c.success_statuses = codes.map(Number);
    } else delete c.success_statuses;
    if (c.mode === 'users') { if ($('think-time').value.trim()) c.think_time = $('think-time').value.trim(); else delete c.think_time; }
    if ($('staged').checked && c.mode === 'rate') {
      c.rate = 0;
      c.stages = [...$('stage-rows').children].map(row => {
        const get = key => row.querySelector(`[data-field="${key}"]`).value;
        if (!get('rate').trim()) throw new Error('Every phase needs an arrival rate (0 pauses traffic).');
        return {name: get('name'), duration: get('duration'), rate: Number(get('rate'))};
      });
    } else delete c.stages;
    c.thresholds = {...c.thresholds, p95: $('threshold-p95').value.trim()};
    for (const [id,key] of [['errors','max_error_percent'],['drops','max_drop_percent'],['rps','min_started_rps']]) {
      const input = $('threshold-'+id), value = input.value.trim();
      if (input.validity.badInput) throw new Error('Enter a number or leave the pass criterion blank.');
      c.thresholds[key] = value ? Number(value) : null;
    }
    const workers = workerCatalog.origins();
    if (workers.length) c.workers = workers; else delete c.workers;
    builderFormError = ''; $('config-json').value = JSON.stringify(c, null, 2);
    journeyEditor.load(c);
    $('tls-warning').hidden = !c.insecure; updatePreview(c); validateMessage('Configuration changed. Validate or start a run.');
  } catch (e) { builderFormError = e.message; validateMessage(builderFormError, 'bad'); $('plan-preview').replaceChildren(node('p', 'Fix the configuration before running.', 'helper')); $('plan-compact').textContent = 'Fix the configuration before running'; }
  finally { updateInlineHints(); }
}
function durationSeconds(value, allowZero = false) {
  // Matches the positive durations accepted by Go, including compound units.
  const units = {ns:1e-9, us:1e-6, 'µs':1e-6, 'μs':1e-6, ms:1e-3, s:1, m:60, h:3600};
  let rest = String(value || '').replace(/^\+/, ''), total = 0;
  if (!rest) return null;
  if (allowZero && rest === '0') return 0;
  while (rest) { const part = /^(\d+(?:\.\d*)?|\.\d+)(ns|us|µs|μs|ms|s|m|h)/.exec(rest); if (!part) return null; total += Number(part[1])*units[part[2]]; rest = rest.slice(part[0].length); }
  return total > 0 || allowZero ? total : null;
}
function updateInlineHints() {
  const duration = $('duration');
  const hints = [
    ...['ramp-up','startup-delay'].map(id => [id, !$(id).disabled && $(id).value.trim() && (durationSeconds($(id).value.trim(), true) === null || durationSeconds($(id).value.trim(), true) > 86400) ? 'Use a duration from 0s to 24h.' : '']),
    ['iterations', !$('iterations').disabled && (!$('iterations').value.trim() || !$('iterations').validity.valid) ? 'Enter a whole number from 1 to 1000000.' : ''],
    ['duration', !duration.disabled && durationSeconds(duration.value) === null ? 'Enter a positive duration, such as 10s or 2m.' : ''],
    ['threshold-p95', $('threshold-p95').value.trim() && durationSeconds($('threshold-p95').value.trim()) === null ? 'Use a positive duration, such as 200ms.' : ''],
    ...['errors','drops','rps'].map(id => {
      const input = $('threshold-' + id);
      return ['threshold-' + id, (input.validity.badInput || (input.value.trim() && !input.validity.valid)) ? id === 'rps' ? 'Enter a number of at least 0.' : 'Enter a percentage from 0 to 100.' : ''];
    })
  ];
  for (const [id, message] of hints) {
    const input = $(id), error = $(id + '-error');
    error.textContent = message;
    error.hidden = !message;
    input.setAttribute('aria-invalid', String(!!message));
  }
}
function hasHTTPWorkers(workers) { return (workers || []).some(url => /^http:/i.test(url)); }
function updatePreview(c) {
  $('worker-control-warning').hidden = !hasHTTPWorkers(c.workers);
  $('phase-summary').replaceChildren(...(c.stages || []).map(s => node('span', `${s.name || 'Phase'} · ${s.rate}/s · ${s.duration}`, 'phase-chip')));
  const csvEOF = c.scenarios?.some(s=>s.csv?.on_eof==='stop_user');
  const journey = !!c.scenarios?.length, unit = journey ? 'journeys' : 'requests';
  const seconds = c.stages?.length ? c.stages.reduce((sum,p) => { const d=durationSeconds(p.duration); return sum === null || d === null ? null : sum+d; }, 0) : durationSeconds(c.duration);
  const planned = c.stages?.length ? c.stages.reduce((sum,p) => { const d=durationSeconds(p.duration); return sum === null || d === null ? null : sum+Math.ceil(d*p.rate); },0) : seconds === null ? null : Math.ceil(seconds*c.rate);
  const stopping = c.iterations > 0 ? `${count(c.iterations)} iterations/user${c.duration ? ' or ' + fmt(seconds) + 's, whichever comes first' : ''}` : seconds === null ? 'set duration' : fmt(seconds) + 's';
  $('plan-compact').textContent = `${journey ? c.scenarios.length + ' ' + (c.scenarios.length === 1 ? 'journey' : 'journeys') : c.method || 'GET'} · ${c.mode === 'rate' ? (c.stages?.length ? c.stages.length + ' phases' : c.rate + ' ' + unit + '/s') : c.vus + ' users'} · ${stopping}${planned !== null && c.mode === 'rate' ? ' · ' + count(planned) + ' planned' : ''}`;
  const rows = [c.scenarios?.length ? `${c.scenarios.length} journey scenario(s)` : `${c.method || 'GET'} request`, c.url || 'Choose a target URL',
    c.mode === 'rate' ? `${c.stages?.length ? c.stages.length+' phases' : c.rate+' '+unit+'/s'} · cap ${c.max_in_flight} concurrent ${unit}` : `${c.vus} concurrent users`,
    c.iterations > 0 ? `${count(c.vus * c.iterations)} ${c.duration || csvEOF ? 'maximum' : 'planned'} ${unit} · ${stopping}` : seconds === null ? 'Enter a valid load duration' : `${fmt(seconds)}s load window`,
    c.mode === 'rate' && planned !== null ? `${count(planned)} planned ${unit}; some may be missed` : 'Throughput depends on response time',
    c.workers?.length ? `Run on ${c.workers.length} workers` : 'Run on this machine'];
  if (c.mode === 'users' && (c.ramp_up || c.startup_delay)) {
    const ramp = durationSeconds(c.ramp_up, true) || 0, delay = durationSeconds(c.startup_delay, true) || 0;
    rows.push(`${fmt(delay)}s startup wait before load · ${fmt(ramp)}s user ramp`, `First user at ${fmt(delay)}s; last planned start at ${fmt(delay + ramp*(c.vus-1)/c.vus)}s from run start`);
    if (seconds !== null && ramp*(c.vus-1)/c.vus >= seconds) rows.push('The duration limit closes before every user can start.');
  }
  if (c.mode === 'users') rows.push('After a failed request: ' + (errorActions[c.on_error || 'next_iteration'] || c.on_error));
  for(const s of c.scenarios||[])if(s.csv)rows.push(`CSV ${s.name}: ${s.csv.name||'dataset'} · ${s.csv.sharing==='per_user'?'per user':'shared'} · ${s.csv.on_eof==='stop_user'?'users finish normally at EOF; may end before limits':'recycle rows'}`);
  if (c.insecure) rows.push('HTTPS verification disabled');
  $('plan-preview').replaceChildren(...rows.map((text,i) => node('p', text, i===1 ? 'preview-target' : '')));
  const checks = node('div', undefined, 'preview-checks'), title = node('h3', 'Pass criteria');
  checks.append(title);
  const enabled = value => value !== null && value !== undefined && String(value).trim() !== '';
  const limits = {max_error_percent:0,max_drop_percent:0,...c.thresholds}, values = [
    [limits.p95, value => `Request p95 ≤ ${value}`],
    [limits.max_error_percent, value => `Request errors ≤ ${value}%`],
    [limits.max_drop_percent, value => `Missed arrivals ≤ ${value}%`],
    [limits.min_started_rps, value => `Started HTTP requests ≥ ${value}/s`]
  ];
  const list = node('div', undefined, 'preview-check-list');
  for (const [value, label] of values) if (enabled(value)) list.append(node('span', label(value)));
  if (c.on_error === 'stop_user') list.append(node('span', 'No users stopped on error'));
  if (['stop_test','stop_test_now'].includes(c.on_error)) list.append(node('span', 'Test must not stop on error'));
  if (!list.childElementCount) list.append(node('p', 'No pass criteria enabled.', 'helper'));
  checks.append(list); $('plan-preview').append(checks);
}
for (const id of ['name','url','method','duration','iterations','timeout','rate','vus','capacity','distribution','diagnostics','body','success-statuses','insecure','think-time','ramp-up','startup-delay','on-error','threshold-p95','threshold-errors','threshold-drops','threshold-rps']) $(id).addEventListener('input', updateBuilder);
$('stop-condition').addEventListener('change', () => { syncStoppingFields(); updateBuilder(); });
$('add-header').addEventListener('click', () => { addHeaderRow(); $('header-rows').lastElementChild.querySelector('input').focus(); });
$('staged').addEventListener('change', () => {
  try { const c = readEditor(); if ($('staged').checked) { c.stages = [{name:'Phase 1',duration:c.duration,rate:c.rate}]; c.rate = 0; } else { c.rate = c.stages?.[0]?.rate || 20; delete c.stages; } setEditor(c, selectedScenario); }
  catch(e) { $('staged').checked = !$('staged').checked; validateMessage(e.message, 'bad'); }
});
$('add-stage').addEventListener('click', () => { try { const c=readEditor(); c.stages.push({name:`Phase ${c.stages.length+1}`,duration:'10s',rate:c.stages.at(-1).rate}); setEditor(c, selectedScenario); } catch(e) { validateMessage(e.message, 'bad'); } });
for (const id of ['telemetry-url','telemetry-interval','telemetry-timeout','telemetry-auth','telemetry-metrics']) $(id).addEventListener('input',()=>{
  try {
    const c=readEditor(true), url=$('telemetry-url').value.trim();
    if (!url) delete c.telemetry;
    else c.telemetry={...c.telemetry,url,interval:$('telemetry-interval').value,timeout:$('telemetry-timeout').value,bearer_auth:$('telemetry-auth').value==='true',metrics:JSON.parse($('telemetry-metrics').value)};
    telemetryFormError=''; $('config-json').value=JSON.stringify(c,null,2); validateMessage('Configuration changed. Validate or start a run.');
  } catch (e) { telemetryFormError='Fix the scenario and selected metrics JSON: '+e.message; validateMessage(telemetryFormError,'bad'); }
});
$('mode').addEventListener('change', () => {
  try {
    const c = readEditor(); c.mode = $('mode').value;
    if (c.mode === 'users') { c.rate = 0; delete c.stages; c.arrival_distribution = 'constant'; }
    else { c.rate = c.rate || 20; delete c.think_time; delete c.iterations; delete c.ramp_up; delete c.startup_delay; delete c.on_error; c.duration = c.duration || '10s'; }
    $('config-json').value = JSON.stringify(c, null, 2); syncForm(c); validateMessage('Configuration changed. Validate or start a run.');
  } catch (e) { validateMessage(e.message, 'bad'); }
});
$('config-json').addEventListener('input', () => {
  try { syncForm(readEditor(true), true); validateMessage('Configuration changed. Validate or start a run.'); }
  catch (e) { validateMessage('Invalid JSON: ' + e.message, 'bad'); journeyEditor.invalid('Correct Advanced JSON before editing the canvas: ' + e.message); }
});
$('single-phase').addEventListener('click', () => { try { const c = readEditor(); delete c.stages; c.rate = 20; setEditor(c, selectedScenario); } catch (e) { notice(e.message, true); } });
$('starter').addEventListener('change', () => {
  const selected = examples.find(e => e.id === $('starter').value);
  if (selected) { setEditor(selected.config); $('starter').value = ''; notice('Example loaded. The examples target the local demo server on port 8787.'); }
});
async function validateEditor() {
  const data = await (await api('/api/validate', 'POST', readEditor())).json();
  const stop = data.config.iterations > 0 ? `${count(data.config.iterations)} iterations/user${data.load_seconds ? ' or ' + fmt(data.load_seconds) + 's, whichever comes first' : ''}` : `${fmt(data.load_seconds)}s load`;
  validateMessage(`Valid · ${stop} · ${data.phase_count} phase(s) · ${data.workload} workload`, 'good');
  return data;
}
$('validate').addEventListener('click', () => validateEditor().catch(e => validateMessage(e.message, 'bad')));
$('config-form').addEventListener('submit', async e => {
  e.preventDefault(); $('start').disabled = true;
  try {
    const run = await (await api('/api/runs', 'POST', readEditor())).json();
    activeID = run.id; await loadRun(run.id); showView('live'); schedulePoll();
    notice('Run started. You can change views or reload this page while it continues.');
  } catch (error) { validateMessage(error.message, 'bad'); notice(error.message, true); }
  finally { $('start').disabled = !!activeID; }
});
$('stop').addEventListener('click', async () => {
  $('stop').disabled = true;
  try { await api(`/api/runs/${activeID}/stop`, 'POST'); notice('Stopping new work and canceling active requests. Partial results will be saved.'); await poll(); }
  catch (e) { notice(e.message, true); }
  finally { $('stop').disabled = current?.status === 'stopping'; }
});
$('view-active').addEventListener('click', async () => {
  try { if (activeID) { await loadRun(activeID); showView('live'); } } catch (e) { notice(e.message, true); }
});

async function saveScenario(copy = false) {
  const id = copy ? '' : selectedScenario;
  try {
    const saved = await (await api(id ? `/api/scenarios/${id}` : '/api/scenarios', id ? 'PUT' : 'POST', readEditor(), id ? {'If-Match': JSON.stringify(String(selectedRevision))} : {})).json();
    selectedScenario = saved.id; selectedRevision = saved.revision; editorBaseline = $('config-json').value; $('save-scenario').textContent = 'Update scenario'; $('save-copy').hidden = false;
    await refresh(); notice('Scenario saved to the local library.');
  } catch (e) { notice(e.message, true); }
}
$('save-scenario').addEventListener('click', () => saveScenario());
$('save-copy').addEventListener('click', () => saveScenario(true));
$('export-config').addEventListener('click', async () => {
  try { const exported = await (await api('/api/config-export', 'POST', readEditor())).json(); downloadURL(exported.path, 'gometer-scenario.json'); }
  catch (e) { notice(e.message, true); }
});
$('import-config').addEventListener('click', () => $('file-input').click());
$('import-history').addEventListener('click', () => $('file-input').click());
$('file-input').addEventListener('change', async () => {
  const file = $('file-input').files[0]; if (!file) return;
  try {
    if (file.size > 16777216) throw new Error('Import exceeds 16 MiB.');
    const text = await file.text(), parsed = JSON.parse(text);
    if (parsed?.result && parsed?.config) {
      const imported = await (await api('/api/runs/import', 'POST', text)).json();
      await refresh(); await loadRun(imported.id); showView('live'); notice('Saved run imported. Its traffic measurements have not been independently verified.'); return;
    }
    if (file.size > 1048576) throw new Error('Configuration exceeds 1 MiB.');
    const data = await (await api('/api/validate', 'POST', text)).json();
    setEditor(data.config); validateMessage(`Imported and validated · ${data.workload} workload`, 'good'); notice('Scenario imported into the editor. Save it to keep it in your library.');
  } catch (e) { notice(e.message, true); }
  finally { $('file-input').value = ''; }
});

async function refresh() {
  const state = await (await api('/api/state')).json(); library = state.scenarios; runs = state.runs;
  renderLibrary(); renderHistory(); workerCatalog.updateCatalog(state.workers || []);
  renderMigrations(state.migrations||[]);
  const running = state.current && active(state.current);
  activeID = running ? state.current.id : '';
  $('start').disabled = !!activeID; $('live-dot').hidden = !activeID;
  if (activeID && !active(current)) { await loadRun(activeID); schedulePoll(); }
  if (current?.id === state.current?.id && active(current) && !running) await loadRun(current.id);
  if (current) renderRun();
}
function renderLibrary() {
  $('library-count').textContent = library.length;
  if (!library.length) { $('library').replaceChildren(node('div', 'Save a configuration to reuse it here.', 'empty-small')); return; }
  $('library').replaceChildren(...library.map(s => {
    const item = node('div', undefined, 'library-item'), button = node('button', s.name || 'HTTP test');
    button.type = 'button'; button.addEventListener('click', async () => {
      try { const saved = await (await api(`/api/scenarios/${s.id}`)).json(); setEditor(saved.config, s.id, saved.revision); showView('build'); notice('Saved scenario loaded into the editor.'); } catch (e) { notice(e.message, true); }
    });
    const remove = node('button', 'Delete', 'text-button'); remove.type = 'button';
    remove.addEventListener('click', async () => {
      if (!confirm('Delete this saved scenario? Run results will remain.')) return;
      try { await api(`/api/scenarios/${s.id}`, 'DELETE'); if (selectedScenario === s.id) setEditor(readEditor()); await refresh(); notice('Scenario deleted.'); } catch (e) { notice(e.message, true); }
    });
    const copy = node('button', 'Use a copy', 'text-button'); copy.type='button';
    copy.addEventListener('click', async () => {
      if (builderFormError || telemetryFormError || journeyFormError || $('config-json').value !== editorBaseline) { notice('Save or finish your current draft before opening a copy. Your current work has been kept.', true); return; }
      copy.disabled=true;
      try { const saved=await(await api(`/api/scenarios/${s.id}/copy`,'POST',undefined,{'If-Match':JSON.stringify(String(s.revision))})).json();setEditor(saved.config,saved.id,saved.revision);await refresh();showView('build');notice('Independent test copy saved. The original test is unchanged.'); }
      catch(error){notice(error.message,true);}finally{copy.disabled=false;}
    });
    item.append(button, node('small', `${s.workload === 'journeys' ? 'Journeys' : 'Requests'} · ${s.mode} mode · Revision ${s.revision || 1}`), copy, remove);
    if(s.imported_jmx){const source=node('button','Review original JMX','text-button jmx-source-link');source.type='button';source.addEventListener('click',async()=>{try{const saved=await(await api(`/api/scenarios/${s.id}`)).json();await jmxImport.open({...saved.migration,name:saved.name});notice('Reviewing the original JMX. Subsequent builder edits are not part of this source plan.');}catch(e){notice(e.message,true);}});item.append(source);}
    return item;
  }));
}
function renderMigrations(drafts){
  $('migration-count').textContent=drafts.length;
  if(!drafts.length){$('migration-library').replaceChildren(node('div','Save an import draft to continue it later.','empty-small'));return;}
  $('migration-library').replaceChildren(...drafts.map(d=>{
    const item=node('div',undefined,'jmx-draft-item'),open=node('button',d.name||d.filename),remove=node('button','Delete draft','text-button');open.type=remove.type='button';
    open.addEventListener('click',async()=>{try{await jmxImport.open(await(await api(`/api/jmx/migrations/${d.id}`)).json());}catch(e){notice(e.message,true);}});
    remove.addEventListener('click',async()=>{if(!confirm('Delete this migration draft and its stored JMX source?'))return;try{await api(`/api/jmx/migrations/${d.id}`,'DELETE');await refresh();notice('Migration draft deleted.');}catch(e){notice(e.message,true);}});
    item.append(open,node('small','Migration draft · Not runnable'),remove);return item;
  }));
}

async function loadRun(id) { current = await (await api(`/api/runs/${id}`)).json(); renderRun(); }
function schedulePoll() { clearTimeout(timer); if (activeID) timer = setTimeout(poll, 1000); }
async function poll() {
  if (polling || !activeID) return;
  polling = true;
  try {
    const runningID = activeID;
    const run = await (await api(`/api/runs/${runningID}`)).json();
    // Keep a saved run selected while another run is active.
    if (!current || current.id === runningID) { current = run; renderRun(); }
    if (!active(run)) {
      activeID = ''; $('start').disabled = false; $('live-dot').hidden = true;
      await refresh(); notice(run.save_error || run.error || `Run ${run.status}. Results are saved locally.`, !!run.save_error || !!run.error);
    }
  } catch (e) { notice('Live connection interrupted: ' + e.message + ' The run continues while the dashboard server is running.', true); }
  finally { polling = false; schedulePoll(); }
}
function renderRun() {
  if (!current) return;
  const run = current, latest = run.samples.at(-1) || {}, r = run.result || latest.requests || {}, j = run.result?.journeys || latest.journeys || {};
  const journeys = run.workload === 'journeys', traffic = journeys ? j : r;
  $('live-empty').hidden = true; $('run-content').hidden = false;
  $('run-title').textContent = run.name || 'HTTP test'; $('run-target').textContent = run.config.url;
  $('live-eyebrow').textContent = `${journeys ? 'Journeys' : 'Requests'} · ${run.config.mode === 'rate' ? 'Arrival rate' : 'Concurrent users'}`;
  if (run.imported) $('live-eyebrow').textContent += ' · Imported';
  if (run.config.workers?.length) $('live-eyebrow').textContent += ` · ${run.config.workers.length} workers`;
  $('run-tls-warning').hidden = !run.config.insecure;
  $('run-status').textContent = run.status.toUpperCase(); $('run-status').className = 'status ' + run.status;
  $('stop').hidden = !activeID || run.id !== activeID; $('stop').disabled = run.status === 'stopping';
  $('view-active').hidden = !activeID || run.id === activeID;
  const schedule = run.result?.user_schedule || latest.user_schedule;
  $('phase-name').textContent = schedule?.waiting && active(run) ? 'Waiting to start' : latest.phase ? `${latest.draining && active(run) ? 'Draining · ' : ''}${latest.phase}` : 'Preparing';
  $('elapsed').textContent = `${fmt(latest.load_seconds)} / ${fmt(run.planned_seconds)}s load${latest.elapsed_seconds > latest.load_seconds + .05 ? ` · ${fmt(latest.elapsed_seconds - latest.load_seconds)}s ${run.config.workers?.length||run.config.telemetry ? 'drain + collection' : 'drain'}` : ''}`;
  if (run.config.iterations > 0) {
    const target = run.config.vus * run.config.iterations;
    $('elapsed').textContent = `${count(traffic.completed)} / ${count(target)} ${run.config.scenarios?.some(s=>s.csv?.on_eof==='stop_user')?'maximum ':''}${run.workload} · ${fmt(latest.load_seconds)}s load${run.planned_seconds ? ' · ' + fmt(run.planned_seconds) + 's limit' : ''}${run.result?.stop_reason ? ' · Stopped: ' + (stopReasons[run.result.stop_reason] || run.result.stop_reason) : ''}`;
    $('progress').max = target; $('progress').value = traffic.completed || 0;
  } else { $('progress').max = run.planned_seconds || 1; $('progress').value = latest.load_seconds || 0; }
  $('progress').setAttribute('aria-label', run.config.iterations > 0 ? `Completed ${run.workload} toward the iteration limit` : 'Admission window progress');
  if (schedule) {
    const remainder = Math.max(0, (latest.elapsed_seconds || 0) - schedule.startup_seconds - (latest.load_seconds || 0));
    const load = run.config.iterations > 0 ? $('elapsed').textContent : `${fmt(latest.load_seconds)} / ${fmt(run.planned_seconds)}s load${run.result?.stop_reason ? ' · Stopped: ' + (stopReasons[run.result.stop_reason] || run.result.stop_reason) : ''}`;
    $('elapsed').textContent = `${schedule.waiting && active(run) ? 'Waiting: ' : 'Startup: '}${fmt(schedule.startup_seconds)} / ${fmt(schedule.planned_startup_seconds)}s · ${load}${remainder > .05 ? ' · ' + fmt(remainder) + 's drain / collection' : ''} · ${count(schedule.users.active)} active users · ${count(schedule.users.started)} / ${count(schedule.users.planned)} started`;
  }
  const datasets = run.result?.datasets || latest.datasets || [];
  $('csv-status').hidden = !datasets.length;
  $('csv-status').textContent = datasets.map(d=>`${d.scenario} · ${d.name}: ${count(d.rows_assigned)} rows assigned · ${count(d.users_exhausted)} users finished at EOF`).join(' / ');
  const failure = run.result?.failure_policy || latest.failure_policy;
  if (failure) {
    $('elapsed').textContent += ' · ' + errorActions[failure.action];
    if (['stop_test','stop_test_now'].includes(failure.action)) { if (failure.test_stopped) $('elapsed').textContent += failure.action === 'stop_test' && active(run) ? ' · Error stop: draining admitted journeys' : ' · Test stopped on error'; }
    else $('elapsed').textContent += ` · ${count(failure.users_stopped)} users stopped on error`;
  }
  if (!schedule && !run.config.iterations && run.result?.stop_reason) $('elapsed').textContent += ' · Stopped: ' + (stopReasons[run.result.stop_reason] || run.result.stop_reason);
  $('metric-rps').textContent = fmt(r.started_rps); $('metric-requests').textContent = `${count(r.started)} HTTP attempts · ${count(r.in_flight)} in flight`;
  $('metric-latency').textContent = r.latency?.samples ? fmt(r.latency.p95_ms) + ' ms' : '—';
  $('metric-errors').textContent = fmt(r.error_percent, 2) + '%'; $('metric-errors').classList.toggle('bad', !!r.failed);
  $('metric-failed').textContent = `${count(r.failed)} / ${count(r.completed)} completed attempts`;
  $('metric-drops').textContent = fmt(traffic.drop_percent, 2) + '%'; $('metric-drops').classList.toggle('bad', !!(traffic.dropped_capacity || traffic.dropped_scheduler));
  $('metric-missed').textContent = `${count((traffic.dropped_capacity || 0) + (traffic.dropped_scheduler || 0))} / ${count(traffic.scheduled)} ${run.workload}`;
  $('journey-summary').hidden = !journeys; $('journey-key').hidden = !journeys;
  $('journey-summary').replaceChildren(...[`${fmt(j.started_per_second)} journeys/s`, `${count(j.completed)} completed journeys`, `Journey p95 ${j.latency?.samples ? fmt(j.latency.p95_ms) + ' ms' : '—'}`, `${fmt(j.error_percent, 2)}% journey errors`].map(t => node('span', t)));
  $('phase-title').textContent = run.config.mode === 'users' ? 'User schedule' : 'Admission phases';
  $('traffic-unit').textContent = journeys ? 'HTTP req/s + journeys/s' : 'req/s'; $('phase-unit').textContent = run.config.mode === 'users' ? 'planned starts / observed active users' : (journeys ? 'journeys/s' : 'requests/s');
  $('results-panel').hidden = !run.result;
  $('run-journey-canvas').hidden = !run.result?.scenarios?.length;
  if (!$('run-journey-canvas').hidden) runJourneyEditor.loadRun(run);
  GoMeterResults.renderOverview($('result-overview'), run);
  if (run.result) renderResults(run.result);
  renderDiagnostics(run, latest, r, traffic);
  renderWorkers(run,latest);
  renderTelemetry(run,latest);
  if (run.error || run.save_error) notice(run.save_error || run.error, true);
  if (view === 'live') requestAnimationFrame(drawCharts);
}
function telemetryPoints(run) {
  if (run.result?.telemetry) return run.result.telemetry.timeline||[];
  const seen=new Set(); return run.samples.map(s=>s.telemetry).filter(p=>p&&!seen.has(p.sequence)&&seen.add(p.sequence));
}
function telemetryNumber(value,unit='',rate=false) {
  if (value===null || value===undefined) return '—';
  if (unit==='bytes') { value/=1048576; unit='MiB'; }
  const numeric=Math.abs(value)>=1e6 || (value!==0&&Math.abs(value)<.001) ? Number(value).toExponential(2) : fmt(value,3);
  return `${numeric}${unit?' '+unit:''}${rate?'/s':''}`;
}
function renderTelemetry(run,latest) {
  const config=run.config.telemetry, summary=run.result?.telemetry;
  $('telemetry-panel').hidden=!config; if (!config) return;
  const points=telemetryPoints(run), point=points.at(-1), metrics=summary?.series.map(s=>s.metric)||config.metrics;
  const incomplete=point?.error || point?.values.some(v=>v.value===undefined);
  $('telemetry-state').textContent=summary ? (summary.complete?'COMPLETE SCRAPES':'INCOMPLETE SCRAPES') : (point?(incomplete?'SCRAPE INCOMPLETE':'OBSERVING'):'WAITING');
  $('telemetry-source').textContent=config.url;
  const problems=[];
  if (point?.error) problems.push(point.error);
  for (const [i,v] of (point?.values||[]).entries()) if (v.issue) problems.push(`${metrics[i]?.title||metrics[i]?.name}: ${v.issue}`);
  if (summary&&!summary.complete) problems.unshift(summary.attempts ? `${summary.failed_scrapes} failed scrapes and ${summary.incomplete_scrapes} scrapes with missing/invalid series. Load checks are independent.` : 'No telemetry observations were recorded. Load checks are independent.');
  $('telemetry-warning').hidden=!problems.length; $('telemetry-warning').textContent=problems.join(' ');
  const rows=metrics.map((m,i)=>{
    const v=point?.values[i], s=summary?.series[i], label=node('div',m.title||m.name); label.append(node('div',m.name+(Object.keys(m.labels||{}).length?' '+JSON.stringify(m.labels):''),'helper'));
    const range=m.kind==='gauge'&&s ? [s.min,s.mean,s.max].map(x=>telemetryNumber(x,m.unit)).join(' / ') : '—';
    return [label,m.kind,telemetryNumber(v?.value,m.unit),telemetryNumber(v?.rate,m.unit||'units',true),range,s ? telemetryNumber(s.observed_increase,m.unit) : '—',s ? `${s.samples} / ${s.missing} / ${s.resets}` : 'After collection'];
  });
  $('telemetry-table').replaceChildren(makeTable(['Series','Kind','Latest scrape','Latest rate','Gauge min / mean / max','Observed increase','Samples / gaps / resets'],rows));
  const facts=[point?`Scrape ${point.sequence} · observed at ${fmt(point.elapsed_seconds,2)}s · ${fmt(point.duration_ms,2)}ms to collect`:'Waiting for first scrape near workload admission',summary?`${summary.attempts} recorded scrapes · ${points.length} retained${summary.truncated?' (latest 900)':''} · ${summary.canceled_scrapes} stop-canceled requests`:'Final summary includes every recorded scrape',`Interval ${config.interval||'1s'} · timeout ${config.timeout||'1s'}`];
  if (summary) for (const s of summary.series) if (s.mean_rate!==undefined) facts.push(`${s.metric.title}: observed mean ${telemetryNumber(s.mean_rate,s.metric.unit||'units',true)} over ${fmt(s.covered_seconds,2)}s of continuous readings`);
  $('telemetry-facts').replaceChildren(...facts.map(x=>node('span',x)));
  const selected=$('telemetry-series').value, labels=metrics.map(m=>m.title||m.name), options=[...$('telemetry-series').options];
  if (options.length!==labels.length || options.some((o,i)=>o.textContent!==labels[i])) { $('telemetry-series').replaceChildren(...labels.map((text,i)=>{const option=node('option',text); option.value=String(i); return option;})); $('telemetry-series').value=Number(selected)<metrics.length?selected:'0'; if (!$('telemetry-series').value) $('telemetry-series').value='0'; }
}
$('telemetry-series').addEventListener('change',()=>requestAnimationFrame(drawCharts));
function renderWorkers(run,latest) {
  const urls=run.config.workers||[], distribution=run.result?.distribution;
  $('workers-panel').hidden=!urls.length;
  $('workers-plaintext-warning').hidden=!hasHTTPWorkers(urls);
  if (!urls.length) return;
  const finished=!!run.result;
  $('workers-state').textContent=distribution ? (distribution.complete?'COMPLETE':'INCOMPLETE') : (latest.workers?.length?'COORDINATING':'PREPARING');
  $('workers-warning').hidden=!distribution || distribution.complete;
  $('workers-warning').textContent=distribution ? `${distribution.failure||'Worker measurements are incomplete.'} ${count(distribution.unreported_planned_arrivals)} planned arrivals remain unreported. Global counts include only workers with final results.` : '';
  const rows=urls.map((url,index)=>{
    const w=distribution?.workers.find(x=>x.index===index), point=latest.workers?.find(x=>x.index===index);
    const journeys=run.workload==='journeys', a=w ? (journeys?w.journeys:w.requests) : null;
    const known=!finished || ['finished','interrupted'].includes(w?.status);
    const cpu=w?.generator?.cpu_available ? fmt(w.generator.average_cpu_percent)+'%' : '—';
    const heap=w?.generator ? fmt(w.generator.peak_heap_bytes/1048576)+' MiB' : '—';
    const label=node('div',`${index+1}. ${w?.name||url}`); if (w) label.append(node('div',w.url,'helper')); if (hasHTTPWorkers([url])) label.append(node('div','HTTP · plaintext control','helper'));
    return [label,w?.status||point?.status||'preparing',w ? count(w.planned_arrivals) : '—',known ? count(a?.scheduled??point?.scheduled) : 'Unknown',known ? count(w?.requests.completed??point?.http_completed) : 'Unknown',cpu,heap];
  });
  $('workers-table').replaceChildren(makeTable(['Worker','Status','Assigned arrivals','Reported arrivals','HTTP completed','CPU avg','Heap peak'],rows));
  const facts=distribution ? [`${count(distribution.planned_arrivals)} planned ${run.workload}`,`Concurrency cap: ${run.config.max_in_flight} across ${urls.length} workers`,...distribution.workers.map(w=>`${w.name||w.url}: cap ${w.capacity} · probe RTT ${fmt(w.round_trip_ms,2)} ms · clock offset ${fmt(w.clock_offset_ms,2)} ms`)] : [latest.workers?.length ? 'Latest received worker counters. Allocation details and resource summaries appear after final collection.' : 'Reserving all workers before committing a shared start.'];
  $('workers-facts').replaceChildren(...facts.map(x=>node('span',x)));
}
function renderDiagnostics(run, latest, request, traffic) {
  const summary=run.result?.generator, resources=summary?.final || latest.generator, http=request.http;
  $('generator-panel').hidden=!resources; $('http-panel').hidden=!http;
  $('diagnostics-empty').hidden=!!(resources||http) || active(run);
  if (resources) {
    const cpuAvailable=summary ? summary.cpu_available : resources.cpu_available;
    $('cpu-label').textContent=summary ? 'Process CPU · run average' : 'Process CPU · last interval';
    $('generator-cpu').textContent=cpuAvailable ? fmt(summary ? summary.average_cpu_percent : resources.cpu_percent)+'%' : 'Unavailable';
    $('generator-cpu-note').textContent=summary&&cpuAvailable ? `100% = one core · peak interval ${fmt(summary.peak_cpu_percent)}%` : '100% equals one CPU core';
    $('generator-heap').textContent=fmt(resources.heap_bytes/1048576)+' MiB';
    $('generator-heap-note').textContent=summary ? `Peak sampled ${fmt(summary.peak_heap_bytes/1048576)} MiB` : 'Includes objects awaiting GC';
    $('generator-rss').textContent=resources.rss_available ? fmt(resources.process_peak_rss_bytes/1048576)+' MiB' : 'Unavailable';
    $('generator-goroutines').textContent=count(resources.goroutines);
    $('generator-goroutines-note').textContent=summary ? `Peak sampled ${count(summary.peak_goroutines)}` : 'Process-wide, sampled';
    const facts=[`GC cycles: ${count(resources.gc_cycles)}`, `GC pause: ${fmt(resources.gc_pause_ms,2)} ms`, `Arrival start delay p95: ${fmt(traffic.start_delay?.p95_ms,2)} ms`, `Missed: ${count(traffic.dropped_capacity)} capacity / ${count(traffic.dropped_scheduler)} scheduler`, `GOMAXPROCS: ${resources.gomaxprocs}`];
    if (summary) facts.push(summary.platform,summary.go_version,`${summary.logical_cpus} logical CPUs`,`${summary.samples} resource samples`);
    $('generator-facts').replaceChildren(...facts.map(text=>node('span',text)));
  }
  if (http) {
    const labels=[['Connection acquisition','connection_wait'],['DNS lookup','dns'],['TCP connect','connect'],['TLS handshake','tls'],['Request write','write'],['First header byte from attempt start','first_byte'],['Response body read','body_read']];
    const rows=labels.map(([name,key])=>{const value=http[key]; return [name,count(value.samples),...['mean_ms','p50_ms','p95_ms','max_ms'].map(field=>value.samples ? fmt(value[field],2) : '—')];});
    $('http-timings').replaceChildren(makeTable(['Observed interval','Samples','Mean','p50','p95','Max'],rows));
    const facts=[`${count(http.connections_acquired)} acquired / ${count(http.reused_connections)} reused`,`${count(http.connection_requests)} acquisition requests`,`${count(http.idle_reuses)} idle reuses`,`${count(http.dial_attempts)} TCP dials / ${count(http.dial_failures)} failed`,`${count(http.dns_lookups)} DNS lookups / ${count(http.dns_failures)} failed`,`${count(http.tls_handshakes)} TLS handshakes / ${count(http.tls_failures)} failed`,`${count(http.write_completions)} write completions / ${count(http.write_failures)} failed`,`${count(http.body_read_failures)} body read failures`,`${count(http.unfinished_events)} unfinished pending events`,...Object.entries(http.protocols||{}).map(([protocol,n])=>`${protocol}: ${count(n)} response headers`)];
    $('connection-facts').replaceChildren(...facts.map(text=>node('span',text)));
  }
}
function renderResults(result) {
  GoMeterResults.renderChecks($('checks'), result.checks || [], result);
  const failed = (result.checks || []).filter(c => !c.passed).length;
  $('checks-summary').textContent = `${count((result.checks || []).length - failed)} passed · ${count(failed)} failed${failed ? ' · Failed checks shown first' : ''}`;
  if ($('checks-details').dataset.run !== current.id) { $('checks-details').open = failed > 0; $('checks-details').dataset.run = current.id; }
  $('scenario-results').replaceChildren(...(result.scenarios || []).map(s => {
    const section = node('div', undefined, 'step-section');
    const dataset = result.datasets?.find(d=>d.scenario===s.name);
    if(dataset)section.append(node('p',`CSV: ${dataset.name} · ${count(dataset.rows)} source rows · ${dataset.sharing==='shared'?'shared':'per user'} · ${dataset.on_eof==='recycle'?'recycles':'stops user at EOF'} · ${count(dataset.rows_assigned)} assigned · ${count(dataset.users_exhausted)} users exhausted`,'helper'));
    section.append(node('h3', s.name), node('p', `${count(s.journeys.completed)} journeys · ${fmt(s.journeys.error_percent, 2)}% errors · ${s.journeys.latency.samples ? fmt(s.journeys.latency.p95_ms) + ' ms p95' : 'no latency samples'} · ${s.checks?.length ? (s.passed ? 'checks passed' : 'checks failed') : 'no scenario checks'}`));
    const rows = s.steps.map(step => [step.name, step.method, count(step.completed), count(step.skipped) + (step.skipped_condition ? ` (${count(step.skipped_condition)} conditional)` : ''), step.completed ? fmt(step.error_percent, 2) + '%' : '—', step.latency.samples ? fmt(step.latency.p95_ms) + ' ms' : '—']);
    const wrap = node('div', undefined, 'table-wrap'); wrap.append(makeTable(['Step', 'Method', 'Attempts', 'Skipped journeys', 'Errors', 'p95'], rows)); section.append(wrap);
    if (s.checks?.length) { const checks = node('div'); GoMeterResults.renderChecks(checks, s.checks, s.journeys); section.append(checks); }
    return section;
  }));
}
for (const [id, suffix, extension] of [['download-json', 'report.json', 'json'], ['download-html', 'report.html', 'html'], ['download-run', 'export', 'json']]) $(id).addEventListener('click', async () => {
  downloadURL(`/download/${current.id}/${suffix}`, `gometer-${suffix === 'export' ? 'run-' : ''}${current.id}.${extension}`);
});
function downloadURL(path, filename) {
  const a = node('a'); a.href = path; a.download = filename;
  document.body.append(a); a.click(); a.remove();
}

// Counter deltas are interval measurements. Histogram quantiles are cumulative.
function chartData(run) {
  const values = {traffic: [], journeys: [], target: [], p95: [], p50: [], p99: [], errors: [], drops: []};
  run.samples.forEach((s, index) => {
    const x = s.elapsed_seconds, prev = run.samples[index - 1], r = s.requests || {}, traffic = run.workload === 'journeys' ? (s.journeys || {}) : r;
    const dt = prev ? x - prev.elapsed_seconds : 0, pr = prev?.requests || {}, pt = run.workload === 'journeys' ? (prev?.journeys || {}) : pr;
    if (dt > 0) {
      const completed = (r.completed || 0) - (pr.completed || 0), scheduled = (traffic.scheduled || 0) - (pt.scheduled || 0);
      values.traffic.push([x, Math.max(0, ((r.started || 0) - (pr.started || 0)) / dt)]);
      values.journeys.push([x, Math.max(0, ((s.journeys?.started || 0) - (prev.journeys?.started || 0)) / dt)]);
      values.errors.push([x, completed > 0 ? 100 * ((r.failed || 0) - (pr.failed || 0)) / completed : null]);
      const drops = (traffic.dropped_capacity || 0) + (traffic.dropped_scheduler || 0) - (pt.dropped_capacity || 0) - (pt.dropped_scheduler || 0);
      values.drops.push([x, scheduled > 0 ? 100 * drops / scheduled : null]);
    }
    values.target.push([x, run.config.mode === 'rate' ? (s.draining ? 0 : s.target_rate) : null]);
    values.p95.push([x, r.latency?.samples ? r.latency.p95_ms : null]); values.p50.push([x, r.latency?.samples ? r.latency.p50_ms : null]); values.p99.push([x, r.latency?.samples ? r.latency.p99_ms : null]);
  });
  return values;
}
const colors = {teal: '#087f75', blue: '#527bca', gray: '#94a5af', red: '#bd3c45', amber: '#b27b14'};
function drawChart(id, series, xEnd, fixedMax, xStart = 0, signed = false) {
  GoMeterResults.drawChart($(id), series, {xEnd, fixedMax, xStart, signed, scope:current.id, empty:current.result ? 'No recorded measurements' : 'Waiting for measurements'});
}

function duration(value) {
  const match = String(value).matchAll(/([0-9]*\.?[0-9]+)(ns|us|µs|μs|ms|s|m|h)/g);
  let seconds = 0; for (const m of match) seconds += Number(m[1]) * ({ns: 1e-9, us: 1e-6, 'µs': 1e-6, 'μs': 1e-6, ms: .001, s: 1, m: 60, h: 3600}[m[2]]); return seconds;
}
function drawCharts() {
  if (!current || view !== 'live') return;
  const theme = getComputedStyle(document.documentElement);
  for (const name of Object.keys(colors)) colors[name] = theme.getPropertyValue(`--chart-${name}`).trim();
  const v = chartData(current), end = current.samples.at(-1)?.elapsed_seconds || 1, start = current.samples[0]?.elapsed_seconds || 0;
  const traffic = [{name:'HTTP attempts/s', unit:'req/s', points:v.traffic, color:colors.teal}];
  if (current.config.mode === 'rate') traffic.push({name:'Target arrivals/s', unit:current.workload === 'journeys' ? 'journeys/s' : 'req/s', points:v.target, color:colors.gray, dashed:true, step:true});
  if (current.workload === 'journeys') traffic.push({name:'Journeys/s', unit:'journeys/s', points:v.journeys, color:colors.blue});
  drawChart('traffic-chart', traffic, end, null, start);
  drawChart('latency-chart', [{name:'p50', unit:'ms', points:v.p50, color:colors.blue}, {name:'p95', unit:'ms', points:v.p95, color:colors.teal}, {name:'p99', unit:'ms', points:v.p99, color:colors.amber, dashed:true}], end, null, start);
  drawChart('errors-chart', [{name:'Request errors', unit:'%', points:v.errors, color:colors.red}, {name:'Missed arrivals', unit:'%', points:v.drops, color:colors.amber}], end, 100, start);
  const phases = current.config.stages?.length ? current.config.stages : [{name: current.config.name, duration: current.config.duration, rate: current.config.rate}];
  let offset = 0, phasePoints = [];
  $('phase-list').replaceChildren(...phases.map(p => {
    const d = p.duration ? duration(p.duration) : end, target = current.config.mode === 'rate' ? (p.rate || 0) : current.config.vus;
    phasePoints.push([offset, target], [offset + d, target]);
    offset += d; return node('span', `${p.name || 'Phase'} · ${current.config.mode === 'rate' ? (p.rate || 0) + '/s' : current.config.vus + ' users'} · ${current.config.iterations ? count(current.config.iterations) + ' iterations/user' + (p.duration ? ' or ' + p.duration : '') : p.duration}`);
  }));
  const userSchedule = current.result?.user_schedule || current.samples.at(-1)?.user_schedule;
  if (userSchedule) {
    const delay = userSchedule.planned_startup_seconds, ramp = userSchedule.ramp_up_seconds, users = current.config.vus;
    const lastStart = delay + ramp*(users-1)/users;
    const horizon = current.planned_seconds ? delay + current.planned_seconds : Math.max(end,lastStart);
    const plannedUsers = [[0,0]], points = Math.min(users,200);
    if (!ramp) plannedUsers.push([delay,users]);
    else for (let i=0;i<points;i++) {
      const index = points === 1 ? 0 : Math.floor(i*(users-1)/(points-1)), due = delay + ramp*index/users;
      if (due >= horizon && current.planned_seconds) break;
      plannedUsers.push([due,index+1]);
    }
    plannedUsers.push([Math.max(horizon,end),plannedUsers.at(-1)[1]]);
    const observed = current.samples.map(s=>[s.elapsed_seconds,s.user_schedule?.users.active ?? null]);
    $('phase-list').replaceChildren(node('span',`Planned user starts (gray) · observed active users (blue, sampled) · ${fmt(delay)}s startup · ${fmt(ramp)}s ramp${users>200?' · plan simplified to 200 points':''}`));
    $('phase-chart').setAttribute('aria-label','Planned user starts and sampled active users over wall time');
    drawChart('phase-chart', [{name:'Planned user starts',unit:'users',points:plannedUsers,color:colors.gray,dashed:true,step:true},{name:'Observed active users',unit:'users',points:observed,color:colors.blue,step:true}], Math.max(horizon,end) || 1);
  } else {
    $('phase-chart').setAttribute('aria-label','Configured phase targets over elapsed time');
    drawChart('phase-chart', [{name:current.config.mode === 'users' ? 'Configured users' : 'Target arrivals/s',unit:current.config.mode === 'users' ? 'users' : current.workload === 'journeys' ? 'journeys/s' : 'req/s',points:phasePoints,color:colors.teal,step:true}], current.planned_seconds || offset || 1);
  }
  if (!$('generator-panel').hidden) {
    const cpu=[],heap=[],memory=[];
    for (const sample of current.samples) {
      const r=sample.generator;
      cpu.push([sample.elapsed_seconds,r?.cpu_available ? r.cpu_percent : null]);
      heap.push([sample.elapsed_seconds,r ? r.heap_bytes/1048576 : null]);
      memory.push([sample.elapsed_seconds,r ? r.runtime_bytes/1048576 : null]);
    }
    drawChart('cpu-chart',[{name:'Process CPU',unit:'%',points:cpu,color:colors.teal}],end,null,start);
    drawChart('memory-chart',[{name:'Allocated heap',unit:'MiB',points:heap,color:colors.teal},{name:'Mapped by Go runtime',unit:'MiB',points:memory,color:colors.blue}],end,null,start);
  }
  drawTelemetryChart();
}
window.addEventListener('gometer:themechange', () => requestAnimationFrame(drawCharts));
function drawTelemetryChart() {
  if (!current||view!=='live'||$('telemetry-panel').hidden) return;
  const points=telemetryPoints(current), index=Number($('telemetry-series').value)||0, m=current.result?.telemetry?.series[index]?.metric||current.config.telemetry.metrics[index];
  const values=points.map(p=>{const v=p.values[index], value=m.kind==='counter'?v?.rate:v?.value; return [p.elapsed_seconds,value===undefined?null:(m.unit==='bytes'?value/1048576:value)];});
  $('telemetry-unit').textContent=(m.unit==='bytes'?'MiB':m.unit||'units')+(m.kind==='counter'?'/s · observed':' · sampled');
  const end=Math.max(current.samples.at(-1)?.elapsed_seconds||1,points.at(-1)?.elapsed_seconds||1);
  drawChart('telemetry-chart',[{name:m.title||m.name,unit:(m.unit==='bytes'?'MiB':m.unit||'units')+(m.kind==='counter'?'/s':''),points:values,color:colors.blue}],end,null,points[0]?.elapsed_seconds||0,true);
}
window.addEventListener('resize', () => requestAnimationFrame(drawCharts));
window.addEventListener('gometer:layoutchange', () => { if (view === 'live') requestAnimationFrame(drawCharts); });

function makeTable(headers, rows) {
  const table = node('table'), head = node('thead'), body = node('tbody'), tr = node('tr'); tr.append(...headers.map(t => node('th', t))); head.append(tr);
  for (const row of rows) { const r = node('tr'); for (const value of row) { const td = node('td'); td.append(value instanceof Node ? value : document.createTextNode(String(value))); r.append(td); } body.append(r); }
  table.append(head, body); return table;
}
function renderHistory() {
  selectedRuns = new Set([...selectedRuns].filter(id => runs.some(r => r.id === id)));
  if (!runs.length) { $('history').replaceChildren(node('div', 'Completed and interrupted runs will appear here.', 'empty-small')); updateCompare(); return; }
  const query = $('history-search').value.trim().toLowerCase();
  const visibleRuns = runs.filter(r => `${r.name} ${r.status} ${r.workload}`.toLowerCase().includes(query));
  if (!visibleRuns.length) { $('history').replaceChildren(node('p', 'No runs match your search.', 'helper')); updateCompare(); return; }
  const rows = visibleRuns.map(r => {
    const checkbox = node('input', undefined, 'history-select'); checkbox.type = 'checkbox'; checkbox.checked = selectedRuns.has(r.id); checkbox.setAttribute('aria-label', 'Select ' + r.name + ' for comparison'); checkbox.disabled = ['running', 'stopping', 'abandoned', 'error'].includes(r.status);
    checkbox.addEventListener('change', () => {
      if (checkbox.checked && selectedRuns.size >= 2) { checkbox.checked = false; notice('Select at most two runs to compare.', true); return; }
      if (checkbox.checked) selectedRuns.add(r.id); else selectedRuns.delete(r.id); updateCompare();
    });
    const label = node('div', r.name || 'HTTP test'); label.append(node('small', new Date(r.created_at).toLocaleString()));
    if (r.worker_snapshots?.length) label.append(node('small', 'Workers: ' + r.worker_snapshots.map(w => `${w.name} (${w.origin})`).join(', ')));
    const actions = node('div');
    const open = node('button', 'View', 'text-button'); open.addEventListener('click', async () => { try { await loadRun(r.id); showView('live'); } catch (e) { notice(e.message, true); } });
    const reuse = node('button', 'Reuse', 'text-button'); reuse.addEventListener('click', async () => { try { const c = await (await api(`/api/runs/${r.id}/config`)).json(); setEditor(c); showView('build'); notice('Run configuration loaded.'); } catch (e) { notice(e.message, true); } });
    const remove = node('button', 'Delete', 'text-button'); remove.disabled = active(r); remove.addEventListener('click', async () => {
      if (!confirm('Delete this saved run and its results? Export it first if you need a copy.')) return;
      try { await api(`/api/runs/${r.id}`, 'DELETE'); if (current?.id === r.id) { current = null; $('live-empty').hidden = false; $('run-content').hidden = true; } await refresh(); notice('Run deleted.'); } catch (e) { notice(e.message, true); }
    });
    actions.append(open, reuse, remove); return [checkbox, label, r.workload, node('span', r.status.toUpperCase(), 'status ' + r.status), actions];
  });
  $('history').replaceChildren(makeTable(['', 'Test', 'Workload', 'Result', 'Actions'], rows)); updateCompare();
}
function updateCompare() { $('compare').disabled = selectedRuns.size !== 2; $('compare').textContent = `Compare selected (${selectedRuns.size}/2)`; }
$('history-search').addEventListener('input', renderHistory);
$('refresh-history').addEventListener('click', () => refresh().catch(e => notice(e.message, true)));
$('compare').addEventListener('click', async () => {
  try {
    const [a, b] = await Promise.all([...selectedRuns].map(async id => (await api(`/api/runs/${id}`)).json()));
    if (!a.result || !b.result) throw new Error('Both runs need final results to compare.');
    const A = a.result, B = b.result, arrivalA = A.workload === 'journeys' ? A.journeys : A, arrivalB = B.workload === 'journeys' ? B.journeys : B;
    const rows = [
      ['Target URL', A.url, B.url, '—'], ['Workload', A.workload, B.workload, '—'], ['Model', A.mode, B.mode, '—'], ['HTTPS verification', A.insecure ? 'Disabled' : 'Enabled', B.insecure ? 'Disabled' : 'Enabled', '—'], ['Result', a.status, b.status, '—'],
    ];
    function metric(label, x, y, unit, digits = 1, deltaUnit = unit) {
      const delta = y - x, sign = delta > 0 ? '+' : '', pct = x !== 0 ? ` (${sign}${fmt(100 * delta / Math.abs(x), 1)}%)` : '';
      rows.push([label, fmt(x, digits) + unit, fmt(y, digits) + unit, node('span', sign + fmt(delta, digits) + deltaUnit + pct, 'delta')]);
    }
    metric('Load window', A.load_seconds, B.load_seconds, 's'); metric('Concurrency limit', A.concurrency_limit, B.concurrency_limit, '', 0);
    metric('Started HTTP attempts', A.started, B.started, '', 0); metric('Average started HTTP rate', A.started_rps, B.started_rps, ' req/s');
    if (A.latency.samples && B.latency.samples) metric('Request p95', A.latency.p95_ms, B.latency.p95_ms, ' ms'); else rows.push(['Request p95', A.latency.samples ? fmt(A.latency.p95_ms)+' ms' : 'No samples', B.latency.samples ? fmt(B.latency.p95_ms)+' ms' : 'No samples', '—']);
    metric('Request errors', A.error_percent, B.error_percent, '%', 2, ' percentage points');
    metric('Missed workload demand', arrivalA.drop_percent, arrivalB.drop_percent, '%', 2, ' percentage points');
    if (A.workload === 'journeys' && B.workload === 'journeys') { metric('Journey arrivals', A.journeys.started_per_second, B.journeys.started_per_second, ' journeys/s'); if (A.journeys.latency.samples && B.journeys.latency.samples) metric('Journey p95', A.journeys.latency.p95_ms, B.journeys.latency.p95_ms, ' ms'); metric('Journey errors', A.journeys.error_percent, B.journeys.error_percent, '%', 2, ' percentage points'); }
    if (A.http?.first_byte?.samples && B.http?.first_byte?.samples) metric('First header byte p95',A.http.first_byte.p95_ms,B.http.first_byte.p95_ms,' ms');
    if (A.http?.body_read?.samples && B.http?.body_read?.samples) metric('Body read p95',A.http.body_read.p95_ms,B.http.body_read.p95_ms,' ms');
    if (A.generator && B.generator) {
      if (A.generator.cpu_available && B.generator.cpu_available) metric('Generator average CPU',A.generator.average_cpu_percent,B.generator.average_cpu_percent,'%',1,' percentage points');
      metric('Peak sampled Go heap',A.generator.peak_heap_bytes/1048576,B.generator.peak_heap_bytes/1048576,' MiB');
    }
    // Configuration mismatches are visible; no causal or automatic verdict.
    const mismatches = [];
    for (const [key, label] of [['url', 'target'], ['mode', 'model'], ['rate', 'rate'], ['stages', 'phases'], ['duration', 'planned duration'], ['iterations', 'iterations per user'], ['ramp_up', 'user ramp'], ['startup_delay', 'startup delay'], ['vus', 'user count'], ['max_in_flight', 'capacity'], ['scenarios', 'journey definitions'], ['method', 'method'], ['body', 'body'], ['headers', 'headers'], ['timeout', 'timeout'], ['think_time', 'think time'], ['arrival_distribution', 'arrival spacing'], ['seed', 'seed'],['workers','worker pool'],['telemetry','target telemetry']]) if (JSON.stringify(a.config[key]) !== JSON.stringify(b.config[key])) mismatches.push(label);
    if (A.workload !== B.workload) mismatches.push('arrival unit'); if (A.interrupted || B.interrupted) mismatches.push('partial run');
    const hasDiagnostics=result=>!!(result.generator||result.http||result.distribution?.workers?.some(w=>w.generator||w.requests?.http));
    if (!!a.config.insecure !== !!b.config.insecure) mismatches.push('HTTPS certificate verification');
    if (hasDiagnostics(A)!==hasDiagnostics(B)) mismatches.push('diagnostics instrumentation');
    const environment=result=>result.distribution ? result.distribution.workers.filter(w=>w.generator).map(w=>[w.index,w.generator.platform,w.generator.go_version,w.generator.logical_cpus,w.generator.final.gomaxprocs]) : (result.generator ? [[0,result.generator.platform,result.generator.go_version,result.generator.logical_cpus,result.generator.final.gomaxprocs]] : []);
    const envA=environment(A),envB=environment(B);
    if (envA.length && envB.length && JSON.stringify(envA)!==JSON.stringify(envB)) mismatches.push('generator environment');
    if ((a.config.on_error || 'next_iteration') !== (b.config.on_error || 'next_iteration')) mismatches.push('failure action');
    $('comparison-warning').hidden = !mismatches.length; $('comparison-warning').textContent = 'Conditions differ: ' + mismatches.join(', ') + '.';
    $('comparison-table').replaceChildren(makeTable(['Measurement', `${a.name} (A)`, `${b.name} (B)`, 'B − A'], rows));
    $('comparison').hidden = false; $('comparison').scrollIntoView({behavior: 'smooth', block: 'start'});
  } catch (e) { notice(e.message, true); }
});

function renderCoverage() {
  const query = $('coverage-search').value.toLowerCase(), status = $('coverage-status').value;
  const rows = coverage.features.filter(f => (!status || f.status === status) && `${f.name} ${f.group} ${f.detail}`.toLowerCase().includes(query));
  $('coverage-count').textContent = `${rows.length} of ${coverage.features.length} capability families · Available = stated capability supported; Partial = limitations listed; Not available = backlog.`;
  $('coverage-table').replaceChildren(makeTable(['Capability / JMeter reference', 'GoMeter today', 'Coverage', 'Next stage'], rows.map(f => {
    const name = node('div', f.name), link = node('a', f.group + ' ↗'); link.href = f.source; link.target = '_blank'; link.rel = 'noopener noreferrer'; name.append(link);
    return [name, f.detail, node('span', {available:'Available',partial:'Partial',missing:'Not available'}[f.status], 'coverage-status '+f.status), f.stage];
  })));
}
$('coverage-search').addEventListener('input', renderCoverage);
$('coverage-status').addEventListener('change', renderCoverage);

// Help changes views without replacing the editor or starting work.
function openGuide(topic) {
  showView('guide');
  const heading = $(topic);
  heading.scrollIntoView({block: 'start'});
  heading.focus({preventScroll: true});
}
function openBuilderField(id) {
  showView('build');
  showBuilderTab('settings');
  const target = $(id), details = target.closest('details');
  if (details) details.open = true;
  target.scrollIntoView({block: 'center'});
  if (!target.matches('input,select,textarea,button')) target.tabIndex = -1;
  target.focus({preventScroll: true});
}
for (const button of document.querySelectorAll('[data-guide-topic]')) button.addEventListener('click', () => openGuide(button.dataset.guideTopic));
for (const button of document.querySelectorAll('[data-builder-target]')) button.addEventListener('click', () => openBuilderField(button.dataset.builderTarget));

const welcomePreference = 'gometer.welcome.dismissed.v1';
function setWelcomeVisible(visible) {
  $('welcome-tip').hidden = !visible;
  try { if (visible) localStorage.removeItem(welcomePreference); else localStorage.setItem(welcomePreference, 'true'); }
  catch { /* Guides also work when browser storage is unavailable. */ }
}
let welcomeDismissed = false;
try { welcomeDismissed = localStorage.getItem(welcomePreference) === 'true'; } catch { /* Show the tip for this visit. */ }
$('welcome-tip').hidden = welcomeDismissed;
$('dismiss-welcome').addEventListener('click', () => { setWelcomeVisible(false); $('builder-settings-tab').focus({preventScroll: true}); });
$('show-welcome').addEventListener('click', () => { setWelcomeVisible(true); showView('build'); $('welcome-title').scrollIntoView({block: 'center'}); $('welcome-tip').querySelector('button').focus({preventScroll: true}); });
$('load-practice').addEventListener('click', () => {
  const example = examples.find(e => e.id === 'smoke');
  if (!example) { notice('The starter configurations are still loading. Reload if the workspace is unavailable.', true); return; }
  const c = JSON.parse(JSON.stringify(example.config));
  Object.assign(c, {name: 'My first test', method: 'GET', mode: 'rate', rate: 5, duration: '2s', max_in_flight: 2, timeout: '1s', insecure: false});
  c.thresholds = {p95: '200ms', max_error_percent: 0, max_drop_percent: 0, min_started_rps: null};
  setEditor(c);
  showView('build');
  notice('Practice settings loaded. Start the demo server, then Validate and Start run when you are ready. Loading the guide sends no traffic.');
  $('url').focus({preventScroll: true});
});

async function initialize() {
  try {
    examples = await (await api('/api/examples')).json();
    $('starter').append(...examples.map(e => { const option = node('option', e.name); option.value = e.id; return option; }));
    coverage = await (await api('/coverage.json')).json(); renderCoverage();
    setEditor(examples[0].config);
    $('load-practice').disabled = false;
    await refresh();
    if (activeID) { showView('live'); schedulePoll(); } else showView('tests');
  } catch (e) { notice('Cannot load the local workspace: ' + e.message, true); }
}
initialize();
