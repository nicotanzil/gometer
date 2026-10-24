'use strict';
(function(root){
 function create({api,open,newTest,resume}){
  const $=id=>document.getElementById(id),el=(tag,text)=>{const e=document.createElement(tag);if(text!==undefined)e.textContent=text;return e;};let records=[];
  const action=(label,fn)=>{const b=el('button',label);b.type='button';b.className='text-button';b.addEventListener('click',()=>Promise.resolve(fn()).catch(error=>{$('tests-message').textContent=error.message;}));return b;};
  function render(){const query=$('tests-search').value.trim().toLowerCase(),shown=records.filter(t=>`${t.name} ${t.preview.target_origin} ${t.preview.mode}`.toLowerCase().includes(query));$('tests-count').textContent=`${shown.length} of ${records.length} tests`;
   if(!shown.length){$('tests-list').replaceChildren(el('p',records.length?'No matching tests. Clear the search to see all tests.':'No saved tests yet. Create a test to get started.'));return;}
   const table=el('table'),head=el('thead'),row=el('tr'),body=el('tbody');for(const label of ['Test','Target origin','Revision','Updated','Actions'])row.append(el('th',label));head.append(row);
   for(const t of shown){const tr=el('tr'),name=el('td'),actions=el('td');name.append(action(t.name||'HTTP test',()=>inspect(t.id)));actions.append(action('Edit test',()=>open(t.id)),action('Use a copy',()=>open(t.id,true)));for(const value of [name,el('td',t.preview.target_origin),el('td',String(t.revision)),el('td',new Date(t.updated_at).toLocaleString()),actions])tr.append(value);body.append(tr);}table.append(head,body);$('tests-list').replaceChildren(table);
  }
  async function inspect(id){const t=await(await api('/api/tests/'+id)).json(),panel=$('test-detail');panel.replaceChildren(el('h2',t.name));const facts=el('dl');for(const [name,value]of [['Test ID',t.id],['Revision',t.revision],['Target origin',t.preview.target_origin],['Mode',t.preview.mode],['Journeys',t.preview.scenario_count],['Workers',t.preview.worker_count]])facts.append(el('dt',name),el('dd',String(value)));panel.append(facts,el('p','Request headers, bodies, query values, variables, and dataset contents are omitted from this preview.'),action('Edit test',()=>open(id)),action('Use a copy',()=>open(id,true)));if(t.source)panel.append(el('p',`Copied from ${t.source.id} · revision ${t.source.revision}`));panel.hidden=false;panel.querySelector('h2').tabIndex=-1;panel.scrollIntoView({block:'start'});panel.querySelector('h2').focus({preventScroll:true});}
  async function refresh(){records=await(await api('/api/tests')).json();render();}
  $('tests-search').addEventListener('input',render);$('tests-refresh').addEventListener('click',()=>refresh().catch(e=>$('tests-message').textContent=e.message));$('test-new').addEventListener('click',newTest);$('test-resume').addEventListener('click',resume);
  return{refresh};
 }
 root.GoMeterTests={create};
})(globalThis);
