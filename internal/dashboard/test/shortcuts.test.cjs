const {test}=require('node:test');
const assert=require('node:assert/strict');
const {controller}=require('../assets/shortcuts.js');

function setup(){
  const actions=[],hints=[],timers=new Map();let next=0;
  const state={view:'build',modal:false,characters:true,undo:true,starting:false};
  const keys=controller({perform:a=>actions.push(a),allowed:a=>!['validate','save','start','undo'].includes(a)||state.view==='build'&&(a!=='undo'||state.undo)&&(a!=='start'||!state.starting),modal:()=>state.modal,characters:()=>state.characters,hint:v=>hints.push(v),schedule:fn=>{timers.set(++next,fn);return next;},cancel:id=>timers.delete(id)});
  function press(key,extra={}){
    const e={key,target:{closest:()=>null},ctrlKey:false,metaKey:false,altKey:false,shiftKey:false,repeat:false,isComposing:false,defaultPrevented:false,preventDefault(){this.defaultPrevented=true;},...extra};
    keys.keydown(e);return e;
  }
  return {keys,state,press,actions,hints,expire(){for(const fn of [...timers.values()])fn();}};
}

test('G sequences navigate with a hint, case-insensitively, and never start traffic',()=>{
  for(const [key,view] of Object.entries({t:'tests',b:'build',l:'live',h:'history',w:'workers',g:'guide',c:'coverage'})){
    const p=setup();assert.equal(p.press('G',{shiftKey:true}).defaultPrevented,true);assert.equal(p.hints.at(-1),true);
    assert.equal(p.press(key.toUpperCase(),{shiftKey:true}).defaultPrevented,true);assert.deepEqual(p.actions,[view]);assert.equal(p.hints.at(-1),false);
  }
  const shifted=setup();shifted.press('g');shifted.press('Shift',{shiftKey:true});shifted.press('B',{shiftKey:true});assert.deepEqual(shifted.actions,['build']);
  const p=setup();p.press('g');p.press('s');p.press('Enter');assert.deepEqual(p.actions,[]);
});

test('prefixes expire and reset on Escape, invalid input, focus changes and modifiers',()=>{
  const p=setup();p.press('g');p.expire();p.press('b');assert.deepEqual(p.actions,[]);
  p.press('g');assert.equal(p.press('Escape').defaultPrevented,true);p.press('h');assert.deepEqual(p.actions,[]);
  p.press('g');p.press('x');p.press('h');assert.deepEqual(p.actions,[]);
  p.press('g');p.keys.reset();p.press('l');assert.deepEqual(p.actions,[]);
  p.press('g');p.press('c',{ctrlKey:true});p.press('b');assert.deepEqual(p.actions,[]);
});

test('typing, native selects, contenteditable and nested input controls cannot trigger navigation',()=>{
  for(const target of [{closest:()=>({})},{isContentEditable:true},{closest:()=>null}]){
    const p=setup();const extra=target.isContentEditable||target.closest?.()?{target}:{target,composedPath:()=>[target,{isContentEditable:true}]};
    for(const key of ['g','b','?'])assert.equal(p.press(key,extra).defaultPrevented,false);
    assert.deepEqual(p.actions,[]);
  }
});

test('builder accelerators work with either platform modifier and distinguish validation from start',()=>{
  for(const modifier of ['ctrlKey','metaKey']){
    const p=setup(),typing={closest:()=>({})};
    p.press('Enter',{[modifier]:true,target:typing});p.press('s',{[modifier]:true,target:typing});
    assert.deepEqual(p.actions,['validate','save']);
    p.press('Enter',{[modifier]:true,shiftKey:true});assert.deepEqual(p.actions,['validate','save','start']);
    p.press('z',{[modifier]:true});assert.equal(p.actions.at(-1),'undo');
  }
});

test('canvas undo leaves text undo and other browser/editing shortcuts untouched',()=>{
  const p=setup();
  for(const key of ['z','c','v','a','f','r','l','w','t','p']){
    const extra={ctrlKey:true,...(key==='z'?{target:{closest:()=>({})}}:{})};assert.equal(p.press(key,extra).defaultPrevented,false);
  }
  assert.equal(p.press('s',{ctrlKey:true,shiftKey:true}).defaultPrevented,false);
  assert.equal(p.press('z',{metaKey:true,shiftKey:true}).defaultPrevented,false);
  p.state.undo=false;assert.equal(p.press('z',{metaKey:true}).defaultPrevented,false);
  assert.deepEqual(p.actions,[]);
});

test('builder actions are scoped and cannot start another run while one is starting',()=>{
  const p=setup();p.state.view='history';
  for(const [key,shiftKey] of [['s',false],['Enter',false],['Enter',true],['z',false]])assert.equal(p.press(key,{metaKey:true,shiftKey}).defaultPrevented,false);
  p.state.view='build';p.state.starting=true;p.press('Enter',{ctrlKey:true,shiftKey:true});assert.deepEqual(p.actions,[]);
});

test('open dialogs, IME composition, held keys and consumed events block every app action',()=>{
  for(const extra of [{isComposing:true},{keyCode:229},{repeat:true},{defaultPrevented:true},{altKey:true},{ctrlKey:true,metaKey:true}]){
    const p=setup();for(const key of ['g','b','?','s','Enter']){p.press(key,extra);p.press(key,{ctrlKey:true,...extra});}assert.deepEqual(p.actions,[]);
  }
  const p=setup();p.state.modal=true;for(const key of ['g','b','?'])p.press(key);p.press('Enter',{metaKey:true,shiftKey:true});p.press('s',{ctrlKey:true});assert.deepEqual(p.actions,[]);
});

test('character shortcuts can be disabled while modifier actions stay available',()=>{
  const p=setup();p.state.characters=false;for(const key of ['g','b','?'])assert.equal(p.press(key).defaultPrevented,false);
  p.press('Enter',{ctrlKey:true});assert.deepEqual(p.actions,['validate']);
  p.state.characters=true;assert.equal(p.press('?').defaultPrevented,true);assert.equal(p.actions.at(-1),'help');
});
