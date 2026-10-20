'use strict';

// A single editor module owns selection, draft errors, history, and diagram navigation.
// Callers supply configuration reads/writes and the existing validate/save/run actions.
globalThis.GoMeterJourneyEditor = (() => {
  const M = globalThis.GoMeterJourneyModel;
  let instance = 0;
  const el = (tag, text, cls) => { const n=document.createElement(tag); if(text!==undefined)n.textContent=text; if(cls)n.className=cls; return n; };
  function button(text, action, cls='je-button') { const n=el('button',text,cls); n.type='button'; n.addEventListener('click',action); const key={Validate:'Enter','Save scenario':'S','Start run':'Shift+Enter',Undo:'Z'}[text];if(key)n.setAttribute('aria-keyshortcuts',`Meta+${key} Control+${key}`);return n; }
  // A value restored before blur may not emit change. Still settle its draft error.
  function watchInput(input,apply,mark) {
    let dirty=false;
    const settle=()=>{dirty=false;apply();};
    input.addEventListener('input',()=>{dirty=true;mark();});
    input.addEventListener('change',settle);
    input.addEventListener('blur',()=>{if(dirty)settle();});
  }
  const svgEl = (tag, attrs={}) => { const n=document.createElementNS('http://www.w3.org/2000/svg',tag);for(const [k,v] of Object.entries(attrs))n.setAttribute(k,v);return n; };
  function create(root, options) {
    const id=`journey-${++instance}`, previewOnly=!!options.preview, readonly=!!options.readonly||previewOnly;
    let config={}, si=0, selected=0, tab=readonly?'results':'request', history=[], errors=new Map(), internal=false, report=null, reportID='', dragging=null;
    let zoom=1, pan={x:0,y:0}, autoFit=true, dataFlow=false, preview=null, world=null, lastWidth=0, pendingFrame=0;
    root.classList.add('je');
    root.innerHTML='<div class="je-heading"><div><div class="eyebrow">FOLLOW THE FLOW</div><h2></h2><p class="je-subtitle"></p></div><div class="je-actions" data-actions></div></div><div class="je-guide" hidden></div><div class="je-message" role="status" aria-live="polite" hidden></div><div class="je-empty" hidden></div><div class="je-workspace" data-mobile-panel="canvas"><aside class="je-library" aria-label="Journey selection"></aside><div class="je-mobile-tabs" role="group" aria-label="Journey editor panel"><button type="button" data-panel="canvas" aria-pressed="true">Canvas</button><button type="button" data-panel="inspector" aria-pressed="false">Inspector</button></div><section class="je-center" aria-label="Journey flow diagram"><div class="je-toolbar"></div><div class="je-viewport" aria-label="Pannable journey diagram"><div class="je-stage"></div></div><div class="je-legend"></div><div class="je-preview" role="status" hidden></div></section><aside class="je-inspector" aria-label="Journey inspector"></aside></div><div class="je-footer"></div>';
    const q = name => root.querySelector(name), viewport=q('.je-viewport'), stage=q('.je-stage');
    function mobilePanel(panel) {
      q('.je-workspace').dataset.mobilePanel=panel;
      q('.je-mobile-tabs [data-panel="canvas"]').setAttribute('aria-pressed',panel==='canvas');
      q('.je-mobile-tabs [data-panel="inspector"]').setAttribute('aria-pressed',panel==='inspector');
    }
    q('.je-mobile-tabs [data-panel="canvas"]').addEventListener('click',()=>mobilePanel('canvas'));
    q('.je-mobile-tabs [data-panel="inspector"]').addEventListener('click',()=>mobilePanel('inspector'));
    q('h2').textContent=previewOnly?'Imported journey preview':readonly?'Journey results':'Journey canvas';
    q('.je-subtitle').textContent=previewOnly?'Inspect the mapped requests and loops. No workload traffic is sent.':readonly?'Observed HTTP attempts and skipped steps from this run.':'Select a node to edit requests, branch conditions, or loop limits.';
    const guide=q('.je-guide');
    guide.textContent='Solid arrows show execution order. Dotted links show extracted variables used later. Select a request for headers, body, checks, and extractions. Drag nodes to reorder or move between paths; the inspector also has Move up/down and Move to path controls. Drag the canvas to pan, or use Fit and zoom. The test failure action decides whether a failed request skips later steps, continues the journey, stops this user, or stops the entire test. Branches choose a True or False path using current variables. Loops repeat a body with a fixed count or a bounded while condition. Invalid conditions, deadlines, cancellation, and exhausted while limits always stop the current journey.';
    if(previewOnly)guide.textContent='Solid arrows show execution order. Select a request or loop to inspect its converted configuration. Drag the background to pan, or use Fit and zoom. This preview has no execution measurements or editing controls. Save the import and open it in the builder to modify or run the journey.';
    const actions=q('[data-actions]');
    actions.append(button('How this works',()=>{guide.hidden=!guide.hidden;},'je-button je-quiet'));
    if(!readonly) for(const [label,action,cls] of [['Test settings',options.settings,'je-button'],['Validate',()=>validate(),'je-button'],['Save scenario',()=>invoke(options.save),'je-button'],['Start run',()=>invoke(options.start),'je-button je-primary']]) actions.append(button(label,action,cls));
    function message(text,bad=false) {const n=q('.je-message');n.hidden=!text;n.textContent=text;n.classList.toggle('je-bad',bad);}
    function draft(key,error='') {
      if(error)errors.set(key,error);else errors.delete(key);
      options.onDraftError?.([...errors.values()].join(' '));
      if(errors.size)message([...errors.values()].join(' '),true);else message('Draft updated. Validate before running.');
    }
    function current() {return config.scenarios?.[si];}
    function entries() {return M.flatten(current()?.steps||[]);}
    function step() {return entries()[selected]?.step;}
    function measurement(index) {const n=entries()[index];if(!n)return null;const r=report?.scenarios?.[si],path=`scenarios[${si}].${n.path}`;if(M.kind(n.step)!=='request')return r?.controls?.find(c=>c.path===path);return r?.steps?.find(s=>s.path===path)||r?.steps?.[entries().slice(0,index).filter(n=>M.kind(n.step)==='request').length];}
    function totalSteps(c=config) {return (c.scenarios||[]).reduce((n,s)=>n+M.flatten(s.steps).length,0);}
    function snapshot(c) {return {config:M.copy(c),si,selected};}
    function retain(s) {history.push(s);while(history.length>20||history.reduce((n,h)=>n+JSON.stringify(h.config).length,0)>8*1048576)history.shift();}
    function commit(action,keepInspector=false) {
      if(errors.size&&!['edit-step','edit-journey','edit-test'].includes(action.type)){message('Correct unfinished inspector fields before changing the journey structure.',true);return;}
      try{
        const before=options.read(), result=M.mutate(before,{scenario:si,index:selected,...action});
        if(JSON.stringify(before)===JSON.stringify(result.config)){draft('mutation');return;}
        retain(snapshot(before));config=result.config;si=result.scenario;selected=result.index;preview=null;report=null;
        internal=true;try{options.write(config);}finally{internal=false;}
        errors.delete('mutation');render(keepInspector);if(!errors.size)message('Draft updated. Validate before running.');return true;
      }catch(e){if(['edit-step','edit-journey','edit-test'].includes(action.type))draft('mutation',e.message);else message(e.message,true);return false;}
    }
    function invoke(action) {if(errors.size){message([...errors.values()].join(' '),true);return;}action?.();}
    async function validate() {
      if(errors.size){message([...errors.values()].join(' '),true);return;}
      let source;try{source=JSON.stringify(options.read());message('Validating configuration…');await options.validate();if(source===JSON.stringify(options.read()))message('Valid configuration. Validation sent no workload traffic.');}
      catch(e){if(source&&source!==JSON.stringify(options.read()))return;message(e.message,true);const match=/scenarios\[(\d+)\]\.(steps\[\d+\](?:\.(?:if\.(?:then|else)|loop\.steps)\[\d+\])*)/.exec(e.message);if(match){si=Number(match[1]);selected=entries().findIndex(n=>n.path===match[2]);tab='request';render();centerSelected();}}
    }
    function select(index) {
      if(errors.size){message('Correct the unfinished inspector fields before selecting another request.',true);return;}
      selected=index;preview=null;autoFit=false;render();centerSelected();
      if(window.matchMedia?.('(max-width:700px)').matches)mobilePanel('inspector');
    }
    function render(keepInspector=false) {
      const c=current(), empty=!config.scenarios?.length;
      q('.je-workspace').hidden=empty;q('.je-footer').hidden=empty;q('.je-empty').hidden=!empty;
      if(empty){const emptyBox=q('.je-empty');emptyBox.replaceChildren(el('h3','Turn requests into a journey'),el('p','A journey runs an ordered sequence of requests, with shared cookies and extracted data. Start with the request in Test settings.'));if(!readonly)emptyBox.append(button('Create journey from current request',()=>{commit({type:'create'});autoFit=true;requestAnimationFrame(fit);},'je-button je-primary'));return;}
      si=Math.max(0,Math.min(si,config.scenarios.length-1));selected=Math.max(-1,Math.min(selected,entries().length-1));
      renderLibrary();renderToolbar();renderGraph();renderFooter();
      if(!keepInspector)renderInspector();
      const previewBox=q('.je-preview');previewBox.hidden=!preview;
      if(preview){const complex=entries().some(n=>M.kind(n.step)!=='request');previewBox.replaceChildren(el('strong',complex?'Structure preview · paths depend on responses':preview.failed===null?'Preview: all requests succeed':'Preview: selected request fails'),el('p',entries().map(n=>`${n.depth?n.lane+': ':''}${M.stepName(n.step,n.index)}${complex&&M.kind(n.step)!=='request'?' ('+M.describe(n.step)+')':!complex&&preview.failed!==null&&config.on_error!=='continue'&&n.index>preview.failed?' [skipped]':!complex&&preview.failed===n.index?' [failed; journey remains failed]' :n.step.pause?` → wait ${n.step.pause}`:''}`).join(' → ')),el('small',complex?'Both branch paths and bounded loop bodies are shown once. Runtime variables decide which requests execute. No traffic sent.':'Sequence preview only. Responses and assertion outcomes are not simulated. No traffic sent.'));}
      if(autoFit)requestAnimationFrame(fit);
    }
    function renderLibrary() {
      const library=q('.je-library');library.replaceChildren(el('div','JOURNEYS','je-label'));
      const sum=config.scenarios.reduce((n,s)=>n+(Number(s.weight??1)||0),0);
      config.scenarios.forEach((s,index)=>{const b=button('',()=>{if(errors.size){message('Correct unfinished inspector fields first.',true);return;}si=index;selected=0;preview=null;autoFit=true;render();},'je-journey');b.setAttribute('aria-pressed',index===si);b.append(el('span',s.name||`Journey ${index+1}`),el('small',sum?`${Math.round((s.weight??1)/sum*100)}%`:'—'));library.append(b);});
      if(!readonly)library.append(button('+ Add journey',()=>commit({type:'add-journey'}),'je-link'));
      library.append(el('div','FLOW OUTLINE','je-label'));
      entries().forEach(({step:s,index,depth,lane})=>{const b=button('',()=>select(index),'je-outline-step');b.setAttribute('aria-pressed',index===selected);b.append(el('span',String(index+1).padStart(2,'0')),el('span',M.stepName(s,index)));b.title=`${lane} · ${M.stepName(s,index)}`;b.style.paddingLeft=`${8+Math.min(depth,8)*10}px`;b.dataset.kind=M.kind(s);if(depth)b.append(el('small',lane,'je-lane'));dragHandlers(b,index);library.append(b);});
      library.append(button(readonly?'Journey summary':'Journey settings & data',()=>select(-1),'je-link'));
      if(!readonly){library.append(el('div','ADD TO JOURNEY','je-label'),button('+ HTTP request',()=>commit({type:'add-step',index:selected<0?entries().length-1:selected}),'je-link'),button('+ Branch',()=>commit({type:'add-branch'}),'je-link'),button('+ Loop',()=>commit({type:'add-loop'}),'je-link'),button('+ Think time',()=>{if(errors.size){message('Correct unfinished inspector fields first.',true);return;}if(!step()||M.kind(step())!=='request'){message('Choose a request to pause after.',true);return;}if(!step().pause)commit({type:'edit-step',patch:{pause:'500ms'}});tab='request';renderInspector();q('[data-field="pause"]')?.focus();},'je-link'));}
      const note=el('p','Branches choose a path. Loops repeat their body. '+(config.on_error==='continue'?'Request errors are retained while later steps continue.':config.on_error==='stop_user'?'A failure stops this user; peers continue.':config.on_error==='stop_test'?'A failure stops new work; other admitted journeys drain.':config.on_error==='stop_test_now'?'A failure cancels active work and pending user starts.':'A failure skips later steps in this journey.'),'je-library-note');library.append(note);
    }
    function renderToolbar() {
      const bar=q('.je-toolbar');bar.replaceChildren();
      const dataLabel=el('label',undefined,'je-toggle'),check=el('input');check.type='checkbox';check.checked=dataFlow;check.addEventListener('change',()=>{dataFlow=check.checked;renderGraph();renderInspector();});dataLabel.append(check,document.createTextNode('Data flow'));bar.append(dataLabel);
      const tools=el('div',undefined,'je-tools');tools.append(button('−',()=>zoomAt(zoom/1.2),'je-button je-icon'),el('span',`${Math.round(zoom*100)}%`,'je-zoom'),button('+',()=>zoomAt(zoom*1.2),'je-button je-icon'),button('Fit',fit,'je-button'),button('Full screen',()=>{if(document.fullscreenElement===root)document.exitFullscreen().catch(()=>{});else root.requestFullscreen?.().catch(()=>message('Full screen is unavailable in this browser.'));},'je-button'));
      tools.children[0].setAttribute('aria-label','Zoom out');tools.children[2].setAttribute('aria-label','Zoom in');bar.append(tools);
      const legend=q('.je-legend');legend.replaceChildren(el('span','— Execution order'),el('span',dataFlow?'┄ Extracted data · selected request':'Drag the background to pan'));
      if(readonly)legend.append(el('span',previewOnly?'Structure preview · no execution measurements':'Failures and skips are observed counts.'));
      else {legend.append(button('Preview sequence',()=>{if(errors.size){message('Correct unfinished inspector fields first.',true);return;}preview={failed:null};render();},'je-link'));if(preview&&selected>=0&&!entries().some(n=>M.kind(n.step)!=='request'))legend.append(button('Preview failure here',()=>{preview={failed:selected};render();},'je-link'));}
    }
    function applyTransform() {stage.style.transform=`translate(${pan.x}px, ${pan.y}px) scale(${zoom})`;q('.je-zoom').textContent=`${Math.round(zoom*100)}%`;}
    function fit() {if(!world||!viewport.clientWidth)return;zoom=Math.min(1.15,Math.max(.04,Math.min((viewport.clientWidth-24)/world.width,(viewport.clientHeight-40)/world.height)));pan={x:(viewport.clientWidth-world.width*zoom)/2,y:(viewport.clientHeight-world.height*zoom)/2};autoFit=true;applyTransform();}
    function zoomAt(next,point={x:viewport.clientWidth/2,y:viewport.clientHeight/2}) {const clamped=Math.min(2,Math.max(.04,next)),factor=clamped/zoom;pan={x:point.x-(point.x-pan.x)*factor,y:point.y-(point.y-pan.y)*factor};zoom=clamped;autoFit=false;applyTransform();}
    function centerSelected() {if(!world||selected<0)return;const point=world.nodes[selected];if(!point)return;zoom=Math.max(.85,zoom);pan={x:viewport.clientWidth/2-(point.x+world.nodeWidth/2)*zoom,y:viewport.clientHeight/2-(point.y+point.height/2)*zoom};autoFit=false;applyTransform();}
    function dragHandlers(n,index) {
      if(readonly)return;n.draggable=true;
      n.addEventListener('dragstart',e=>{if(errors.size){e.preventDefault();return;}dragging=index;e.dataTransfer.setData('text/plain',id);e.dataTransfer.effectAllowed='move';});
      n.addEventListener('dragover',e=>{if(dragging!==null)e.preventDefault();});
      n.addEventListener('drop',e=>{e.preventDefault();if(dragging!==null&&e.dataTransfer.getData('text/plain')===id)commit({type:'move-step',index:dragging,to:index});dragging=null;});
      n.addEventListener('dragend',()=>{dragging=null;});
    }
    function renderGraph() {
      const c=current();if(!c)return;
      const all=entries(),analysis=M.dependencies(config,si);world=M.layout(c.steps);
      stage.replaceChildren();stage.style.width=`${world.width}px`;stage.style.height=`${world.height}px`;
      const svg=svgEl('svg',{width:world.width,height:world.height,'aria-hidden':'true',class:'je-edges'}),defs=svgEl('defs'),marker=svgEl('marker',{id:`${id}-arrow`,viewBox:'0 0 8 8',refX:'6',refY:'4',markerWidth:'6',markerHeight:'6',orient:'auto'});
      marker.append(svgEl('path',{d:'M 1 1 L 6 4 L 1 7',class:'je-arrow'}));defs.append(marker);svg.append(defs);
      for(const edge of world.edges){const {x1,y1,x2,y2,label,bend}=edge;let d=bend!==null?`M ${x1} ${y1} H ${bend} V ${y2} H ${x2}`:x1===x2?`M ${x1} ${y1} V ${y2-4}`:`M ${x1} ${y1} V ${(y1+y2)/2} H ${x2} V ${y2-4}`;svg.append(svgEl('path',{d,class:'je-execution','marker-end':`url(#${id}-arrow)`}));if(label){const text=svgEl('text',{x:bend??x2,y:label==='Repeat'?(y1+y2)/2:Math.max(y1+15,Math.min(y2-5,y1+25)),class:'je-edge-label','text-anchor':'middle'});text.textContent=label;svg.append(text);}}
      function endpoint(text,y){const n=el('div',text,'je-endpoint');n.style.left=`${world.cx-86}px`;n.style.top=`${y}px`;stage.append(n);}
      endpoint('● JOURNEY START',22);endpoint('■ JOURNEY END',world.endY);
      world.nodes.forEach((position,index)=>{
        const s=all[index].step,kind=M.kind(s),info=analysis.steps[index],result=measurement(index),left=position.x,cx=left+144;
        const b=button('',()=>select(index),'je-node');b.dataset.kind=kind;b.style.left=`${left}px`;b.style.top=`${position.y}px`;b.style.width=`${world.nodeWidth}px`;b.style.height=`${position.height}px`;b.setAttribute('aria-pressed',selected===index);b.setAttribute('aria-label',`${readonly?'Inspect':'Edit'} ${M.stepName(s,index)}`);b.classList.toggle('je-invalid',!!info.missing.length&&!readonly);b.classList.toggle('je-failed',!!result?.failed);b.classList.toggle('je-skipped',!!result?.skipped&&!result.completed);dragHandlers(b,index);
        const top=el('div',undefined,'je-node-title');top.append(el('span',M.stepName(s,index)),el('small',String(index+1).padStart(2,'0')));
        const request=el('div',undefined,'je-node-request');request.append(el('span',kind==='request'?s.method||'GET':kind==='branch'?'IF':'LOOP','je-method'),el('code',M.describe(s)));request.title=M.describe(s);
        const meta=el('div',undefined,'je-node-meta');meta.append(el('span',kind==='request'?(result?`${result.completed||0} attempts${result.skipped?` · ${result.skipped} skipped journeys`:''}`:`${s.assertions?.length||0} checks`):result?(kind==='branch'?`True ${result.condition_true} · False ${result.condition_false}`:`${result.iterations} body iterations`):kind==='branch'?'True / False paths':'Bounded repetition'));
        if(info.outputs.length)meta.append(el('span',`{ } ${info.outputs.join(', ')}`,'je-data-label'));else if(info.inputs.length)meta.append(el('span',`Uses ${info.inputs.map(v=>v.name).join(', ')}`,'je-data-label'));
        b.append(top,request,meta);
        if(result){const stats=el('div',undefined,'je-node-stats');stats.append(el('span',kind!=='request'?`${result.entered} visits`:result.latency?.samples?`p95 ${Number(result.latency.p95_ms).toFixed(1)} ms`:'No latency samples'),el('span',kind!=='request'?`${result.failed} failed`:result.completed?`${Number(result.error_percent).toFixed(1)}% errors`:`${result.skipped||0} skipped`));b.append(stats);}
        else if(info.missing.length&&!readonly)b.append(el('div',`Missing: ${info.missing.join(', ')}`,'je-node-issue'));
        if(preview?.failed!=null&&!all.some(n=>M.kind(n.step)!=='request'))b.classList.toggle('je-skipped',config.on_error!=='continue'&&index>preview.failed);
        b.addEventListener('focus',()=>{if(b.matches(':focus-visible'))centerNode(index);});stage.append(b);
        if(position.pause){const pause=button(`◷ Think time · ${s.pause}`,()=>{select(index);if(!readonly){tab='request';renderInspector();q('[data-field="pause"]')?.focus();}},'je-pause');pause.style.left=`${cx-100}px`;pause.style.top=`${position.pause.y}px`;stage.append(pause);}
      });
      if(dataFlow)for(const [i,edge] of analysis.edges.entries()){
        if(selected>=0&&edge.from!==selected&&edge.to!==selected)continue;
        const from=world.nodes[edge.from],to=world.nodes[edge.to],x1=from.x,x2=to.x,y1=from.y+63,y2=to.y+63,gutter=Math.min(x1,x2)-20-(i%3)*12;
        const path=svgEl('path',{d:`M ${x1} ${y1} C ${gutter} ${y1}, ${gutter} ${y2}, ${x2} ${y2}`,class:'je-data-edge'});const title=svgEl('title');title.textContent=edge.names.join(', ');path.append(title);svg.append(path);
      }
      stage.prepend(svg);applyTransform();
    }
    function centerNode(index) {const p=world?.nodes[index];if(!p)return;const top=pan.y+p.y*zoom,bottom=top+p.height*zoom;if(top<0||bottom>viewport.clientHeight){pan={x:viewport.clientWidth/2-(p.x+world.nodeWidth/2)*zoom,y:viewport.clientHeight/2-(p.y+p.height/2)*zoom};autoFit=false;applyTransform();}}
    function renderFooter() {
      const footer=q('.je-footer'),issues=M.dependencies(config,si).steps.flatMap((s,i)=>s.missing.map(name=>`${M.stepName(entries()[i].step,i)} needs {{${name}}}`));footer.replaceChildren();
      footer.append(el('span',previewOnly?`${entries().length} mapped flow nodes · preview only`:readonly?`${report?.scenarios?.[si]?.journeys?.completed??0} completed journeys · per-step cards show HTTP measurements`:issues.length?`${issues.length} missing variable reference(s): ${issues.join('; ')}`:`${entries().length} flow nodes · ${totalSteps()} across this test`));footer.classList.toggle('je-bad',!readonly&&!!issues.length);
      if(!readonly)footer.append(button('Undo',undo,'je-link'));footer.append(el('small',previewOnly?'Saving an import does not start a test.':readonly?'Step p95 values cannot be added to get journey p95.':'Editing and previewing send no workload traffic.'));
      if(!readonly)footer.querySelector('button').disabled=!history.length;
    }
    function field(container,label,value,apply,opts={}) {
      const wrap=el('label',label,'je-field'),input=el(opts.area?'textarea':opts.select?'select':'input');
      if(opts.select) for(const [key,text] of opts.select.map(v=>Array.isArray(v)?v:[v,v])){const option=el('option',text);option.value=key;input.append(option);}
      if(opts.area){input.rows=opts.rows||3;input.spellcheck=false;}else if(!opts.select){input.type=opts.number?'number':'text';if(opts.number){input.min=opts.min??'0';input.step=opts.integer?'1':'any';}if(opts.max)input.max=opts.max;}
      input.value=value??'';input.setAttribute('aria-label',label);input.dataset.field=opts.key||label;wrap.append(input);if(opts.help)wrap.append(el('small',opts.help));container.append(wrap);
      watchInput(input,()=>{try{if(opts.number&&input.validity.badInput)throw new Error(`Enter a number for ${label}, or clear the field.`);apply(input.value,input);draft(input.dataset.field);}catch(e){draft(input.dataset.field,e.message);}},()=>draft(input.dataset.field,`Finish editing ${label} before running.`));return input;
    }
    function number(value,label,nullable=false,integer=false,min=0,max=Infinity) {if(value===''&&nullable)return null;const n=Number(value);if(value===''||!Number.isFinite(n)||n<min||n>max||(integer&&!Number.isInteger(n)))throw new Error(`${label} must be ${integer?'a whole number':'a number'} between ${min} and ${max===Infinity?'the engine limit':max}.`);return n;}
    function mapEditor(container,title,values,apply,kind) {
      const group=el('section',undefined,'je-group'),rows=el('div'),key=`map-${title}`;group.append(el('h3',title));container.append(group);group.append(rows);
      function collect(){const map=Object.create(null),names=new Set();for(const row of rows.children){const [keyInput,valueInput]=row.querySelectorAll('input'),name=keyInput.value.trim(),value=valueInput.value;if(!name&&!value)continue;if(!name)throw new Error(`${title}: every value needs a name.`);if(kind==='variable'&&(!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)||['user_id','iteration','journey_id'].includes(name)))throw new Error(`${title}: use a non-reserved variable name.`);const normalized=kind==='header'?name.toLowerCase():name;if(names.has(normalized))throw new Error(`${title}: duplicate name ${name}.`);names.add(normalized);map[name]=value;}return map;}
      function update(){try{apply(collect());draft(key);}catch(e){draft(key,e.message);}}
      function row(name='',value=''){const r=el('div',undefined,'je-map-row');for(const [label,text] of [[`${title} name`,name],[`${title} value`,value]]){const input=el('input');input.value=text;input.placeholder=label.endsWith('name')?'Name':'Value';input.setAttribute('aria-label',label);watchInput(input,update,()=>draft(key,`Finish editing ${title} before running.`));r.append(input);}const remove=button('×',()=>{r.remove();update();},'je-remove');remove.setAttribute('aria-label',`Remove ${title} row`);r.append(remove);rows.append(r);}
      for(const [name,value] of Object.entries(values||{}))row(name,value);
      group.append(button('+ Add '+(kind==='header'?'header':'variable'),()=>{row();rows.lastElementChild.querySelector('input').focus();},'je-link'));
    }
    function renderInspector() {
      const inspector=q('.je-inspector');inspector.replaceChildren();
      const c=current();if(!c)return;
      if(previewOnly){
        const s=step();inspector.append(el('h3',s?M.stepName(s,selected):c.name,'je-inspector-title'));
        if(!s){const stop=config.iterations?`${config.iterations} iterations/user${config.duration?' or '+config.duration:''}`:config.duration;inspector.append(el('p',`Cookies: ${config.cookies===false?'disabled':'enabled'} · ${config.vus} users · ${stop}${config.startup_delay?' · startup '+config.startup_delay:''}${config.ramp_up?' · ramp '+config.ramp_up:''} · On error: ${{next_iteration:'start next iteration',continue:'continue journey',stop_user:'stop this user',stop_test:'stop test gracefully',stop_test_now:'stop test immediately'}[config.on_error||'next_iteration']}`,'je-note'));return;}
        inspector.append(el('p',M.describe(s),'je-note'));
        const details=M.kind(s)==='request'?{method:s.method||'GET',url:s.url,headers:s.headers||{},body:s.body||'',success_statuses:s.success_statuses?.length===200?'200–399':s.success_statuses||[],assertions:s.assertions||[]}:M.kind(s)==='loop'?{count:s.loop.count,while:s.loop.while,max_iterations:s.loop.max_iterations}:s.if.condition;
        inspector.append(el('pre',JSON.stringify(details,null,2),'je-note'));return;
      }
      if(selected<0){journeyInspector(inspector);return;}
      const s=step(),info=M.dependencies(config,si).steps[selected];if(!s)return;
      if(M.kind(s)!=='request'){controlInspector(inspector,s);return;}
      inspector.append(el('h3',M.stepName(s,selected),'je-inspector-title'));
      const tabs=readonly?['results']:['request','checks','variables'];if(!tabs.includes(tab))tab=tabs[0];
      const tabBar=el('div',undefined,'je-tabs');for(const name of tabs){const b=button(name[0].toUpperCase()+name.slice(1),()=>{if(errors.size){message('Correct unfinished fields before changing inspector tabs.',true);return;}tab=name;renderInspector();},'je-tab');b.setAttribute('aria-pressed',name===tab);tabBar.append(b);}inspector.append(tabBar);
      const patch=(value,keep=true)=>commit({type:'edit-step',patch:value},keep);
      if(readonly){resultInspector(inspector,measurement(selected));return;}
      if(tab==='request'){
        field(inspector,'Step name',s.name||'',value=>patch({name:value}),{key:'name',help:'Unique within this journey. A blank name uses its position.'});
        field(inspector,'HTTP method',s.method||'GET',value=>patch({method:value}),{key:'method'});
        field(inspector,'Request path',s.url||'',value=>patch({url:value}),{key:'url',help:'Relative to the test URL. Absolute URLs must use the same origin.'});
        mapEditor(inspector,'Step headers',s.headers,values=>patch({headers:values}),'header');
        field(inspector,'Request body',s.body||'',value=>patch({body:value}),{key:'body',area:true,help:'{{item|json}} encodes a JSON string, including its quotes.'});
        field(inspector,'Successful status codes',(s.success_statuses||[]).join(', '),value=>{const statuses=value.trim()?value.split(',').map(x=>{if(!/^\d{3}$/.test(x.trim())||Number(x)<100||Number(x)>599)throw new Error('Use HTTP status codes such as 200, 201.');return Number(x);}):[];patch({success_statuses:statuses});},{key:'success_statuses',help:'Blank inherits test defaults; otherwise use comma-separated codes.'});
        field(inspector,'Pause after success',s.pause||'',value=>patch({pause:value}),{key:'pause',help:'For example 500ms or 2s. Included in journey duration.'});
        inspector.append(el('p',config.on_error==='continue'?'On failure: attempt later requests and loop iterations. The journey remains failed; failed verification publishes no extracted values.':config.on_error==='stop_user'?'On failure: skip later requests and stop this user’s future iterations. Other users continue.':config.on_error==='stop_test'?'On failure: end this journey and stop new work. Other admitted journeys finish under their deadlines.':config.on_error==='stop_test_now'?'On failure: cancel all active requests, journey pauses, and pending user starts. Partial results are retained.':'On failure: skip later requests, then start the next iteration.','je-note'));
      } else if(tab==='checks') assertionEditor(inspector,s);
      else {
        extractionEditor(inspector,s);
        inspector.append(el('h4','Available before this request'));
        for(const v of info.before.slice(0,40)){const row=el('div',undefined,'je-variable-row');row.append(el('code',`{{${v.name}}}`),el('small',v.source));inspector.append(row);}
        if(info.before.length>40)inspector.append(el('p',`${info.before.length-40} additional variables are available.`,'je-note'));
        inspector.append(el('p','Extracted values become available after a successful response. Values reset for each journey.','je-note'));
      }
      if(info.missing.length)inspector.append(el('p',`Missing variables: ${info.missing.map(v=>'{{'+v+'}}').join(', ')}. Move their producer earlier or define them in journey data.`,'je-note je-bad'));
      movementInspector(inspector);
    }
    function movementInspector(container) {
      const all=entries(),n=all[selected],siblings=all.filter(v=>v.container===n.container),position=siblings.indexOf(n);
      const reorder=el('div',undefined,'je-inspector-actions'),up=button('Move up',()=>commit({type:'move-step',to:siblings[position-1].index})),down=button('Move down',()=>commit({type:'move-step',to:siblings[position+1].index}));up.disabled=position===0;down.disabled=position===siblings.length-1;
      reorder.append(up,down,button('Duplicate',()=>commit({type:'duplicate-step'})),button('Remove',()=>commit({type:'remove-step'}),'je-button je-danger'));container.append(reorder);
      const choices=M.containers(current()).filter(c=>!c.path.startsWith(n.path+'.')).map(c=>[c.path,c.label]);
      field(container,'Move to path',n.container,value=>{draft('Move to path');commit({type:'move-container',container:value});},{select:choices,help:'Moves this node and all its children to the end of the selected path.'});
    }
    function conditionEditor(container,condition,apply) {
      const latest=()=>step().if?.condition||step().loop.while;
      field(container,'Condition variable',condition.variable,value=>apply({...latest(),variable:value}),{key:'condition-variable',help:'A variable from journey data, a prior extraction, or an enclosing loop.'});
      field(container,'Condition operator',condition.operator||'equals',value=>{const c={...latest(),operator:value};if(['exists','not_exists'].includes(value))delete c.value;else c.value??='';apply(c,false);},{key:'condition-operator',select:[['equals','Equals'],['not_equals','Does not equal'],['contains','Contains'],['not_contains','Does not contain'],['gt','Greater than (number)'],['gte','At least (number)'],['lt','Less than (number)'],['lte','At most (number)'],['exists','Exists'],['not_exists','Does not exist']]});
      if(!['exists','not_exists'].includes(condition.operator))field(container,'Comparison value',condition.value??'',value=>apply({...latest(),value}),{key:'condition-value',help:'Text or {{variable}}. Empty text is allowed; numeric comparisons require finite numbers.'});
    }
    function controlInspector(container,s) {
      const kind=M.kind(s);container.append(el('h3',M.stepName(s,selected),'je-inspector-title'));
      if(readonly){const r=measurement(selected);container.append(el('p',kind==='branch'?'Observed branch decisions':'Observed loop executions','je-note'));if(!r){container.append(el('p','No control measurements.'));return;}for(const [name,value] of [['Visits',r.entered],['Completed',r.completed],['Failed',r.failed],['Condition true',r.condition_true],['Condition false',r.condition_false],['Body iterations',r.iterations],['Condition errors',r.condition_errors]])container.append(el('p',`${name}: ${value}`,'je-note'));container.append(el('p','Control visits and decisions are counts, with no HTTP latency samples.','je-note'));return;}
      const patch=(value,keep=true)=>commit({type:'edit-step',patch:value},keep);
      field(container,'Step name',s.name||'',value=>patch({name:value}),{key:'name'});
      if(kind==='branch'){
        conditionEditor(container,s.if.condition,(condition,keep=true)=>patch({if:{...step().if,condition}},keep));
        container.append(el('p','Evaluates once on each visit. Only the chosen path runs; both paths join at the next node.','je-note'));
      }else{
        const loop=s.loop;
        field(container,'Loop mode',loop.count!=null?'count':'while',value=>{const next={...step().loop};delete next.count;delete next.while;delete next.max_iterations;if(value==='count')next.count=2;else{next.while={variable:'journey_id',operator:'equals',value:'1'};next.max_iterations=10;}patch({loop:next},false);},{select:[['count','Fixed count'],['while','While condition']]});
        if(loop.count!=null)field(container,'Repeat count',loop.count,value=>patch({loop:{...step().loop,count:number(value,'Repeat count',false,true,1,1000)}}),{number:true,integer:true,min:1,max:1000});
        else{
          conditionEditor(container,loop.while,(condition,keep=true)=>patch({loop:{...step().loop,while:condition}},keep));
          field(container,'Maximum iterations',loop.max_iterations,value=>patch({loop:{...step().loop,max_iterations:number(value,'Maximum iterations',false,true,1,1000)}}),{number:true,integer:true,min:1,max:1000});
        }
        field(container,'Iteration variable',loop.index_variable||'',value=>patch({loop:{...step().loop,index_variable:value}}),{help:'Optional 1 based counter, scoped to this body. Its previous value is restored on exit.'});
        container.append(el('p',loop.count!=null?'Runs the body a fixed number of times. '+(config.on_error==='continue'?'Request failures are recorded while repetition continues. Cancellation, journey deadlines and safety limits still stop the journey.':'A child failure, cancellation, or journey deadline stops repetition.'):'The condition is checked before each body iteration. A while condition still true at the limit fails the journey with loop_limit. Update its variable through a response extraction.','je-note'));
      }
      container.append(el('h4','Add to a child path'));
      const n=entries()[selected];for(const [suffix,label] of kind==='branch'?[['.if.then','True'],['.if.else','False']]:[['.loop.steps','Body']]){
        const row=el('div',undefined,'je-child-actions');row.append(el('strong',label));for(const [type,word] of [['add-step','request'],['add-branch','branch'],['add-loop','loop']])row.append(button(`+ ${label} ${word}`,()=>commit({type,container:n.path+suffix}),'je-link'));container.append(row);
      }
      const info=M.dependencies(config,si).steps[selected];if(info.missing.length)container.append(el('p',`Missing variables: ${info.missing.join(', ')}`,'je-note je-bad'));
      movementInspector(container);
    }
    function extractionEditor(container,s) {
      container.append(el('h4','Extract from this response'));
      const rows=el('div');container.append(rows);
      function update(){try{const result=Object.create(null);for(const row of rows.children){const name=row.querySelector('[data-name]').value.trim(),kind=row.querySelector('select').value,selector=row.querySelector('[data-selector]').value;if(!name&&!selector)continue;if(!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)||['user_id','iteration','journey_id'].includes(name))throw new Error('Use a non-reserved extraction name, for example token.');if(Object.hasOwn(result,name))throw new Error('Extraction names must be unique.');result[name]={...row.original,[kind]:selector};delete result[name][kind==='header'?'json_pointer':'header'];}commit({type:'edit-step',patch:{extract:result}},true);draft('extractions');}catch(e){draft('extractions',e.message);}}
      function add(name='',value={json_pointer:'/token'}){const row=el('div',undefined,'je-rule');row.original=M.copy(value);const nameInput=field(row,'Variable name',name,update,{key:'extraction-name'});nameInput.dataset.name='';field(row,'Extract from',value.header?'header':'json_pointer',()=>update(),{select:[['json_pointer','JSON Pointer'],['header','Response header']],key:'extraction-kind'});const selector=field(row,'Selector',value.header||value.json_pointer||'',update,{key:'extraction-selector',help:'An empty JSON Pointer selects the document root.'});selector.dataset.selector='';row.append(button('Remove extraction',()=>{row.remove();update();},'je-link je-danger'));rows.append(row);}
      for(const [name,value] of Object.entries(s.extract||{}))add(name,value);
      container.append(button('+ Add extraction',()=>{if(rows.children.length>=100){message('A request supports at most 100 extractions.',true);return;}add();rows.lastElementChild.querySelector('input').focus();draft('extractions','Name the new extraction or remove it before running.');},'je-link'));
    }
    function assertionEditor(container,s) {
      container.append(el('h4','Response assertions'));const rows=el('div');container.append(rows);
      function update(){try{const assertions=[];for(const row of rows.children){const kind=row.querySelector('select').value,selector=row.querySelector('[data-selector]').value,expected=row.querySelector('[data-expected]').value,a={...row.original};for(const k of ['json_pointer','header','body_contains','equals'])delete a[k];a[kind]=selector;if(kind!=='body_contains')a.equals=expected;assertions.push(a);row.querySelector('[data-expected]').closest('label').hidden=kind==='body_contains';}commit({type:'edit-step',patch:{assertions}},true);draft('assertions');}catch(e){draft('assertions',e.message);}}
      function add(a){const row=el('div',undefined,'je-rule');row.original=M.copy(a);const kind=a.body_contains!=null?'body_contains':a.header?'header':'json_pointer';field(row,'Check type',kind,update,{key:'assertion-kind',select:[['json_pointer','JSON value equals'],['header','Response header equals'],['body_contains','Body contains']]});const selector=field(row,kind==='body_contains'?'Expected text':'Selector',a.body_contains??(a.header||a.json_pointer||''),update,{key:'assertion-selector'});selector.dataset.selector='';const expected=field(row,'Expected value',a.equals||'',update,{key:'assertion-expected'});expected.dataset.expected='';expected.closest('label').hidden=kind==='body_contains';row.append(button('Remove assertion',()=>{row.remove();update();},'je-link je-danger'));rows.append(row);}
      (s.assertions||[]).forEach(add);
      container.append(button('+ Add assertion',()=>{if((step().assertions?.length||0)>=100){message('A request supports at most 100 assertions.',true);return;}commit({type:'edit-step',patch:{assertions:[...(step().assertions||[]),{json_pointer:'/ok',equals:'true'}]}});},'je-link'),el('p','Extractions are available to assertions on the same response. A failed assertion follows the test failure action. Failed verification publishes no extracted values.','je-note'));
    }
    function journeyInspector(container) {
      const c=current();container.append(el('h3',readonly?c.name||'Journey summary':'Journey settings','je-inspector-title'));
      if(readonly){const j=report?.scenarios?.[si]?.journeys;if(j)resultInspector(container,j,true);return;}
      const patch=value=>commit({type:'edit-journey',patch:value},true);
      field(container,'Journey name',c.name||'',value=>{if(!value.trim())throw new Error('Enter a journey name.');patch({name:value});},{key:'journey-name'});
      field(container,'Mix weight',c.weight??1,value=>patch({weight:number(value,'Weight',false,true,1,1000)}),{key:'weight',number:true,integer:true,min:1,max:1000,help:'Weights allocate scheduled arrivals, not completed journeys.'});
      field(container,'Journey timeout',config.journey_timeout||'30s',value=>commit({type:'edit-test',patch:{journey_timeout:value}},true),{key:'journey_timeout',help:'Applies to every journey, including requests, verification, and pauses.'});
      mapEditor(container,'Journey variables',c.variables,values=>patch({variables:values}),'variable');
      mapEditor(container,'Test variables',config.variables,values=>commit({type:'edit-test',patch:{variables:values}},true),'variable');
      container.append(el('h4','Dataset'));
      if(c.csv)container.append(el('p',`${c.csv.name||'CSV dataset'} · ${c.csv.sharing==='per_user'?'each user has a sequence':'shared across users'} · ${c.csv.on_eof==='stop_user'?'stop user normally at EOF':'recycle rows'}`,'je-note'));
      const csvControls=el('div',undefined,'je-inspector-actions'),csvButton=button(c.csv?'Edit CSV dataset':'Import CSV dataset',()=>options.csv?.(M.copy(current()),value=>commit({type:'edit-journey',patch:{csv:value,data:[]}})));
      csvButton.disabled=config.mode==='rate'||!!config.workers?.length;csvControls.append(csvButton);
      if(c.csv)csvControls.append(button('Remove CSV dataset',()=>commit({type:'edit-journey',patch:{csv:null}})));
      container.append(csvControls);
      if(csvButton.disabled)container.append(el('p','CSV datasets require local Concurrent users mode. Change Test settings to import a file.','je-note'));
      if(!c.csv) {
      field(container,'Dataset rows · JSON',JSON.stringify(c.data||[],null,2),value=>{const rows=JSON.parse(value);if(!Array.isArray(rows)||rows.some(row=>!row||Array.isArray(row)||typeof row!=='object'||Object.values(row).some(v=>typeof v!=='string')))throw new Error('Data must be an array of objects containing string values.');patch({data:rows});},{key:'data',area:true,rows:5,help:'Example: [{"username":"alice","item":"coffee"}]. Every row needs identical keys. Rows cycle per scheduled journey.'});
      }
      container.append(el('h4','Journey pass criteria'));
      for(const [key,label,numberField] of [['p95','Journey p95 latency',false],['max_error_percent','Maximum journey errors (%)',true],['max_drop_percent','Maximum missed journeys (%)',true],['min_started_per_second','Minimum started journeys / s',true]])field(container,label,c.thresholds?.[key]??'',value=>{const thresholds={...current().thresholds,[key]:numberField?number(value,label,true,false,0,key.startsWith('max_')?100:Infinity):value};patch({thresholds});},{key:`threshold-${key}`,number:numberField,help:'Blank disables this criterion.'});
      container.append(el('p','Dataset values override journey variables; extractions override them after each successful response. Engine-reserved values cannot be replaced.','je-note'));
      const controls=el('div',undefined,'je-inspector-actions');controls.append(button('Duplicate journey',()=>commit({type:'duplicate-journey'})),button('Remove journey',()=>commit({type:'remove-journey'}),'je-button je-danger'));container.append(controls);
    }
    function resultInspector(container,r,journey=false) {
      if(!r){container.append(el('p','No final measurements are available for this step.','je-note'));return;}
      container.append(el('p',journey?'Whole-journey measurements':'HTTP step measurements','je-note'));
      const table=el('table',undefined,'je-result-table'),body=el('tbody');
      for(const [name,value] of [['Completed',r.completed??0],['Failed',r.failed??0],...(!journey?[['Skipped journeys',r.skipped??0],...(r.path?[['Reached journeys',r.executed_journeys??0],['Skipped by condition',r.skipped_condition??0]]:[])]:[]),['Error rate',r.completed?`${Number(r.error_percent).toFixed(2)}%`:'No completed samples'],['p95',r.latency?.samples?`${Number(r.latency.p95_ms).toFixed(2)} ms`:'No latency samples'],['Latency samples',r.latency?.samples??0]]){const row=el('tr');row.append(el('th',name),el('td',String(value)));body.append(row);}table.append(body);container.append(table);
      if(r.errors&&Object.keys(r.errors).length){container.append(el('h4','Observed failures'));for(const [name,value] of Object.entries(r.errors))container.append(el('p',`${name}: ${value}`,'je-note'));}
      container.append(el('p',journey?'Journey latency includes requests, assertions, and pauses.':'Attempts can exceed journeys inside loops. Skipped journeys never attempted this node; conditional skips identify an unchosen path or a zero-iteration body. No response bodies or extracted values are shown.','je-note'));
    }
    function undo() {
      if(!history.length)return;const previous=history.pop();errors.clear();options.onDraftError?.('');config=previous.config;si=previous.si;selected=previous.selected;preview=null;internal=true;try{options.write(config);}finally{internal=false;}render();message('Last canvas edit undone.');
    }
    viewport.addEventListener('pointerdown',event=>{
      if(event.target.closest('button')||event.button!==0)return;
      const initial={x:event.clientX,y:event.clientY,pan:{...pan}};autoFit=false;viewport.setPointerCapture(event.pointerId);viewport.classList.add('je-panning');
      function move(e){pan={x:initial.pan.x+e.clientX-initial.x,y:initial.pan.y+e.clientY-initial.y};applyTransform();}
      function end(){viewport.removeEventListener('pointermove',move);viewport.removeEventListener('pointerup',end);viewport.removeEventListener('pointercancel',end);viewport.classList.remove('je-panning');}
      viewport.addEventListener('pointermove',move);viewport.addEventListener('pointerup',end);viewport.addEventListener('pointercancel',end);
    });
    viewport.addEventListener('wheel',event=>{if(!event.ctrlKey&&!event.metaKey)return;event.preventDefault();const b=viewport.getBoundingClientRect();zoomAt(zoom*(event.deltaY<0?1.1:1/1.1),{x:event.clientX-b.left,y:event.clientY-b.top});},{passive:false});
    const observer=new ResizeObserver(()=>{if(!viewport.clientWidth||viewport.clientWidth===lastWidth)return;lastWidth=viewport.clientWidth;cancelAnimationFrame(pendingFrame);pendingFrame=requestAnimationFrame(()=>{renderGraph();if(autoFit)fit();});});observer.observe(viewport);
    const onFullscreen=()=>{if(root.isConnected){autoFit=true;requestAnimationFrame(fit);}};
    document.addEventListener('fullscreenchange',onFullscreen);
    return {
      validate,
      undo,
      canUndo:()=>!readonly&&history.length>0,
      destroy() {observer.disconnect();cancelAnimationFrame(pendingFrame);document.removeEventListener('fullscreenchange',onFullscreen);},
      load(c,replaceDraft=false) {
        if(internal)return;
        try{M.shape(c);const replaced=JSON.stringify(config.scenarios)!==JSON.stringify(c.scenarios);if(JSON.stringify(config)!==JSON.stringify(c))history=[];config=M.copy(c);if(replaceDraft){errors.clear();options.onDraftError?.('');history=[];si=0;selected=0;tab=readonly?'results':'request';preview=null;autoFit=true;message('');}render(!replaceDraft&&!!errors.size);if(replaced&&autoFit)requestAnimationFrame(fit);}
        catch(e){draft('shape',e.message);q('.je-workspace').hidden=true;q('.je-empty').hidden=true;q('.je-footer').hidden=true;}
      },
      invalid(text) {draft('json',text);q('.je-workspace').hidden=true;q('.je-empty').hidden=true;q('.je-footer').hidden=true;},
      show() {requestAnimationFrame(()=>{renderGraph();if(autoFit)fit();});},
      loadRun(run) {
        const key=run.id+JSON.stringify(run.result?.scenarios);if(key===reportID)return;reportID=key;report=run.result;config=M.copy(run.config);si=0;selected=0;autoFit=true;preview=null;render();
      }
    };
  }
  return {create};
})();
