'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const {accounting, checkDescription, nearestIndex, sampleTimes, readingAt} = require('../assets/results.js');

test('workload accounting keeps missed journeys separate from HTTP attempts', () => {
  const requests = {completed:30, succeeded:28, failed:2, scheduled:30};
  const journeys = {scheduled:15, started:12, completed:10, succeeded:8, failed:2, in_flight:2, dropped_capacity:2, dropped_scheduler:1};
  const model = accounting({workload:'journeys',samples:[],result:{...requests,journeys}});
  assert.equal(model.unit, 'journeys');
  assert.deepEqual(model.segments.map(s=>s.value), [8,2,2,2,1]);
  assert.equal(model.segments.reduce((total,s)=>total+s.value,0), journeys.scheduled);
  assert.equal(model.requests.completed,30);
  assert.equal(model.workload.completed,10);
});

test('live accounting uses the latest cumulative sample and marks incomplete final coverage', () => {
  const samples = [{requests:{scheduled:5,succeeded:2}}, {requests:{scheduled:10,succeeded:4,failed:1,in_flight:2,dropped_capacity:2,dropped_scheduler:1}}];
  const live = accounting({workload:'requests',samples});
  assert.equal(live.workload.scheduled,10);
  assert.equal(live.segments.reduce((total,s)=>total+s.value,0),10);
  const partial = accounting({workload:'requests',samples,result:{scheduled:2,succeeded:2,distribution:{complete:false,unreported_planned_arrivals:40}}});
  assert.equal(partial.incomplete,true);
  assert.equal(partial.workload.scheduled,2,'Unreported worker demand must not become zero-latency success or a made-up drop.');
});

test('inspection keeps missing data, a zero reading, and negative telemetry distinct', () => {
  const s = {points:[[0,null],[1,0],[2,-4],[3,undefined],[4,NaN],[5,Infinity]]};
  assert.equal(readingAt(s,0),null);
  assert.equal(readingAt(s,1),0);
  assert.equal(readingAt(s,2),-4);
  for (const time of [1.5,3,4,5,6]) assert.equal(readingAt(s,time),null);
  assert.equal(readingAt({step:true,points:[[0,10],[2,20],[4,null],[6,0]]},3),20);
  assert.equal(readingAt({step:true,points:[[0,10],[2,null],[4,0]]},3),null);
  assert.equal(readingAt({step:true,points:[[0,10],[2,0]]},3),null,'Do not extend observations beyond their retained horizon.');
});

test('inspection selects retained timestamps without inventing intermediate observations', () => {
  const times=sampleTimes([{points:[[4,1],[2,null],[3,0]]},{points:[[3,1],[10,0],[NaN,4]]}]);
  assert.deepEqual(times,[2,3,4,10]);
  assert.equal(nearestIndex(times,-1),0);
  assert.equal(nearestIndex(times,3.2),1);
  assert.equal(nearestIndex(times,9),3);
  assert.equal(nearestIndex(times,Infinity),3);
  assert.equal(nearestIndex([],3),-1);
});

test('check labels show threshold direction and preserve the backend verdict', () => {
  const description = (name, actual, limit, unit, passed=false, context) => checkDescription({name,actual,limit,unit,passed},context);
  assert.equal(description('p95',250,200,'ms').rule,'≤ 200 ms');
  assert.equal(description('min_started_rps',8,10,'req/s').rule,'≥ 10 req/s');
  assert.equal(description('requests_completed',12,1,'requests').rule,'≥ 1 requests');
  assert.equal(description('run_completed',0,1,'boolean').observed,'No');
  assert.equal(description('test_stopped_on_error',1,0,'boolean').rule,'Required: No');
  const noSamples=description('p95',0,200,'ms',false,{latency:{samples:0}});
  assert.equal(noSamples.observed,'No samples');
  assert.equal(noSamples.passed,false,'A numerical comparison cannot override a failed check with no samples.');
  const journey=description('journeys.p95',0,300,'ms',false,{latency:{samples:20},journeys:{latency:{samples:0}}});
  assert.equal(journey.name,'Journey p95 latency');assert.equal(journey.observed,'No samples');assert.equal(journey.rule,'≤ 300 ms');
  assert.equal(description('min_started_per_second',2,3,'journeys/s').rule,'≥ 3 journeys/s');
  assert.equal(description('<script>unknown</script>',1,2,'items').name,'<script>unknown</script>');
});
