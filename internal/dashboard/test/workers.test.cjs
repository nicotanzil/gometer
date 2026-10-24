'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const {selection}=require('../assets/workers.js');

test('catalog refresh, rename and removal preserve captured configuration origins',()=>{
  const m=selection();m.load(['https://OLD.example:443/']);
  m.catalog([{id:'east',name:'East',origin:'https://old.example'}]);
  assert.deepEqual(m.unknown(),[]);assert.equal(m.includes('https://old.example'),true);
  m.catalog([{id:'east',name:'Renamed',origin:'https://new.example'}]);
  assert.deepEqual(m.origins(),['https://OLD.example:443/']);assert.deepEqual(m.unknown(),['https://OLD.example:443/']);
  m.catalog([]);assert.deepEqual(m.origins(),['https://OLD.example:443/']);
  const copy=m.origins();copy.length=0;assert.equal(m.origins().length,1);
});

test('worker mode without selections cannot become an implicit local run',()=>{
  const m=selection();assert.deepEqual(m.origins(),[]);
  m.chooseMode('workers');assert.throws(()=>m.origins(),/Select at least one worker/);
  m.toggle('https://east.example',true);assert.deepEqual(m.origins(),['https://east.example']);
  m.toggle('https://east.example',false);assert.throws(()=>m.origins(),/Select at least one worker/);
  m.chooseMode('local');assert.deepEqual(m.origins(),[]);
});

test('legacy duplicate origins remain visible for backend validation until explicitly deselected',()=>{
  const m=selection(),legacy=['https://east.example/','https://EAST.example:443'];m.load(legacy);
  assert.deepEqual(m.origins(),legacy);m.catalog([{id:'e',name:'East',origin:'https://east.example'}]);assert.deepEqual(m.origins(),legacy);
  m.toggle('https://east.example',false);assert.throws(()=>m.origins(),/Select at least one worker/);
  m.toggle('https://east.example',true);assert.deepEqual(m.origins(),['https://east.example']);
});

test('local selection is an explicit change and later loads restore original worker configurations',()=>{
  const m=selection();m.load(['http://127.0.0.1:8801']);m.chooseMode('local');assert.deepEqual(m.selected(),[]);
  m.load(['http://127.0.0.1:8801']);assert.equal(m.mode(),'workers');assert.deepEqual(m.origins(),['http://127.0.0.1:8801']);
});
