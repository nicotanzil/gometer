'use strict';

globalThis.GoMeterCSV = (() => {
  const el=(tag,text,cls)=>{const e=document.createElement(tag);if(text!==undefined)e.textContent=text;if(cls)e.className=cls;return e;};
  function create({api}) {
    const dialog=el('dialog',undefined,'csv-dialog');
    dialog.setAttribute('aria-labelledby','csv-title');
    dialog.innerHTML='<form method="dialog" class="csv-top"><div><div class="eyebrow">JOURNEY DATA</div><h2 id="csv-title">Import a CSV dataset</h2></div><button aria-label="Close CSV import" value="cancel" class="text-button">✕</button></form><p class="csv-intro">Assign one row to each journey iteration. Requests and loops within that journey reuse the row.</p><div class="csv-source"><label>CSV file<input id="csv-file" type="file" accept=".csv,text/csv,text/plain"></label><label>Delimiter<select id="csv-delimiter" aria-label="Delimiter"><option value=",">Comma (,)</option><option value=";">Semicolon (;)</option><option value="\t">Tab</option><option value="|">Pipe (|)</option></select></label></div><p class="helper">UTF-8 with a header row. Up to 10,000 rows, 100 columns and 100,000 cells; the complete configuration must fit within 1 MiB.</p><p id="csv-error" class="bad" role="alert" hidden></p><div id="csv-review" hidden><div class="csv-summary" role="status"></div><h3>1. Map columns to variables</h3><p class="helper">Use these names in requests, for example {{username}}. Leave a mapping blank to ignore that column.</p><div class="csv-mappings"></div><h3>Preview · first 5 rows</h3><div class="csv-preview" tabindex="0" role="region" aria-label="CSV sample rows"></div><h3>2. Choose row behavior</h3><div class="csv-behavior"><label>Row sharing<select id="csv-sharing" aria-label="Row sharing"><option value="shared">Shared across all users</option><option value="per_user">Each user starts at the first row</option></select></label><label>When rows run out<select id="csv-eof" aria-label="When rows run out"><option value="recycle">Recycle from the first row</option><option value="stop_user">Stop this user normally</option></select></label></div><p class="helper csv-behavior-help"></p></div><p class="csv-storage helper">CSV source and values are included in saved configurations and run bundles. Review them before sharing. Importing sends no workload traffic.</p><div class="csv-footer"><button type="button" id="csv-cancel" class="button secondary">Cancel</button><button type="button" id="csv-apply" class="button primary" disabled>Use dataset</button></div>';
    document.body.append(dialog);
    const q=s=>dialog.querySelector(s), file=q('#csv-file'), delimiter=q('#csv-delimiter'), apply=q('#csv-apply'), error=q('#csv-error');
    let dataset=null, callback=null, generation=0, busy=false;
    const fail=e=>{error.textContent=e.message;error.hidden=false;};
    function behavior() {
      q('.csv-behavior-help').textContent=(q('#csv-sharing').value==='shared'?'Users take the next shared row; which user receives a row depends on scheduling.':'Each user has a separate sequence for this journey.')+' '+(q('#csv-eof').value==='recycle'?'Rows repeat after the last row.':'A user stops before starting another journey when no row is available. This is normal completion and can end the test before its iteration or duration limit.');
    }
    async function preview(reset=false) {
      const version=++generation;apply.disabled=true;error.hidden=true;q('#csv-review').hidden=true;
      if(!dataset)return;
      busy=true;
      try {
        const p=await(await api('/api/csv/preview','POST',dataset)).json();
        if(version!==generation||!dialog.open)return;
        dataset.columns=reset?p.columns:Object.fromEntries(p.headers.map(h=>[h,Object.hasOwn(dataset.columns||{},h)?dataset.columns[h]:h]));
        q('.csv-summary').textContent=`${dataset.name || 'CSV dataset'} · ${p.row_count.toLocaleString()} rows · ${p.headers.length} columns`;
        q('.csv-mappings').replaceChildren(...p.headers.map(header=>{
          const label=el('label',undefined,'csv-map'),name=el('span',header),input=el('input');input.value=dataset.columns[header];input.autocomplete='off';input.spellcheck=false;input.maxLength=128;
          input.addEventListener('input',()=>{dataset.columns[header]=input.value;error.hidden=true;});
          label.append(name,input);return label;
        }));
        const table=el('table'),head=el('thead'),tr=el('tr'),body=el('tbody');
        for(const h of p.headers){const th=el('th',h);th.scope='col';tr.append(th);}head.append(tr);
        for(const values of p.sample){const row=el('tr');for(const v of values)row.append(el('td',v));body.append(row);}table.append(head,body);q('.csv-preview').replaceChildren(table);
        q('#csv-review').hidden=false;apply.disabled=false;
      } catch(e) { if(version===generation)fail(e); }
      finally { if(version===generation)busy=false; }
    }
    file.addEventListener('change',async()=>{
      const chosen=file.files[0];if(!chosen)return;
      const version=++generation;apply.disabled=true;error.hidden=true;q('#csv-review').hidden=true;
      if(chosen.size>1048576){dataset=null;fail(new Error('CSV exceeds 1 MiB.'));return;}
      try { const content=new TextDecoder('utf-8',{fatal:true}).decode(await chosen.arrayBuffer());if(version!==generation||!dialog.open)return;dataset={name:chosen.name,content,delimiter:delimiter.value,sharing:q('#csv-sharing').value,on_eof:q('#csv-eof').value};await preview(true); }
      catch(e){if(version===generation){dataset=null;fail(e);}}
    });
    delimiter.addEventListener('change',()=>{if(dataset){dataset.delimiter=delimiter.value;preview(true);}});
    for(const id of ['#csv-sharing','#csv-eof'])q(id).addEventListener('change',behavior);
    const close=()=>dialog.close();q('#csv-cancel').addEventListener('click',close);
    dialog.addEventListener('close',()=>{generation++;busy=false;dataset=null;callback=null;});
    apply.addEventListener('click',async()=>{
      if(!dataset||busy)return;busy=true;apply.disabled=true;error.hidden=true;const version=generation;
      const value=structuredClone({...dataset,sharing:q('#csv-sharing').value,on_eof:q('#csv-eof').value});
      for(const input of dialog.querySelectorAll('input,select'))input.disabled=true;
      try { await api('/api/csv/validate','POST',value);if(version!==generation||!dialog.open)return;if(callback(value)===false)throw new Error('The dataset could not be applied. Check unfinished fields and the 1 MiB configuration limit.');close(); }
      catch(e){if(version===generation)fail(e);}
      finally{if(version===generation){busy=false;for(const input of dialog.querySelectorAll('input,select'))input.disabled=false;if(dialog.open)apply.disabled=false;}}
    });
    return {open(scenario,onApply){
      callback=onApply;dataset=scenario.csv?structuredClone(scenario.csv):null;for(const input of dialog.querySelectorAll('input,select'))input.disabled=false;file.value='';error.hidden=true;apply.disabled=true;q('#csv-review').hidden=true;
      q('#csv-title').textContent=dataset?'Edit CSV dataset':'Import a CSV dataset';
      q('.csv-intro').textContent='Assign one row to each journey iteration. Requests and loops within that journey reuse the row.'+(scenario.data?.length?' Applying a CSV replaces this journey’s inline dataset. Undo on the canvas restores it.':'');
      const value=dataset?.delimiter||',';
      if(!Array.from(delimiter.options).some(o=>o.value===value)){const option=el('option',`Custom (${value})`);option.value=value;delimiter.append(option);}delimiter.value=value;
      q('#csv-sharing').value=dataset?.sharing||'shared';q('#csv-eof').value=dataset?.on_eof||'recycle';behavior();dialog.showModal();if(dataset)preview();
    }};
  }
  return {create};
})();
