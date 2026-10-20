'use strict';

// All edits preserve the CLI configuration. Diagram coordinates are never serialized.
(function (root) {
  const copy = value => JSON.parse(JSON.stringify(value));
  const object = value => value !== null && typeof value === 'object' && !Array.isArray(value);
  const bytes = value => new TextEncoder().encode(value).length;
  const reserved = ['user_id', 'iteration', 'journey_id'];
  function shape(config) {
    if (!object(config)) throw new Error('The test configuration must be an object.');
    function stringMap(values, label) {
      if (values != null && (!object(values) || Object.values(values).some(v => typeof v !== 'string'))) throw new Error(`${label} must contain text values.`);
    }
    stringMap(config.variables, 'Test variables'); stringMap(config.headers, 'Test headers');
    if (config.scenarios === undefined || config.scenarios === null) return;
    if (!Array.isArray(config.scenarios)) throw new Error('scenarios must be an array. Correct Advanced JSON to use the canvas.');
    if (config.scenarios.length > 20) throw new Error('A test supports at most 20 journeys.');
    let count = 0;
    for (const scenario of config.scenarios) {
      if (!object(scenario) || !Array.isArray(scenario.steps)) throw new Error('Each journey needs a steps array. Correct Advanced JSON to use the canvas.');
      if (scenario.name !== undefined && typeof scenario.name !== 'string') throw new Error('Journey names must be text.');
      if (scenario.weight != null && (!Number.isInteger(scenario.weight) || scenario.weight < 1)) throw new Error('Journey weights must be positive whole numbers.');
      stringMap(scenario.variables, 'Journey variables');
      if (scenario.thresholds != null && !object(scenario.thresholds)) throw new Error('Journey thresholds must be an object.');
      if(scenario.csv!=null){if(!object(scenario.csv)||typeof scenario.csv.content!=='string')throw new Error('CSV needs a source content string.');stringMap(scenario.csv.columns,'CSV column mappings');}
      if (scenario.data != null) {
        if (!Array.isArray(scenario.data)) throw new Error('Journey data must be an array.');
        for (const row of scenario.data) stringMap(row, 'Dataset rows');
      }
      let local = 0;
      function condition(c) {
        if (!object(c) || typeof c.variable !== 'string' || (c.operator !== undefined && typeof c.operator !== 'string') || (c.value !== undefined && typeof c.value !== 'string')) throw new Error('Conditions need a variable, operator, and text comparison value.');
      }
      function visit(steps,depth=0) {
        if (!Array.isArray(steps)) throw new Error('Each flow path needs a steps array.');
        if (depth>8) throw new Error('Control flow nesting exceeds 8 levels.');
        for (const step of steps) {
          local++;count++;
          if (local>50) throw new Error('A journey supports at most 50 request or control nodes.');
          if (count>100) throw new Error('A test supports at most 100 request or control nodes in total.');
        if (!object(step)) throw new Error('Each journey step must be an object.');
        for (const key of ['name', 'url', 'method', 'body', 'pause']) if (step[key] !== undefined && typeof step[key] !== 'string') throw new Error(`Step ${key} must be text.`);
        for (const key of ['headers', 'extract']) if (step[key] != null && !object(step[key])) throw new Error(`Step ${key} must be an object.`);
        stringMap(step.headers, 'Step headers');
        for (const extraction of Object.values(step.extract || {})) {
          if (!object(extraction) || Object.entries(extraction).some(([k,v]) => ['json_pointer','header'].includes(k) && v != null && typeof v !== 'string')) throw new Error('Extractions must contain text selectors.');
        }
        if (step.assertions != null && (!Array.isArray(step.assertions) || step.assertions.some(a => !object(a)))) throw new Error('Step assertions must be an array of objects.');
        for (const assertion of step.assertions || []) if (Object.entries(assertion).some(([k,v]) => ['json_pointer','header','equals','body_contains'].includes(k) && v != null && typeof v !== 'string')) throw new Error('Assertion selectors and expected values must be text.');
        if (step.success_statuses != null && !Array.isArray(step.success_statuses)) throw new Error('Step success_statuses must be an array.');
          if (step.if != null) {
            if (!object(step.if)) throw new Error('if must be an object.');
            condition(step.if.condition);visit(step.if.then,depth+1);visit(step.if.else || [],depth+1);
          }
          if (step.loop != null) {
            const loop=step.loop;
            if (!object(loop)) throw new Error('loop must be an object.');
            for (const k of ['count','max_iterations']) if (loop[k]!=null&&!Number.isInteger(loop[k])) throw new Error('Loop limits must be whole numbers.');
            if (loop.index_variable!=null&&typeof loop.index_variable!=='string') throw new Error('Loop index variable must be text.');
            if (loop.while!=null) condition(loop.while);
            visit(loop.steps,depth+1);
          }
        }
      }
      visit(scenario.steps);
    }
  }
  function unique(base, names) {
    base = String(base || 'Request');
    for (let n = 1; ; n++) {
      const suffix = n === 1 ? '' : ` ${n}`;
      let prefix = base;
      while (bytes(prefix + suffix) > 128) prefix = Array.from(prefix).slice(0, -1).join('');
      const name = prefix + suffix;
      if (!names.includes(name)) return name;
    }
  }
  function stepName(step, index) { return step.name || `Step ${index + 1}`; }
  function kind(step) { return step.if?'branch':step.loop?'loop':'request'; }
  function flatten(steps, path='steps', depth=0, parent=null, lane='Main flow', result=[]) {
    steps.forEach((step,position)=>{
      const item={step,index:result.length,path:`${path}[${position}]`,container:path,array:steps,position,depth,parent,lane};result.push(item);
      if(step.if){flatten(step.if.then,item.path+'.if.then',depth+1,item.index,'True',result);flatten(step.if.else||[],item.path+'.if.else',depth+1,item.index,'False',result);}
      if(step.loop)flatten(step.loop.steps,item.path+'.loop.steps',depth+1,item.index,'Loop body',result);
    });return result;
  }
  function containers(scenario, initialize=false) {
    const result=[{path:'steps',label:'Main flow',array:scenario.steps}];
    for(const n of flatten(scenario.steps)) {
      if(n.step.if){if(initialize&&!n.step.if.else)n.step.if.else=[];result.push({path:n.path+'.if.then',label:stepName(n.step,n.index)+' · True',array:n.step.if.then},{path:n.path+'.if.else',label:stepName(n.step,n.index)+' · False',array:n.step.if.else||[]});}
      if(n.step.loop)result.push({path:n.path+'.loop.steps',label:stepName(n.step,n.index)+' · Loop body',array:n.step.loop.steps});
    }return result;
  }
  function mutate(config, action) {
    shape(config);
    const c=copy(config);let scenarios=c.scenarios||[],si=action.scenario??0,index=action.index??0,chosen=null;
    if(action.type==='create') {
      if(scenarios.length)throw new Error('This test already has journeys.');
      c.scenarios=[{name:'Journey 1',steps:[{name:'Request 1',method:c.method||'GET',url:c.url||'/',...(c.body?{body:c.body}:{})}]}];c.method='GET';delete c.body;si=0;index=0;
    } else if(action.type==='add-journey'||action.type==='duplicate-journey') {
      if(scenarios.length>=20)throw new Error('A test supports at most 20 journeys.');
      const source=action.type==='duplicate-journey'?scenarios[si]:{name:'Journey',steps:[{name:'Request 1',method:'GET',url:'/'}]};
      if(!source)throw new Error('Choose a journey first.');const scenario=copy(source);scenario.name=unique(action.type==='duplicate-journey'?source.name+' copy':'Journey',scenarios.map(s=>s.name));scenarios.push(scenario);c.scenarios=scenarios;si=scenarios.length-1;index=0;
    } else {
      const scenario=scenarios[si];if(!scenario)throw new Error('Choose a journey first.');
      const all=flatten(scenario.steps),node=all[index];
      if(action.type==='edit-journey')Object.assign(scenario,action.patch);
      else if(action.type==='edit-test')Object.assign(c,action.patch);
      else if(action.type==='remove-journey'){if(scenarios.length===1)throw new Error('Keep at least one journey.');scenarios.splice(si,1);si=Math.min(si,scenarios.length-1);index=0;}
      else if(['add-step','add-branch','add-loop','duplicate-step'].includes(action.type)) {
        const names=all.map(n=>stepName(n.step,n.index));
        let source;
        if(action.type==='duplicate-step') {if(!node)throw new Error('Choose a node first.');source=copy(node.step);for(const n of flatten([source])){n.step.name=unique(stepName(n.step,n.index)+' copy',names);names.push(n.step.name);}}
        else if(action.type==='add-branch')source={name:unique('Branch',names),if:{condition:{variable:'journey_id',operator:'equals',value:'1'},then:[{name:unique('True request',names),method:'GET',url:'/'}],else:[]}};
        else if(action.type==='add-loop')source={name:unique('Loop',names),loop:{count:2,index_variable:'loop_index',steps:[{name:unique('Repeated request',names),method:'GET',url:'/'}]}};
        else source={name:unique('Request',names),method:'GET',url:'/'};
        const target=action.container?containers(scenario,true).find(n=>n.path===action.container)?.array:node?.array||scenario.steps;
        if(!target)throw new Error('Choose a valid flow path.');const position=action.container?target.length:node?node.position+1:target.length;target.splice(position,0,source);chosen=source;
      } else {
        if(!node)throw new Error('Choose a node first.');
        if(action.type==='edit-step') {Object.assign(node.step,action.patch);chosen=node.step;}
        else if(action.type==='remove-step') {node.array.splice(node.position,1);if(!flatten(scenario.steps).some(n=>kind(n.step)==='request'))throw new Error('Keep at least one request in a journey.');index=Math.min(index,flatten(scenario.steps).length-1);}
        else if(action.type==='move-step'||action.type==='move-container') {
          const destination=action.type==='move-container'?containers(scenario,true).find(n=>n.path===action.container):all[action.to];
          if(!destination||!Number.isInteger(action.to??0))throw new Error('Choose a valid position for this node.');
          const path=action.type==='move-container'?destination.path:destination.container;
          if(path.startsWith(node.path+'.'))throw new Error('A control cannot be moved inside itself.');
          const target=destination.array;let position=action.type==='move-container'?target.length:destination.position;
          node.array.splice(node.position,1);if(action.type==='move-container'&&target===node.array)position--;target.splice(position,0,node.step);chosen=node.step;
        } else throw new Error('Unknown journey edit.');
      }
      if(chosen)index=flatten(scenario.steps).findIndex(n=>n.step===chosen);
    }
    shape(c);
    if((c.scenarios||[]).some(s=>!Number.isInteger(s.weight??1)||(s.weight??1)<1||(s.weight??1)>1000))throw new Error('Each journey weight must be a whole number between 1 and 1000.');
    if((c.scenarios||[]).reduce((n,s)=>n+(s.weight??1),0)>10000)throw new Error('Combined journey weights must not exceed 10000.');
    if(bytes(JSON.stringify(c))>1048576)throw new Error('Configuration exceeds 1 MiB.');
    return {config:c,scenario:si,index};
  }
  function templates(value) {
    if (typeof value !== 'string') return [];
    return Array.from(value.matchAll(/\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:\|\s*(?:url|path|json)\s*)?\}\}/g), match => match[1]);
  }
  // Read only the header for dependency hints. The server parser remains
  // authoritative for file dialect, row values and all execution validation.
  function csvVariables(csv) {
    if(!csv)return [];
    const text=csv.content.replace(/^\uFEFF/,'').replaceAll('\r\n','\n'),delimiter=csv.delimiter||',';
    let quoted=false,closed=false,value='',headers=[];
    for(let i=0;i<text.length;i++){
      const char=String.fromCodePoint(text.codePointAt(i));if(char.length>1)i++;
      if(quoted){if(char==='"'){if(text[i+1]==='"'){value+='"';i++;}else{quoted=false;closed=true;}}else value+=char;continue;}
      if(char===delimiter){headers.push(value);value='';closed=false;continue;}
      if(char==='\n'){if(!headers.length&&!value&&!closed)continue;headers.push(value);break;}
      if(closed)return [];
      if(char==='"'&&!value)quoted=true;else value+=char;
      if(i===text.length-1)headers.push(value);
    }
    return headers.map(h=>Object.hasOwn(csv.columns||{},h)?csv.columns[h]:h).filter(Boolean);
  }
  function dependencies(config, scenarioIndex) {
    const scenario=config.scenarios?.[scenarioIndex];if(!scenario)return {steps:[],edges:[]};
    const all=flatten(scenario.steps),byStep=new Map(all.map(n=>[n.step,n])),edges=new Map(),infos=[];
    const known=new Map();
    for(const [values,source] of [[config.variables,'Test variable'],[scenario.variables,'Journey variable'],[scenario.data?.[0],'Dataset row']])if(object(values))for(const name of Object.keys(values))known.set(name,{name,source,index:null,indices:[]});
    for(const name of csvVariables(scenario.csv))if(name)known.set(name,{name,source:'CSV dataset row',index:null,indices:[]});
    for(const name of reserved)known.set(name,{name,source:'Engine value',index:null,indices:[]});
    const guard=(values,c,truth)=>{const result=new Map(values);if((c.operator==='exists'&&truth)||(c.operator==='not_exists'&&!truth))if(!result.has(c.variable))result.set(c.variable,{name:c.variable,source:'Condition guard',index:null,indices:[]});return result;};
    function sequence(nodes,input) {
      let state=new Map(input);
      for(const step of nodes) {
        const {index}=byStep.get(step),before=Array.from(state.values()),missing=new Set(),inputs=new Map(),outputs=object(step.extract)?Object.keys(step.extract):[];
        function consumeName(name){const origin=state.get(name);if(!origin){missing.add(name);return;}inputs.set(name,origin);for(const from of origin.indices||[]){if(from===index)continue;const key=`${from}:${index}`;if(!edges.has(key))edges.set(key,{from,to:index,names:new Set()});edges.get(key).names.add(name);}}
        function consume(text){for(const name of templates(text))consumeName(name);}
        function condition(c){if(!['exists','not_exists'].includes(c.operator))consumeName(c.variable);consume(c.value);}
        if(step.if) {
          condition(step.if.condition);
          const a=sequence(step.if.then,guard(state,step.if.condition,true)),b=sequence(step.if.else||[],guard(state,step.if.condition,false));
          state=new Map();for(const [name,origin] of a)if(b.has(name)){const other=b.get(name),indices=[...new Set([...(origin.indices||[]),...(other.indices||[])])];state.set(name,{name,source:origin.source===other.source?origin.source:'Both branch paths',index:indices[0]??null,indices});}
        } else if(step.loop) {
          const loop=step.loop;if(loop.while)condition(loop.while);const body=loop.while?guard(state,loop.while,true):new Map(state),key=loop.index_variable;
          if(key)body.set(key,{name:key,source:'Loop iteration (1 based)',index,indices:[index]});
          const after=sequence(loop.steps,body);
          if(loop.count!=null){if(key){if(state.has(key))after.set(key,state.get(key));else after.delete(key);}state=after;}
        } else {
          const headers=new Map();for(const values of [config.headers,step.headers])if(object(values))for(const [name,value] of Object.entries(values))headers.set(name.toLowerCase(),value);
          for(const text of [step.url,step.body,...headers.values()])consume(text);
          for(const name of outputs)state.set(name,{name,source:stepName(step,index),index,indices:[index]});
          for(const a of step.assertions||[])consume(a.equals??a.body_contains);
        }
        infos[index]={before,inputs:Array.from(inputs.values()),outputs,missing:Array.from(missing)};
      }return state;
    }
    sequence(scenario.steps,known);
    return {steps:infos,edges:Array.from(edges.values(),e=>({...e,names:Array.from(e.names)}))};
  }
  function layout(steps) {
    const all=flatten(steps),lookup=new Map(all.map(n=>[n.step,n.index])),nodes=[],edges=[];
    const size=steps=>steps.reduce((s,step)=>{
      let width=288,height=178+(step.pause?54:0);
      if(step.if){const a=size(step.if.then),b=size(step.if.else||[]);width=a.width+b.width+80;height=240+Math.max(a.height,b.height);}
      if(step.loop){const body=size(step.loop.steps);width=body.width+100;height=240+body.height;}
      return {width:Math.max(s.width,width),height:s.height+height};
    },{width:288,height:0});
    const bounds=size(steps),width=bounds.width+100,cx=width/2;
    const edge=(x1,y1,x2,y2,label='',bend=null)=>edges.push({x1,y1,x2,y2,label,bend});
    function sequence(steps,x,y) {
      let previous=y;
      for(const step of steps) {
        const index=lookup.get(step),position={index,x:x-144,y:previous+32,height:126,pause:null};nodes[index]=position;edge(x,previous,x,position.y);let end=position.y+126;
        if(step.if){const a=size(step.if.then),b=size(step.if.else||[]),left=x-(b.width+80)/2,right=x+(a.width+80)/2,join=end+62+Math.max(a.height,b.height);
          edge(x,end,left,end+30,'True');edge(x,end,right,end+30,'False');
          const ay=sequence(step.if.then,left,end+30),by=sequence(step.if.else||[],right,end+30);edge(left,ay,x,join);edge(right,by,x,join);end=join;
        }else if(step.loop){const body=size(step.loop.steps),bodyX=x-30,join=end+62+body.height;edge(x,end,bodyX,end+30,'Body');const by=sequence(step.loop.steps,bodyX,end+30);edge(bodyX,by,x+144,position.y+63,'Repeat',x+body.width/2+65);edge(x,end,x,join,'Exit',x+body.width/2+85);end=join;
        }else if(step.pause){position.pause={y:end+24,height:30};edge(x,end,x,position.pause.y);end=position.pause.y+30;}
        previous=end+20;
      }return previous;
    }
    const end=sequence(steps,cx,48);edge(cx,end,cx,end+32);
    return {nodes,edges,width,height:end+92,endY:end+32,nodeWidth:288,cx};
  }
  function describe(step) {if(step.if){const c=step.if.condition;return `${c.variable} ${c.operator||'equals'}${c.value!==undefined?' '+c.value:''}`;}if(step.loop)return step.loop.count!=null?`${step.loop.count} iterations`:`While ${step.loop.while.variable} ${step.loop.while.operator||'equals'} ${step.loop.while.value??''} · max ${step.loop.max_iterations}`;return step.url||'Choose a request path';}
  const model = {copy,shape,mutate,dependencies,layout,stepName,unique,templates,flatten,containers,kind,describe};
  if (typeof module !== 'undefined' && module.exports) module.exports = model;
  else root.GoMeterJourneyModel = model;
})(globalThis);
