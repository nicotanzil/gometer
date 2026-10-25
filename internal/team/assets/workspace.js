'use strict';

// The server owns identity, authorization, revision checks and draft storage.
// This page never stores workload data or tokens in browser storage.
(() => {
  const $ = id => document.getElementById(id);
  const clone = value => JSON.parse(JSON.stringify(value));
  const node = (tag, text, cls) => { const n = document.createElement(tag); if (text !== undefined) n.textContent = text; if (cls) n.className = cls; return n; };
  const action = (text, handler, cls) => { const b = node('button', text, cls); b.type = 'button'; b.addEventListener('click', handler); return b; };
  const state = {session: null, identityInvalid: false, ownerID: null, meta: null, base: null, draft: null, bindings: [], refs: [], kept: '', changed: false, generation: 0, busy: false, draftConflict: false};
  let canvas, canvasIssue = '', timer, searchTimer, listGeneration = 0, draftQueue = Promise.resolve();
  const canEdit = () => !state.identityInvalid && state.session && state.session.member.role !== 'viewer';
  const notice = (text, bad = false) => { $('status').textContent = text; $('status').classList.toggle('error', bad); };
  const note = text => { $('draft-status').textContent = text; };
  function rawDocument() { return `{"schema_version":1,"public_config":${$('public-json').value},"secret_bindings":${JSON.stringify(state.bindings)}}`; }
  const signature = () => JSON.stringify(state.base) + rawDocument();
  const unkept = () => Boolean(canvasIssue) || (state.changed && signature() !== state.kept);
  async function api(path, options = {}) {
    if(state.identityInvalid)throw Error('The account changed. Reload to continue.');
    let response;
    try {
      response = await fetch(path, {...options, headers: {'Content-Type': 'application/json', 'X-Gometer-CSRF': state.session?.csrf_token || '', ...options.headers}});
    } catch { throw Error('Connection lost. Your unfinished changes stay in this tab.'); }
    if (response.status === 401) { state.session = null; permissions(); const error=Error('Your session ended. Sign in again; kept drafts remain private to you.'); error.status=401; notice(error.message,true); throw error; }
    if (response.status === 204) return {data: null, response};
    let data;
    try { data = await response.json(); } catch { throw Error('The workspace returned an unexpected response. Your changes stay in this tab.'); }
    if(state.identityInvalid)throw Error('The account changed. Reload to continue.');
    if (!response.ok) {
      const error = Error(data.error || (response.status === 401 ? 'Your session ended. Sign in again; kept drafts remain private to you.' : 'The request could not be completed.'));
      error.status = response.status;
      if (response.status === 401) { state.session = null; permissions(); notice(error.message, true); }
      throw error;
    }
    return {data, response};
  }
  function permissions() {
    const edit = canEdit() && !state.busy;
    $('fields').disabled = !edit;
    for (const id of ['new-test','save-test','keep-draft','copy-test','add-binding','save-new','fork-draft']) $(id).disabled = !edit;
    $('export-test').disabled = state.busy || !state.meta;
    $('journey').inert = state.busy;
    document.querySelectorAll('.test-card,.draft-row button').forEach(b => { b.disabled = state.busy || (b.dataset.write === 'true' && !canEdit()); });
  }
  async function sessionCheck(initial = false) {
    if(state.identityInvalid)return false;
    const response = await fetch('/api/session');
    if(state.identityInvalid)throw Error('The account changed. Reload to continue.');
    if (!response.ok) { state.session = null; permissions(); notice('Sign in again to continue. Kept drafts remain private to you.', true); return false; }
    const session = await response.json();
    if(state.identityInvalid)return false;
    if (!initial && state.ownerID && state.ownerID !== session.member.id) {
      // An old tab must not show private drafts to the next signed-in person.
      canvas?.destroy(); canvas = null; $('editor').hidden = true; $('empty').hidden = false;
      $('public-json').value = ''; $('bindings').replaceChildren(); $('tests').replaceChildren(); $('drafts').replaceChildren();
      state.identityInvalid = true; state.session = null; state.changed = false; state.bindings = []; state.meta = null; state.base = null; state.draft = null; state.generation++; state.kept = ''; clearTimeout(timer);
      notice('The signed-in account changed. Reload to open that account’s workspace.', true); permissions(); return false;
    }
    const roleChanged = state.session?.member.role !== session.member.role;
    state.ownerID ||= session.member.id; state.session = session;
    document.querySelector('#signout input').value = session.csrf_token;
    permissions(); if (roleChanged && !$('editor').hidden) renderCanvas();
    return true;
  }
  async function refreshTests() {
    const request = ++listGeneration;
    const {data} = await api('/api/tests?' + new URLSearchParams({scope: $('scope').value, search: $('search').value}));
    if (request !== listGeneration) return;
    $('tests').replaceChildren();
    if (!data.tests.length) $('tests').append(node('p', 'No tests match. Create a test or change your search.', 'hint empty'));
    for (const test of data.tests) {
      const b = action('', () => guard(() => openTest(test.id)), 'test-card');
      b.setAttribute('aria-pressed', String(state.meta?.id === test.id));
      b.append(node('strong', test.name), node('small', `${test.creator.name} · edited by ${test.editor.name} · r${test.revision}`));
      $('tests').append(b);
    }
    permissions();
  }
  async function refreshDrafts() {
    const {data} = await api('/api/drafts');
    $('drafts').replaceChildren(); $('draft-count').textContent = data.drafts.length;
    if (!data.drafts.length) $('drafts').append(node('p', 'No private drafts yet.', 'hint'));
    for (const draft of data.drafts) {
      const row = node('div', undefined, 'draft-row');
      const resume = action(`Resume ${draft.name}`, () => guard(() => openDraft(draft.id)), 'resume');
      const remove = action('Remove', () => removeDraft(draft)); remove.dataset.write = 'true'; remove.setAttribute('aria-label', `Remove draft ${draft.name}`);
      row.append(resume, remove); $('drafts').append(row);
    }
    permissions();
  }
  function parsePublic() {
    const value = JSON.parse($('public-json').value);
    if (!value || typeof value !== 'object' || Array.isArray(value)) throw Error('Public configuration must be a JSON object.');
    return value;
  }
  function formFromPublic() {
    let c;
    try { c = parsePublic(); $('json-error').textContent = ''; } catch { $('json-error').textContent = 'JSON is unfinished. Fix it here before changing other settings or keeping a draft.'; return; }
    $('name').value = c.name || ''; $('url').value = c.url || ''; $('method').value = c.method || 'GET'; $('body').value = c.body || '';
    $('mode').value = c.mode || 'users'; $('demand').value = c.mode === 'rate' ? (c.rate ?? 100) : (c.vus ?? 10);
    $('demand-label').textContent = c.mode === 'rate' ? 'Requests or journeys / second' : 'Concurrent users';
    $('duration').value = c.duration ?? (c.iterations ? '' : '10s');
    const scheduled = Boolean(c.stages?.length);
    $('duration').disabled = scheduled || Boolean(c.iterations && !c.duration); $('demand').disabled=scheduled;
    $('mode').disabled=scheduled || Boolean(c.iterations || c.ramp_up || c.startup_delay || c.think_time || (c.on_error && c.on_error !== 'next_iteration') || c.scenarios?.some(s=>s.csv));
    $('demand').max=c.mode==='rate'?'1000000':'100000';
    $('count-note').hidden = !scheduled && !(c.iterations && !c.duration);
    $('count-note').textContent=scheduled?'This test uses traffic stages. Its complete schedule is preserved in Advanced configuration.':'This test uses a fixed iteration count. Its full schedule is preserved in Advanced configuration.'; $('insecure').checked = Boolean(c.insecure);
    $('request-fields').hidden = Boolean(c.scenarios?.length);
    $('body').disabled = !canEdit() || state.bindings.some(b => b.path === '/body');
    $('editor-title').textContent = c.name || 'Untitled test';
  }
  function markChanged() {
    state.changed = true;
    note('Changes are not shared. Keeping a private draft…');
    clearTimeout(timer); timer = setTimeout(() => keepDraft(true).catch(() => {}), 1000);
  }
  function updatePublic(patch) {
    try { if(canvasIssue)throw Error(canvasIssue); const c = parsePublic(); patch(c); $('public-json').value = JSON.stringify(c, null, 2); formFromPublic(); renderCanvas(); markChanged(); }
    catch { formFromPublic(); notice(canvasIssue || 'Fix the unfinished JSON in Advanced configuration first. Your text is preserved.', true); }
  }
  function renderBindings() {
    $('bindings').replaceChildren();
    if (!state.bindings.length) $('bindings').append(node('p', 'No managed references attached.', 'hint'));
    state.bindings.forEach((binding, i) => {
      const row = node('div', undefined, 'bindings-row');
      const pathLabel = node('label', 'Field path'), path = node('input'); path.value = binding.path; path.placeholder = '/headers/Authorization'; pathLabel.append(path);
      const refLabel = node('label', 'Managed reference'), select = node('select');
      select.append(new Option('Choose a reference', ''));
      const current = state.refs.find(ref => ref.id === binding.reference_id && ref.revision === binding.revision && ref.enabled);
      if (binding.reference_id && !current) select.append(new Option(`Needs review · pinned revision ${binding.revision}`, `${binding.reference_id}/${binding.revision}`));
      for (const ref of state.refs.filter(ref => ref.enabled)) select.append(new Option(`${ref.name} · r${ref.revision}`, `${ref.id}/${ref.revision}`));
      select.value = binding.reference_id ? `${binding.reference_id}/${binding.revision}` : ''; refLabel.append(select);
      const ensureSlot = () => {
        if (binding.path === '/body') updatePublic(c => { c.body = ''; });
        if (/^\/headers\/[^/]+$/.test(binding.path)) {
          const key = binding.path.slice(9).replace(/~1/g, '/').replace(/~0/g, '~');
          updatePublic(c => { c.headers ||= {}; if (!(key in c.headers)) c.headers[key] = ''; });
        }
      };
      path.addEventListener('input', () => { binding.path = path.value; markChanged(); });
      path.addEventListener('change', () => { binding.path = path.value; ensureSlot(); renderCanvas(); markChanged(); });
      select.addEventListener('change', () => { const [id, rev] = select.value.split('/'); binding.reference_id = id || ''; binding.revision = Number(rev) || 0; ensureSlot(); renderCanvas(); markChanged(); });
      row.append(pathLabel, refLabel, action('Remove', () => { state.bindings.splice(i,1);
        if (/^\/headers\/[^/]+$/.test(binding.path)) updatePublic(c => { const key=binding.path.slice(9).replace(/~1/g,'/').replace(/~0/g,'~'); if(c.headers?.[key] === '')delete c.headers[key]; });
        renderBindings(); formFromPublic(); renderCanvas(); markChanged(); }));
      $('bindings').append(row);
    });
    permissions();
  }
  async function validate() { if (canvasIssue) throw Error(canvasIssue); await api('/api/tests/validate', {method: 'POST', body: rawDocument()}); }
  function renderCanvas() {
    if (canvasIssue) return;
    canvas?.destroy(); canvas = null; $('journey').replaceChildren(); canvasIssue = '';
    let c; try { c = parsePublic(); } catch { return; }
    // Binding paths are positional. Reordering nodes while paths remain pinned
    // could attach a credential to another request, so show a safe preview.
    const preview = !canEdit() || state.bindings.length > 0;
    if (state.bindings.length) $('journey').append(node('p', 'Secret references are attached. The canvas is a preview; review field paths in Advanced configuration when changing the journey structure.', 'hint'));
    const root = node('div'); $('journey').append(root);
    canvas = GoMeterJourneyEditor.create(root, {preview, title: 'Journey canvas', previewFooter: 'Saving a test sends no workload traffic.', subtitle: preview ? 'Inspect requests, branches and loops. Secret values are never loaded.' : 'Build and edit multi-step journeys.',
      read: parsePublic,
      write: value => { $('public-json').value = JSON.stringify(value, null, 2); formFromPublic(); markChanged(); },
      settings: () => { $('advanced').open = true; $('public-json').focus(); },
      validate, save: saveTest, saveLabel: 'Save test',
      onDraftError: text => { canvasIssue = text; if(text)note('Unfinished canvas fields stay in this tab. Correct them before keeping a draft or changing editors.'); },
      guide: 'Solid arrows show execution order. Select a request, branch or loop to inspect it. Drag the background to pan; use Fit and zoom for a larger journey. Tests with managed bindings use a preview so positional secret paths cannot silently move to another request.',
    });
    if (preview && !c.scenarios?.length && c.url) { try { c = GoMeterJourneyModel.mutate(c,{type:'create'}).config; } catch {} }
    canvas.load(c, true);
  }
  function showDocument(doc, meta, draft = null) {
    clearTimeout(timer); canvasIssue = ''; state.generation++; state.meta = meta; state.draft = draft; state.base = draft ? draft.base || null : meta ? {id: meta.id, revision: meta.revision} : null;
    state.bindings = clone(doc.secret_bindings || []); state.changed = false; state.draftConflict = false;
    $('public-json').value = JSON.stringify(doc.public_config, null, 2); state.kept = signature();
    $('empty').hidden = true; $('editor').hidden = false; $('conflict').hidden = true;
    $('editor-kind').textContent = draft ? 'PRIVATE DRAFT' : meta ? 'SHARED TEST' : 'NEW TEST';
    $('revision').textContent = state.base ? `${draft?'Base revision':'Revision'} ${state.base.revision}` : 'Not shared';
    $('attribution').textContent = meta ? `Created by ${meta.creator.name} · last edited by ${meta.editor.name}` : 'Save a test to share it with your workspace.';
    $('copy-test').hidden = !meta || Boolean(draft); $('export-test').hidden = !meta;
    note(draft ? 'Private draft restored. Saving a test publishes it to the library.' : canEdit() ? 'Changes are kept separately until you save the test.' : 'Viewer access: public configuration and exports are available.');
    renderBindings(); formFromPublic(); renderCanvas(); permissions();
  }
  async function openTest(id) {
    state.busy = true; permissions();
    try { const {data} = await api(`/api/tests/${id}/document`); showDocument(data.document, data); await refreshTests(); notice('Shared test opened. Saving or copying sends no workload traffic.'); }
    catch (error) { notice(error.status === 404 ? 'This test has no shared document. Legacy configuration stays private; create a reviewed shared test to use it here.' : error.message, true); } finally { state.busy = false; permissions(); }
  }
  async function openDraft(id) {
    state.busy = true; permissions();
    try {
      const {data} = await api(`/api/drafts/${id}`);
      let meta = null; if (data.base) { try { meta = (await api(`/api/tests/${data.base.id}/document`)).data; } catch (e) { if (e.status !== 404) throw e; } }
      showDocument(data.document, meta, data);
      if (meta && meta.revision !== data.base.revision) conflict('The saved test changed while this draft was open. Your original base revision is preserved.');
      notice('Your private draft is restored.');
    } catch (e) { notice(e.message, true); } finally { state.busy = false; permissions(); }
  }
  function conflict(text, draftConflict = false) {
    state.draftConflict = draftConflict; $('conflict').hidden = false; $('conflict-text').textContent = text;
    $('fork-draft').hidden = !draftConflict; $('compare').hidden = !state.base; $('reload-saved').hidden = !state.base;
  }
  function keepDraft(silent = false, fork = false) {
    const capture = {raw: rawDocument(), base: clone(state.base), generation: state.generation, signature: signature()};
    draftQueue = draftQueue.catch(() => {}).then(async () => {
      if (capture.generation !== state.generation || !canEdit()) return false;
      try {
        if (canvasIssue) throw Error(canvasIssue);
        const current = fork ? null : state.draft;
        const path = current ? `/api/drafts/${current.id}` : '/api/drafts';
        const {data} = await api(path, {method: current ? 'PUT' : 'POST', headers: current ? {'If-Match': `"draft/${current.id}/${current.revision}"`} : {},
          body: `{"base":${JSON.stringify(capture.base)},"document":${capture.raw}}`});
        if (capture.generation !== state.generation) return false;
        state.draft = data; state.kept = capture.signature; if(state.draftConflict)$('conflict').hidden=true; state.draftConflict = false;
        if (!unkept()) note('Private draft saved. Your changes have not been shared.');
        await refreshDrafts().catch(()=>note('Private draft saved. Refresh to update the draft list.')); return true;
      } catch (error) {
        if (error.status === 409 && state.draft) conflict('This draft changed in another tab, or your draft library is full. Your edits stay here. Keep a separate draft or review your saved drafts.', true);
        note('Draft not stored. Your unfinished changes stay in this tab.');
        if (!silent) notice(error.message, true); return false;
      }
    });
    return draftQueue;
  }
  async function guard(next) {
    if (state.busy) return;
    if (unkept()) {
      const dialog = $('leave-dialog'); dialog.returnValue = 'cancel'; dialog.showModal();
      const choice = await new Promise(resolve => dialog.addEventListener('close', () => resolve(dialog.returnValue), {once: true}));
      if (choice === 'cancel' || !choice) return;
      if (choice === 'keep' && !await keepDraft()) return;
      if (choice === 'discard') { clearTimeout(timer); await draftQueue; state.changed = false; canvasIssue = ''; }
    }
    if (!state.busy) await next();
  }
  async function cleanDraft(draft) {
    if (!draft) return;
    try { await api(`/api/drafts/${draft.id}`, {method: 'DELETE', headers: {'If-Match': `"draft/${draft.id}/${draft.revision}"`}}); }
    catch { notice('The test was saved, but its private draft could not be removed. Review it under Your drafts.'); }
  }
  async function saveTest(asNew = false) {
    if (!canEdit() || state.busy) return;
    clearTimeout(timer); state.busy = true; permissions();
    try {
      await draftQueue;
      if (canvasIssue) throw Error(canvasIssue);
      const base = asNew ? null : state.base;
      const {data} = await api(base ? `/api/tests/${base.id}/document` : '/api/tests', {method: base ? 'PUT' : 'POST', headers: base ? {'If-Match': `"test/${base.id}/${base.revision}"`} : {}, body: rawDocument()});
      const oldDraft = state.draft; showDocument(data.document, data);
      notice('Test saved to the shared library. No workload traffic was sent.');
      await cleanDraft(oldDraft); await Promise.all([refreshTests(), refreshDrafts()]).catch(()=>notice('Test saved. Refresh to update the library.'));
    } catch (error) {
      notice(error.message, true);
      if (error.status === 409) conflict('The test or a managed reference changed. Your local edits are preserved. Compare the saved version or save your edits as a new test.');
      if (canEdit()) await keepDraft(true);
    } finally { state.busy = false; permissions(); }
  }
  async function removeDraft(draft) {
    if (state.busy || !canEdit()) return;
    if (!window.confirm(`Remove your private draft “${draft.name}”? This cannot be undone.`)) return;
    clearTimeout(timer); state.busy = true; permissions();
    try {
      await draftQueue;
      const revision = state.draft?.id === draft.id ? state.draft.revision : draft.revision;
      await api(`/api/drafts/${draft.id}`, {method: 'DELETE', headers: {'If-Match': `"draft/${draft.id}/${revision}"`}});
      if (state.draft?.id === draft.id) { state.draft = null; state.kept = ''; state.changed = true; note('Draft removed. Current editor changes remain in this tab.'); }
      await refreshDrafts();
    } catch (e) { notice(e.message, true); } finally { state.busy = false; permissions(); }
  }
  async function compare() {
    if (!state.base) return;
    try {
      const {data} = await api(`/api/tests/${state.base.id}/document`);
      $('compare-local').value = rawDocument(); $('compare-remote').value = JSON.stringify(data.document, null, 2); $('compare-dialog').showModal();
    } catch (e) { notice(e.message, true); }
  }
  $('new-test').addEventListener('click', () => guard(() => {
    showDocument({schema_version:1, public_config:{name:'Untitled test',url:'',method:'GET',mode:'users',vus:10,rate:0,duration:'10s'},secret_bindings:[]},null); $('name').focus();
  }));
  for (const id of ['name','url','method','body','mode','demand','duration','insecure']) $(id).addEventListener('input', () => updatePublic(c => {
    if (id === 'insecure') c.insecure = $('insecure').checked;
    else if (id === 'mode') { c.mode = $('mode').value; c.rate = c.mode === 'rate' ? c.rate || 100 : 0; }
    else if (id === 'demand') c[c.mode === 'rate' ? 'rate' : 'vus'] = Number($('demand').value);
    else c[id] = $(id).value;
  }));
  $('fields').addEventListener('beforeinput', event => { if(canvasIssue){ event.preventDefault(); notice('Correct unfinished canvas fields before editing other settings.',true); } });
  $('public-json').addEventListener('input', () => { formFromPublic(); renderCanvas(); markChanged(); });
  $('add-binding').addEventListener('click', () => { if(canvasIssue){notice(canvasIssue,true);return;} state.bindings.push({path:'/headers/Authorization',reference_id:'',revision:0}); renderBindings(); renderCanvas(); markChanged(); });
  $('save-test').addEventListener('click', () => saveTest()); $('save-new').addEventListener('click', () => saveTest(true));
  $('keep-draft').addEventListener('click', () => keepDraft()); $('fork-draft').addEventListener('click', () => keepDraft(false,true));
  $('compare').addEventListener('click', compare);
  $('reload-saved').addEventListener('click', async () => { const id = state.base?.id; if (id && await keepDraft()) await openTest(id); });
  $('copy-test').addEventListener('click', () => guard(async () => {
    if (!state.meta || !canEdit()) return;
    try { const {data} = await api(`/api/tests/${state.meta.id}/copy`, {method:'POST',headers:{'If-Match':`"test/${state.meta.id}/${state.meta.revision}"`}}); showDocument(data.document,data); await refreshTests(); notice('Independent copy created. The source test is unchanged.'); }
    catch (e) { notice(e.message,true); if(e.status===409) conflict('The saved test or a reference changed. Reload before copying.'); }
  }));
  $('export-test').addEventListener('click', async () => {
    if (!state.meta) return;
    try {
      const {data} = await api(`/api/tests/${state.meta.id}/export`);
      const url=URL.createObjectURL(new Blob([JSON.stringify(data,null,2)],{type:'application/json'}));
      const link=node('a'); link.href=url; link.download='shared-test.json'; document.body.append(link); link.click(); link.remove(); setTimeout(()=>URL.revokeObjectURL(url),1000);
      notice('Exported the saved revision. Unpublished editor changes are excluded.');
    } catch(e) {notice(e.message,true);}
  });
  $('refresh').addEventListener('click', async () => { try { const {data}=await api('/api/secret-references'); state.refs=data.references; renderBindings(); await Promise.all([refreshTests(),refreshDrafts()]); notice('Library and references refreshed. Editor changes are preserved.'); } catch(e){notice(e.message,true);} });
  $('search').addEventListener('input', () => { clearTimeout(searchTimer); searchTimer=setTimeout(()=>refreshTests().catch(e=>notice(e.message,true)),250); });
  $('scope').addEventListener('change', () => refreshTests().catch(e=>notice(e.message,true)));
  $('guide-open').addEventListener('click', () => $('guide-dialog').showModal());
  $('journey-section').addEventListener('toggle', () => { if($('journey-section').open)canvas?.show(); });
  $('signout').addEventListener('submit', event => { event.preventDefault(); guard(()=>{state.changed=false;clearTimeout(timer);$('signout').submit();}); });
  document.addEventListener('keydown', event => {
    if (event.isComposing || event.repeat || document.querySelector('dialog[open]')) return;
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's' && canEdit() && !$('editor').hidden) {
      event.preventDefault(); if (event.shiftKey) keepDraft(); else saveTest();
    } else if (event.key === '?' && !event.target.closest('input,textarea,select,[contenteditable]')) $('guide-dialog').showModal();
  });
  window.addEventListener('beforeunload', event => { if(unkept()){event.preventDefault();event.returnValue='';} });
  window.addEventListener('focus', () => sessionCheck().catch(()=>{}));
  $('sidebar-toggle').addEventListener('click', () => {
    const collapsed=document.body.dataset.collapsed!=='true'; document.body.dataset.collapsed=String(collapsed);
    $('sidebar-toggle').setAttribute('aria-expanded',String(!collapsed));$('sidebar-toggle').setAttribute('aria-label',collapsed?'Expand sidebar':'Collapse sidebar');
  });
  const theme=matchMedia('(prefers-color-scheme: dark)');
  const applyTheme=()=>{document.documentElement.dataset.theme=$('theme').value==='system'?(theme.matches?'dark':'light'):$('theme').value;};
  applyTheme();theme.addEventListener('change',applyTheme);$('theme').addEventListener('change',applyTheme);
  (async () => {
    try { if(!await sessionCheck(true))return; const {data}=await api('/api/secret-references'); state.refs=data.references; await Promise.all([refreshTests(),refreshDrafts()]); notice(canEdit()?'Choose a test, resume a private draft, or create a new test.':'Viewer access: inspect and export public shared tests.'); permissions(); }
    catch(e){notice(e.message,true);}
  })();
})();
