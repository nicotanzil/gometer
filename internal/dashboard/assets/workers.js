'use strict';

(function(root) {
  function key(origin) {
    try { const u=new URL(origin);u.hostname=u.hostname.toLowerCase().replace(/\.$/,'');return u.origin; }
    catch { return origin; }
  }
  // Catalog refreshes never rewrite the origins captured in a configuration.
  function selection() {
    let chosen=[],catalog=[],mode='local';
    return {
      load(origins) { chosen=[...origins];mode=chosen.length?'workers':'local'; },
      catalog(entries) { catalog=entries.map(entry=>({...entry})); },
      entries:()=>catalog.map(entry=>({...entry})),
      mode:()=>mode,
      chooseMode(value) {mode=value;if(value==='local')chosen=[];},
      toggle(origin,checked) {if(checked){if(!chosen.some(value=>key(value)===key(origin)))chosen.push(origin);}else chosen=chosen.filter(value=>key(value)!==key(origin));},
      selected:()=>[...chosen],
      includes:origin=>chosen.some(value=>key(value)===key(origin)),
      unknown:()=>chosen.filter(origin=>!catalog.some(entry=>key(entry.origin)===key(origin))),
      origins() {if(mode==='local')return [];if(!chosen.length)throw new Error('Select at least one worker, or choose This machine.');if(chosen.length>32)throw new Error('Select at most 32 workers for one run.');return [...chosen];}
    };
  }
  const states={
    reachable:['Reachable · idle','Authenticated status received. This is an observation, not a reservation.'],
    busy:['Busy','The worker reported an existing reservation or run.'],
    unreachable:['Unreachable','Could not connect. Check the address, worker process, routing, and firewall.'],
    authentication_failed:['Authentication failed','Check GOMETER_WORKER_TOKEN at worker and dashboard startup.'],
    transport_failed:['TLS / transport failed','Check certificate trust, GOMETER_WORKER_CA, and startup permission for remote HTTP.'],
    incompatible:['Incompatible response','The address did not return a supported GoMeter worker status response.']
  };
  function create({api,onChange,show}) {
    const $=id=>document.getElementById(id), model=selection();
    const el=(tag,text,cls)=>{const e=document.createElement(tag);if(text!==undefined)e.textContent=text;if(cls)e.className=cls;return e;};
    const button=(text,action,cls='text-button')=>{const b=el('button',text,cls);b.type='button';b.addEventListener('click',action);return b;};
    let editID='',workloadMode='users',busy=new Set();
    const status=worker=>states[worker.check?.state]||['Not checked','Use Check connection to read the worker’s current status.'];
    function message(text,bad=false) {const n=$('worker-message');n.textContent=text;n.hidden=!text;n.classList.toggle('bad',bad);}
    function clearForm() {editID='';$('worker-form').reset();$('worker-form-title').textContent='Register a worker';$('worker-save').textContent='Register worker';$('worker-cancel').hidden=true;}
    function edit(worker) {
      editID=worker.id||'';$('worker-name').value=worker.id?worker.name:'';$('worker-origin').value=worker.origin;
      $('worker-form-title').textContent=editID?'Edit worker':'Register a worker';$('worker-save').textContent=editID?'Save changes':'Register worker';$('worker-cancel').hidden=false;
      show();$('worker-name').focus();
    }
    function setCatalog(entries) {model.catalog(entries);renderList();renderSelection();}
    async function refresh() {const entries=await(await api('/api/workers')).json();setCatalog(entries);}
    async function check(worker) {
      busy.add(worker.id);renderList();message(`Checking ${worker.name}…`);
      try {const updated=await(await api('/api/workers/'+worker.id+'/check','POST')).json();setCatalog(model.entries().map(entry=>entry.id===updated.id?updated:entry));message(`${updated.name}: ${status(updated)[0]}. No workload was sent.`);}
      catch(error) {message(error.message,true);}
      finally {busy.delete(worker.id);renderList();$('worker-list').querySelector(`[data-check="${worker.id}"]`)?.focus();}
    }
    async function remove(worker) {
      if(!window.confirm(`Remove “${worker.name}” from the catalog? Existing configurations and runs keep their worker origins.`))return;
      try {await api('/api/workers/'+worker.id,'DELETE');if(editID===worker.id)clearForm();await refresh();message('Worker removed from the catalog. Existing configurations keep their origins.');}
      catch(error) {message(error.message,true);}
    }
    function renderList() {
      const list=$('worker-list'),entries=model.entries();$('worker-count').textContent=entries.length;
      if(!entries.length){list.replaceChildren(el('p','No workers registered. Add a name and the origin of a running GoMeter worker to get started.','empty-small'));return;}
      const table=el('table'),head=el('thead'),row=el('tr'),body=el('tbody');
      for(const title of ['Worker','Last observation','Actions'])row.append(el('th',title));head.append(row);
      for(const worker of entries) {
        const tr=el('tr'),identity=el('td'),state=el('td'),actions=el('td'),[label,help]=status(worker);
        identity.append(el('strong',worker.name),el('code',worker.origin,'worker-origin'));
        if(worker.origin.startsWith('http:'))identity.append(el('small','HTTP · control traffic is unencrypted','worker-http'));
        const badge=el('span',label,'worker-status');badge.dataset.state=worker.check?.state||'not_checked';state.append(badge,el('small',help));
        if(worker.check?.checked_at){const date=el('time',new Date(worker.check.checked_at).toLocaleString());date.dateTime=worker.check.checked_at;state.append(date);}
        const checkButton=button(busy.has(worker.id)?'Checking…':'Check connection',()=>check(worker),'button secondary');checkButton.disabled=busy.has(worker.id);checkButton.dataset.check=worker.id;
        const actionGroup=el('div',undefined,'worker-actions');actionGroup.append(checkButton,button('Edit',()=>edit(worker)),button('Remove',()=>remove(worker)));actions.append(actionGroup);
        tr.append(identity,state,actions);body.append(tr);
      }
      table.append(head,body);list.replaceChildren(table);
    }
    function renderSelection() {
      $('execution-local').checked=model.mode()==='local';$('execution-workers').checked=model.mode()==='workers';
      const panel=$('worker-selection');panel.hidden=model.mode()!=='workers';
      const list=$('worker-choices');list.replaceChildren();
      for(const worker of model.entries()) {
        const row=el('label',undefined,'worker-choice'),input=el('input');input.type='checkbox';input.checked=model.includes(worker.origin);input.dataset.worker=worker.id;
        input.addEventListener('change',()=>{model.toggle(worker.origin,input.checked);renderSelection();onChange();list.querySelector(`[data-worker="${worker.id}"]`)?.focus();});
        const text=el('span');text.append(el('strong',worker.name),el('code',worker.origin),el('small',status(worker)[0]));row.append(input,text);list.append(row);
      }
      if(!model.entries().length)list.append(el('p','No catalog entries yet. Register a worker to select it here.','helper'));
      const unknown=$('worker-unregistered');unknown.replaceChildren();
      for(const origin of model.unknown()) {
        const row=el('div',undefined,'worker-legacy'),text=el('div');text.append(el('strong','From this configuration'),el('code',origin),el('small','Not registered. This origin is still selected and will be used.'));
        const actions=el('div',undefined,'worker-actions');actions.append(button('Register',()=>edit({origin})),button('Deselect',()=>{model.toggle(origin,false);renderSelection();onChange();}));row.append(text,actions);unknown.append(row);
      }
      $('worker-selection-summary').textContent=`${model.selected().length} selected · maximum 32 per run`;
      const restriction=$('worker-mode-warning');restriction.hidden=workloadMode==='rate'||model.mode()==='local';
      $('worker-empty-warning').hidden=model.mode()!=='workers'||model.selected().length>0;
    }
    for(const [id,value] of [['execution-local','local'],['execution-workers','workers']])$(id).addEventListener('change',()=>{model.chooseMode(value);renderSelection();onChange();});
    $('worker-manage').addEventListener('click',()=>{show();refresh().catch(error=>message(error.message,true));});
    $('worker-refresh').addEventListener('click',()=>refresh().then(()=>message('Catalog refreshed. No connection checks were sent.')).catch(error=>message(error.message,true)));
    $('worker-cancel').addEventListener('click',clearForm);
    $('worker-form').addEventListener('submit',async event=>{
      event.preventDefault();$('worker-save').disabled=true;
      try {const updated=await(await api('/api/workers'+(editID?'/'+editID:''),editID?'PUT':'POST',{name:$('worker-name').value,origin:$('worker-origin').value})).json();clearForm();await refresh();message(`${updated.name} saved. Use Check connection for a status observation.`);}
      catch(error){message(error.message,true);}
      finally{$('worker-save').disabled=false;}
    });
    document.addEventListener('gometer:viewchange',event=>{if(event.detail==='workers')refresh().catch(error=>message(error.message,true));});
    renderList();renderSelection();
    return {updateCatalog:setCatalog,load(origins,mode){model.load(origins);workloadMode=mode;renderSelection();if(origins.length)$('execution-settings').open=true;},origins:()=>model.origins()};
  }
  const api={selection,create};if(typeof module!=='undefined'&&module.exports)module.exports=api;else root.GoMeterWorkers=api;
})(globalThis);
