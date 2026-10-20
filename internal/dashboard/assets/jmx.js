'use strict';

// Import state is isolated from the test builder. Only a server-validated,
// explicitly saved scenario can cross into the executable editor.
globalThis.GoMeterJMX = (() => {
  const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const labels = {mapped:'Mapped',input:'Needs input',blocked:'Blocked',review:'Review',ignored:'Disabled',notice:'Notice'};
  const marks = {mapped:'✓',input:'!',blocked:'×',review:'!',ignored:'−',notice:'i'};
  const badge = status => `<span class="jmx-badge ${status}">${labels[status] || esc(status)}</span>`;
  function create(root, options) {
    let source='', filename='', name='', properties=Object.create(null), accept=false, report=null, selected='', step=0, draftID='', done=null, busy=false, error='', dirty=false, revision=0;
    let previewEditor=null;
    const btn = (label, action, primary=false, disabled=false) => `<button type="button" class="button ${primary?'primary':'secondary'}" data-jmx-action="${action}"${disabled?' disabled':''}>${label}</button>`;
    const request = () => ({source,filename,name,properties,accept_runtime:accept});
    function status() {
      if(report.blockers) return `<div class="jmx-banner blocked"><strong>${report.blockers} execution blocker${report.blockers===1?'':'s'}${report.missing_inputs?` · ${report.missing_inputs} missing propert${report.missing_inputs===1?'y':'ies'}`:''}</strong><p>You can inspect the plan and save a migration draft. A blocked plan cannot become a runnable scenario.</p></div>`;
      if(report.missing_inputs) return `<div class="jmx-banner input"><strong>Supply ${report.missing_inputs} required propert${report.missing_inputs===1?'y':'ies'}</strong><p>The importer does not read your JMeter properties, environment, or referenced files.</p></div>`;
      if(!accept) return '<div class="jmx-banner review"><strong>Review GoMeter runtime policies</strong><p>The supported structure is mapped. Review the execution limits and runtime differences before saving a scenario.</p></div>';
      return '<div class="jmx-banner mapped"><strong>Ready to save as a GoMeter scenario</strong><p>All required inputs are supplied, no execution blockers remain, and runtime policies are accepted.</p></div>';
    }
    function fileline() { return `<div class="jmx-fileline"><span class="jmx-fileicon">JMX</span><div><h2>${esc(filename)}</h2><p>JMeter ${esc(report.source_version||'version not recorded')} · ${esc(report.name)}</p></div><button class="text-button" type="button" data-jmx-action="choose">Change file</button></div>`; }
    function runtime() { return `<label class="jmx-runtime"><input type="checkbox" data-jmx-runtime${accept?' checked':''}${busy?' disabled':''}><span>I accept the documented GoMeter runtime policies for this conversion.</span></label>${(report.components.find(c=>c.id==='runtime')?.details||[]).map(text=>`<p class="helper">${esc(text)}</p>`).join('')}`; }
    function detail() {
      const component=report.components.find(c=>c.id===selected)||report.components[0];
      if(!component) return '';
      return `<section class="panel jmx-detail" aria-label="Selected JMX component"><div class="jmx-detail-head"><div><span class="eyebrow">${esc(component.kind)}</span><h2>${esc(component.name)}</h2></div>${badge(component.status)}</div><code class="jmx-source-path">${esc(component.path)}</code>${component.id==='runtime'?runtime():component.details.map(text=>`<p>${esc(text)}</p>`).join('')}${component.status==='input'?'<button type="button" class="text-button" data-jmx-action="properties">Supply property values ↓</button>':''}${step===1&&component.kind!=='Compatibility'?'<button type="button" class="text-button" data-jmx-action="preview">View imported structure →</button>':''}</section>`;
    }
    function inputs() {
      if(!report.inputs.length) return '';
      return `<section class="panel jmx-inputs" aria-label="JMeter properties"><div class="panel-heading"><h2>JMeter properties</h2><span class="count">${report.inputs.length}</span></div><p class="helper">Values are literal strings. Defaults from the JMX plan are shown; explicitly supplied values override them.</p>${report.inputs.map((input,i)=>`<label for="jmx-property-${i}">${esc(input.name)}${!input.supplied?' · required':''}<input id="jmx-property-${i}" type="text" data-jmx-property="${esc(input.name)}" value="${esc(Object.hasOwn(properties,input.name)?properties[input.name]:input.value)}" autocomplete="off" spellcheck="false"><small>${input.supplied?'Explicit value or plan default':'Not supplied by the plan'}</small></label>`).join('')}${btn('Apply values','analyze',false,busy)}</section>`;
    }
    function components() {
      return `<section class="panel jmx-components" aria-label="JMX component review"><div class="panel-heading"><h2>Component review</h2><span class="count">${report.components.length}</span></div>${report.components.map(c=>`<button type="button" class="jmx-component" data-jmx-component="${esc(c.id)}" aria-pressed="${c.id===selected}"><span class="jmx-mark ${c.status}" aria-hidden="true">${marks[c.status]||'i'}</span><span class="jmx-component-text"><strong>${esc(c.name)}</strong><small>${esc(c.kind)}</small></span>${badge(c.status)}</button>`).join('')}</section>`;
    }
    function sourceTree(nodes) {
      return `<ul class="jmx-source-tree">${nodes.map(n=>{const c=report.components.find(c=>c.id===n.id);return `<li><button type="button" data-jmx-component="${esc(n.id)}" aria-pressed="${n.id===selected}"><span class="jmx-mark ${c?.status||'notice'}" aria-hidden="true">${marks[c?.status]||'i'}</span><span><strong>${esc(n.name)}</strong><small>${esc(n.kind)}</small></span>${badge(c?.status||'notice')}</button>${n.children?.length?sourceTree(n.children):''}</li>`;}).join('')}</ul>`;
    }
    function render() {
      previewEditor?.destroy();
      previewEditor=null;
      let content='',footer='';
      if(done) {
        content=`<section class="panel jmx-complete"><span class="jmx-complete-mark" aria-hidden="true">✓</span><h2>${done.kind==='scenario'?'Scenario saved to your library':'Migration draft saved'}</h2><p>${done.kind==='scenario'?'The original JMX and your conversion decisions are retained with the saved scenario. Open the builder to modify or run it.':'The original JMX, supplied properties, and runtime review are preserved separately from runnable scenarios. Reopen this draft from Migration drafts.'}</p><p class="helper">No workload traffic was sent.</p><div class="heading-actions">${done.kind==='scenario'?btn('Open in journey builder','load',true):btn('Return to compatibility review','review',true)}${btn('Import another plan','new')}</div></section>`;
      } else if(step===0) {
        content=`<section class="panel jmx-upload" aria-label="Choose a JMX plan"><span class="eyebrow">BRING YOUR TEST WITH YOU</span><h2>Import a JMeter test plan</h2><p>Choose a .jmx file or drop it here. Review every mapping before you save.</p>${btn('Choose JMX file','file',true,busy)}<input type="file" data-jmx-file accept=".jmx,application/xml,text/xml" hidden><p class="helper">Up to 1 MiB · XML only · No traffic, scripts, or referenced files are executed.</p></section><section class="panel jmx-sample"><div><h2>Try the HTTP sample</h2><p>A duration-limited user group, scoped headers, a fixed loop, and a literal assertion. Targets the local demo on port 8787.</p></div>${btn('Review sample plan','sample',false,busy)}</section>`;
      } else if(report) {
        const counts=report.components.reduce((out,c)=>(out[c.status]=(out[c.status]||0)+1,out),{});
        content=fileline()+status();
        if(step===1) content+=`<div class="jmx-summary" aria-label="Compatibility summary">${['mapped','input','blocked','review','ignored','notice'].filter(s=>counts[s]).map(s=>`<span><span class="jmx-mark ${s}">${marks[s]}</span>${counts[s]} ${labels[s].toLowerCase()}</span>`).join('')}</div><div class="jmx-review-grid">${components()}<div class="jmx-detail-column">${detail()}${inputs()}</div></div>`;
        if(step===2) {
          content+=report.ready?'<section id="jmx-native-preview" aria-label="Imported journey diagram"></section>':`<div class="jmx-review-grid"><section class="panel"><div class="panel-heading"><h2>Imported structure</h2><span class="pill">NOT RUNNABLE</span></div><p class="helper">This is the original component hierarchy, including blockers. It is not an execution simulation or a partial runnable configuration.</p>${sourceTree(report.preview)}</section>${detail()}</div>`;
        }
        if(step===3) content+=`<section class="panel jmx-save"><h2>${report.ready?'Save your imported scenario':'Preserve this migration draft'}</h2><label for="jmx-save-name">${report.ready?'Scenario name':'Migration name'}<input id="jmx-save-name" data-jmx-name maxlength="256" value="${esc(name||report.name)}"></label>${runtime()}<p class="helper">Saving retains the original JMX. Source files and property values may contain credentials; workspace records are stored locally with owner-only file permissions.</p><div class="heading-actions">${btn('Save scenario','save-scenario',true,busy||!report.ready||dirty)}${btn(draftID?'Update migration draft':'Save migration draft','save-draft',false,busy)}</div><p class="helper">Saving never starts a test. Drafts are kept outside the Scenario library.</p></section>`;
        if(step===1)footer=btn('Back','choose')+btn('Preview journey','preview',true,busy);
        if(step===2)footer=btn('Back to compatibility','review')+btn(report.ready?'Continue to save':'Continue as migration draft','finish',true,busy);
        if(step===3)footer=btn('Back to preview','preview');
      }
      root.innerHTML=`<div class="page-heading"><div><div class="eyebrow">FROM JMETER TO GOMETER</div><h1 tabindex="-1">Import your JMeter test</h1><p>Bring your existing plan into GoMeter, with every conversion decision visible.</p></div><div class="heading-actions">${btn('Import guide','help')}${btn('Back to builder','close')}</div></div><ol class="jmx-steps" aria-label="JMX import progress">${['Choose file','Review compatibility','Preview journey','Save'].map((label,i)=>`<li><button type="button" data-jmx-step="${i}"${step===i?' aria-current="step"':''}${busy||(!report&&i>0)?' disabled':''}><span>${i<step?'✓':i+1}</span>${label}</button></li>`).join('')}</ol>${error?`<div class="validation bad" role="alert">${esc(error)}</div>`:''}${busy?'<p class="jmx-busy" role="status">Analyzing JMX locally…</p>':''}${content}<div class="jmx-footer"><span>No workload traffic is sent during import, preview, or saving.</span><div class="heading-actions">${footer}</div></div><div class="sr-only" role="status" aria-live="polite">${busy?'Analyzing JMX':error||done?'Import state updated.':report?`${report.blockers} blockers, ${report.missing_inputs} missing properties.`:'Choose a JMX file.'}</div>`;
      if(step===2&&report?.ready&&!done) {previewEditor=GoMeterJourneyEditor.create(root.querySelector('#jmx-native-preview'),{preview:true});previewEditor.load(report.config,true);previewEditor.show();}
    }
    async function analyze() {
      if(!source)return;
      const current=++revision;busy=true;error='';render();
      try {
        const next=await (await options.api('/api/jmx/analyze','POST',request())).json();
        if(current!==revision)return;
        report=next;dirty=false;
        if(!report.components.some(c=>c.id===selected))selected=(report.components.find(c=>c.status==='blocked')||report.components.find(c=>c.status==='input')||report.components.find(c=>c.status==='review')||report.components[0])?.id||'';
      } catch(e){if(current===revision){error=e.message;report=null;}}
      finally {if(current===revision){busy=false;render();}}
    }
    function focusStep() { root.querySelector('h1').focus({preventScroll:true}); window.scrollTo(0,0); }
    async function go(next) {if(busy)return;if(dirty)await analyze();if(!report&&next>0)return;step=next;done=null;error='';render();focusStep();}
    async function open(data) {
      options.show();
      if(data) {revision++;source=data.source;filename=data.filename||'imported.jmx';properties=Object.assign(Object.create(null),data.properties||{});accept=!!data.accept_runtime;name=data.name||'';draftID=data.id||'';done=null;selected='';step=1;report=null;dirty=false;await analyze();}
      else render();
    }
    async function readFile(file) {
      if(!file)return;
      try{if(!file.name.toLowerCase().endsWith('.jmx'))throw new Error('Choose a .jmx test plan.');if(file.size>1048576)throw new Error('JMX file exceeds 1 MiB.');await open({source:await file.text(),filename:file.name});}
      catch(e){error=e.message;render();}
    }
    root.addEventListener('input',event=>{
      if(event.target.hasAttribute('data-jmx-property')) {properties[event.target.dataset.jmxProperty]=event.target.value;dirty=true;root.querySelector('[data-jmx-action="save-scenario"]')?.setAttribute('disabled','');}
      if(event.target.hasAttribute('data-jmx-name'))name=event.target.value;
    });
    root.addEventListener('change',event=>{
      if(event.target.hasAttribute('data-jmx-file'))readFile(event.target.files[0]);
      if(event.target.hasAttribute('data-jmx-runtime')){accept=event.target.checked;analyze();}
    });
    root.addEventListener('dragover',event=>{if(event.target.closest('.jmx-upload'))event.preventDefault();});
    root.addEventListener('drop',event=>{if(event.target.closest('.jmx-upload')){event.preventDefault();readFile(event.dataTransfer.files[0]);}});
    root.addEventListener('click',async event=>{
      const target=event.target.closest('button');if(!target||target.disabled)return;
      if(target.hasAttribute('data-jmx-component')){selected=target.dataset.jmxComponent;render();root.querySelector(`[data-jmx-component="${selected}"]`)?.focus({preventScroll:true});return;}
      if(target.hasAttribute('data-jmx-step')){await go(Number(target.dataset.jmxStep));return;}
      try {
        switch(target.dataset.jmxAction){
          case 'close':options.close();break;
          case 'help':options.help();break;
          case 'file':root.querySelector('[data-jmx-file]').click();break;
          case 'sample':await open(await (await options.api('/api/jmx/example')).json());break;
          case 'new':revision++;source='';filename='';name='';draftID='';properties=Object.create(null);accept=false;report=null;done=null;step=0;error='';dirty=false;render();focusStep();break;
          case 'choose':await go(0);break;
          case 'review':await go(1);break;
          case 'preview':await go(2);break;
          case 'finish':await go(3);break;
          case 'analyze':await analyze();break;
          case 'properties':root.querySelector('.jmx-inputs input')?.focus();break;
          case 'load':options.load(done.saved.config,done.saved.id);break;
          case 'save-scenario':case 'save-draft':{
            if(busy)return;
            const scenario=target.dataset.jmxAction==='save-scenario';if(scenario&&(!report?.ready||dirty))return;
            busy=true;error='';render();
            try {
              const path=scenario?'/api/jmx/convert':draftID?`/api/jmx/migrations/${draftID}`:'/api/jmx/migrations';
              const saved=await (await options.api(path,scenario||!draftID?'POST':'PUT',request())).json();
              if(!scenario)draftID=saved.id;
              done={kind:scenario?'scenario':'draft',saved};await options.refresh();
            }finally{busy=false;render();focusStep();}
            break;
          }
        }
      }catch(e){busy=false;error=e.message;render();}
    });
    render();
    return {open,show:()=>previewEditor?.show()};
  }
  return {create};
})();
