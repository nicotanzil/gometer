(function (root) {
  'use strict';
  const number = (value, digits = 1) => Number(value).toLocaleString(undefined, {maximumFractionDigits: digits});
  const finite = value => typeof value === 'number' && Number.isFinite(value);
  const pointValue = point => finite(point?.[1]);
  const element = (tag, text, className) => {
    const e = document.createElement(tag);
    if (text !== undefined) e.textContent = text;
    if (className) e.className = className;
    return e;
  };

  function accounting(run) {
    const latest = run.samples.at(-1) || {};
    const requests = run.result || latest.requests || {};
    const journeys = run.workload === 'journeys';
    const workload = journeys ? (run.result?.journeys || latest.journeys || {}) : requests;
    const segments = [
      {name:'Succeeded', value:workload.succeeded || 0, kind:'success'},
      {name:'Failed', value:workload.failed || 0, kind:'failed'},
      {name:'In flight', value:workload.in_flight || 0, kind:'active'},
      {name:'Missed · capacity', value:workload.dropped_capacity || 0, kind:'capacity'},
      {name:'Missed · scheduler', value:workload.dropped_scheduler || 0, kind:'scheduler'}
    ];
    return {requests, workload, segments, unit:journeys ? 'journeys' : 'requests', incomplete:run.result?.distribution?.complete === false};
  }

  function checkDescription(check, context) {
    const prefixed = check.name.startsWith('journeys.');
    const key = prefixed ? check.name.slice('journeys.'.length) : check.name;
    const journey = prefixed || context?.started_per_second !== undefined;
    const measured = prefixed ? context?.journeys : context;
    const names = {run_completed:'Run completed', requests_completed:'Completed HTTP attempts', p95:journey ? 'Journey p95 latency' : 'Request p95 latency', max_error_percent:journey ? 'Journey errors' : 'Request errors', max_drop_percent:'Missed workload demand', min_started_rps:'HTTP attempts / second', min_started_per_second:'Journey arrivals / second', worker_results_complete:'Worker results complete', users_stopped_on_error:'Users stopped on error', test_stopped_on_error:'Test stopped on error'};
    const name = names[key] || check.name.replaceAll('_', ' ');
    const boolean = check.unit === 'boolean';
    const lower = key === 'requests_completed' || key.startsWith('min_');
    const upper = key.startsWith('max_') || ['p95','users_stopped_on_error'].includes(key);
    const missing = key === 'p95' && measured?.latency?.samples === 0;
    const observed = missing ? 'No samples' : boolean ? (check.actual ? 'Yes' : 'No') : `${number(check.actual, 2)} ${check.unit}`;
    const rule = boolean ? `Required: ${check.limit ? 'Yes' : 'No'}` : `${lower ? '≥' : upper ? '≤' : 'Limit'} ${number(check.limit, 2)} ${check.unit}`;
    return {name, observed, rule, passed:check.passed};
  }

  function renderChecks(container, checks, context) {
    container.classList.add('rv-checks');
    container.replaceChildren(...[...checks].sort((a,b) => Number(a.passed) - Number(b.passed)).map(check => {
      const info = checkDescription(check, context), row = element('div', undefined, 'rv-check');
      row.append(element('span', info.passed ? '✓ Passed' : '× Failed', `rv-verdict ${info.passed ? 'rv-pass' : 'rv-fail'}`), element('strong', info.name), element('span', info.observed, 'rv-observed'), element('span', info.rule, 'rv-rule'));
      return row;
    }));
    if (!checks.length) container.append(element('p', 'No acceptance checks were recorded.', 'helper'));
  }

  function renderOverview(container, run) {
    const {requests, workload, segments, unit, incomplete} = accounting(run);
    const latency = requests.latency;
    const outcomes = element('section', undefined, 'panel rv-outcomes');
    const heading = element('div', undefined, 'panel-heading');
    heading.append(element('h2', 'Workload outcomes'), element('span', unit, 'chart-unit'));
    outcomes.append(heading);
    const total = workload.scheduled || 0;
    const lead = element('p', undefined, 'rv-total');
    lead.append(element('strong', number(total, 0)), document.createTextNode(` scheduled ${unit}`));
    outcomes.append(lead);
    const bar = element('div', undefined, 'rv-outcome-bar');
    bar.setAttribute('aria-hidden', 'true');
    for (const segment of segments) {
      const part = element('span', undefined, `rv-${segment.kind}`);
      part.style.width = `${total ? 100 * segment.value / total : 0}%`;
      bar.append(part);
    }
    outcomes.append(bar);
    const list = element('dl', undefined, 'rv-outcome-list');
    for (const segment of segments) {
      const row = element('div'), label = element('dt', segment.name);
      label.prepend(element('i', undefined, `rv-swatch rv-${segment.kind}`));
      const value = element('dd', number(segment.value, 0));
      value.append(element('small', total ? `${number(100 * segment.value / total)}%` : '—'));
      row.append(label, value); list.append(row);
    }
    outcomes.append(list, element('p', incomplete ? 'Incomplete worker results: only reported workload is shown. Unreported work is unknown.' : unit === 'journeys' ? `${number(requests.completed || 0, 0)} completed HTTP attempts across these journeys. Missed journeys send no requests.` : 'Missed arrivals have no response-latency sample.', incomplete ? 'rv-warning' : 'helper'));
    const latencyPanel = element('section', undefined, 'panel rv-latency');
    const latencyHeading = element('div', undefined, 'panel-heading');
    latencyHeading.append(element('h2', 'Request latency'), element('span', 'ms · cumulative', 'chart-unit'));
    latencyPanel.append(latencyHeading);
    if (latency?.samples) {
      const scale = Math.max(latency.p50_ms, latency.p95_ms, latency.p99_ms, 0.001);
      const rows = element('dl', undefined, 'rv-percentiles');
      for (const [key, label, meaning] of [['p50_ms','p50','50% finish within'],['p95_ms','p95','95% finish within'],['p99_ms','p99','99% finish within']]) {
        const row = element('div'), labelNode = element('dt', label);
        labelNode.append(element('small', meaning));
        const value = element('dd', `${number(latency[key], 2)} ms`), track = element('div', undefined, 'rv-latency-track');
        const fill = element('span'); fill.style.width = `${100 * latency[key] / scale}%`; track.append(fill);
        value.append(track); row.append(labelNode, value); rows.append(row);
      }
      const facts = element('div', undefined, 'rv-latency-facts');
      for (const [key,label] of [['min_ms','Min'],['mean_ms','Mean'],['max_ms','Max']]) {
        const fact = element('span', label); fact.append(element('strong', `${number(latency[key], 2)} ms`)); facts.append(fact);
      }
      latencyPanel.append(rows, facts, element('p', `${number(latency.samples, 0)} completed samples, including failed attempts. Percentiles are histogram estimates.`, 'helper'));
    } else latencyPanel.append(element('p', 'No completed latency samples', 'rv-no-samples'), element('p', 'Latency appears after an HTTP attempt completes. Missed arrivals never contribute a sample.', 'helper'));
    container.replaceChildren(outcomes, latencyPanel);
  }

  function sampleTimes(series) {
    return [...new Set(series.flatMap(s => s.points.map(p => p[0])).filter(finite))].sort((a,b) => a-b);
  }

  function nearestIndex(times, time) {
    if (!times.length) return -1;
    let low = 0, high = times.length - 1;
    while (low < high) { const mid = Math.floor((low + high) / 2); if (times[mid] < time) low = mid + 1; else high = mid; }
    return low > 0 && time - times[low - 1] <= times[low] - time ? low - 1 : low;
  }

  // Read exact observations only: absent values and explicit gaps stay missing.
  function readingAt(series, time) {
    const points = series.points;
    let point;
    if (series.step) {
      for (const candidate of points) { if (candidate[0] > time) break; point = candidate; }
      if (time > points.at(-1)?.[0]) point = undefined;
    } else point = points.find(p => p[0] === time);
    return pointValue(point) ? point[1] : null;
  }

  const charts = new WeakMap();
  function drawChart(canvas, series, options) {
    if (!canvas.clientWidth) return;
    let state = charts.get(canvas);
    if (!state) {
      const frame = element('div', undefined, 'rv-chart-frame');
      canvas.before(frame); frame.append(canvas);
      const tooltip = element('div', undefined, 'rv-chart-tooltip'); tooltip.hidden = true;
      const readout = element('div', undefined, 'rv-screen-reader');
      readout.id = `${canvas.id}-reading`; readout.setAttribute('role', 'status'); readout.setAttribute('aria-live', 'polite'); readout.setAttribute('aria-atomic', 'true');
      const help = element('p', 'Hover, tap, or focus chart · ← → inspect · Home / End · Esc clear', 'rv-chart-help');
      help.id = `${canvas.id}-help`;
      const legend = element('div', undefined, 'rv-chart-legend'); legend.setAttribute('aria-label', 'Chart series');
      frame.append(tooltip, readout); frame.after(legend, help);
      canvas.tabIndex = 0; canvas.setAttribute('aria-describedby', `${help.id} ${readout.id}`);
      state = {frame, tooltip, readout, legend, hidden:new Set(), time:null, times:[], series:[], options:{}};
      charts.set(canvas, state);
      const inspect = event => {
        const rect = canvas.getBoundingClientRect(), times = state.times;
        const fraction = Math.max(0, Math.min(1, (event.clientX - rect.left - 48) / (rect.width - 62)));
        state.time = times[nearestIndex(times, state.options.xStart + fraction * (state.options.xEnd - state.options.xStart))] ?? null;
        paint(canvas, state);
      };
      canvas.addEventListener('pointermove', event => { if (event.pointerType !== 'touch') inspect(event); });
      canvas.addEventListener('pointerdown', inspect);
      canvas.addEventListener('pointerleave', () => { if (document.activeElement !== canvas) { state.time = null; paint(canvas, state); } });
      canvas.addEventListener('focus', () => { if (state.time === null) state.time = state.times.at(-1) ?? null; paint(canvas, state); });
      canvas.addEventListener('blur', () => { state.time = null; paint(canvas, state); });
      canvas.addEventListener('keydown', event => {
        if (event.ctrlKey || event.metaKey || event.altKey || !['ArrowLeft','ArrowRight','Home','End','Escape'].includes(event.key)) return;
        event.preventDefault();
        const times = state.times, index = nearestIndex(times, state.time ?? Infinity);
        state.time = event.key === 'Escape' ? null : times[event.key === 'Home' ? 0 : event.key === 'End' ? times.length - 1 : Math.max(0, Math.min(times.length - 1, index + (event.key === 'ArrowLeft' ? -1 : 1)))] ?? null;
        paint(canvas, state);
      });
    }
    if (state.options.scope !== options.scope) { state.hidden.clear(); state.time = null; }
    const changed = state.series.map(s => `${s.name}:${s.color}:${s.dashed}`).join('|') !== series.map(s => `${s.name}:${s.color}:${s.dashed}`).join('|');
    state.series = series;
    state.times = sampleTimes(series);
    state.options = {...options, xStart:options.xStart || 0, xEnd:Math.max((options.xStart || 0) + 1, options.xEnd)};
    if (changed || !state.legend.children.length) {
      state.legend.replaceChildren(...series.map(s => {
        const button = element('button', undefined, 'rv-series-toggle'); button.type = 'button';
        const swatch = element('i', undefined, 'rv-line-swatch'); swatch.style.borderColor = s.color; if (s.dashed) swatch.style.borderTopStyle = 'dashed';
        button.append(swatch, document.createTextNode(s.name));
        button.setAttribute('aria-label', `Show ${s.name}`); button.setAttribute('aria-pressed', String(!state.hidden.has(s.name)));
        button.addEventListener('click', () => {
          if (state.hidden.has(s.name)) state.hidden.delete(s.name); else state.hidden.add(s.name);
          button.setAttribute('aria-pressed', String(!state.hidden.has(s.name))); paint(canvas, state);
        });
        return button;
      }));
    }
    for (const [i, button] of [...state.legend.children].entries()) button.setAttribute('aria-pressed', String(!state.hidden.has(series[i].name)));
    paint(canvas, state);
  }

  function paint(canvas, state) {
    const {series, options} = state, width = canvas.clientWidth, height = canvas.clientHeight;
    if (!width) return;
    const ratio = root.devicePixelRatio || 1;
    canvas.width = Math.round(width * ratio); canvas.height = Math.round(height * ratio);
    const ctx = canvas.getContext('2d'); ctx.scale(ratio, ratio);
    const theme = getComputedStyle(document.documentElement), css = name => theme.getPropertyValue(name).trim();
    const left = 48, right = width - 14, top = 12, bottom = height - 27;
    const visible = series.filter(s => !state.hidden.has(s.name));
    const values = visible.flatMap(s => s.points.filter(pointValue).map(p => p[1]));
    const min = options.signed ? Math.min(0, ...values) * 1.15 : 0;
    let max = options.fixedMax || Math.max(options.signed ? 0 : 1, ...values) * 1.15;
    if (max <= min) max = min + 1;
    const X = x => left + (x - options.xStart) / (options.xEnd - options.xStart) * (right - left);
    const Y = y => bottom - ((options.signed ? y : Math.max(0,y)) - min) / (max-min) * (bottom-top);
    ctx.font = `10px ${css('--font-mono')}`; ctx.lineWidth = 1;
    for (let i = 0; i <= 4; i++) {
      const y = bottom - (bottom-top)*i/4;
      ctx.strokeStyle = css('--chart-grid'); ctx.beginPath(); ctx.moveTo(left,y); ctx.lineTo(right,y); ctx.stroke();
      const value = min + (max-min)*i/4;
      ctx.fillStyle = css('--chart-axis'); ctx.textAlign = 'right';
      ctx.fillText(Math.abs(value) >= 10000 ? value.toExponential(0) : number(value, max-min<5 ? 3 : 0), left-8,y+3);
    }
    ctx.textAlign = 'center';
    for (let i=0;i<=4;i++) { const x=options.xStart+(options.xEnd-options.xStart)*i/4; ctx.fillText(`${number(x,options.xEnd>10?0:1)}s`,X(x),height-6); }
    let visiblePoints = 0;
    for (const s of visible) {
      ctx.strokeStyle=s.color; ctx.lineWidth=2; ctx.setLineDash(s.dashed?[5,4]:[]); ctx.beginPath(); let previous=null;
      for (const point of s.points) {
        if (!finite(point[0]) || !pointValue(point)) {previous=null;continue;}
        visiblePoints++;
        if (previous) {if(s.step)ctx.lineTo(X(point[0]),Y(previous[1]));ctx.lineTo(X(point[0]),Y(point[1]));}
        else ctx.moveTo(X(point[0]),Y(point[1]));
        previous=point;
      }
      ctx.stroke();ctx.setLineDash([]);
      const available=s.points.filter(pointValue);
      if(available.length===1){ctx.beginPath();ctx.arc(X(available[0][0]),Y(available[0][1]),3,0,Math.PI*2);ctx.fillStyle=s.color;ctx.fill();}
    }
    if(!visiblePoints){ctx.fillStyle=css('--chart-axis');ctx.textAlign='center';ctx.fillText(!visible.length?'All series hidden':options.empty || 'Waiting for measurements',(left+right)/2,(top+bottom)/2);}
    if (state.time === null) {state.tooltip.hidden=true;state.readout.textContent='';return;}
    const time=state.time, x=X(time);
    ctx.strokeStyle=css('--chart-axis');ctx.lineWidth=1;ctx.setLineDash([3,3]);ctx.beginPath();ctx.moveTo(x,top);ctx.lineTo(x,bottom);ctx.stroke();ctx.setLineDash([]);
    const title=element('strong',`${number(time,2)}s elapsed`), rows=[];
    for(const s of visible){
      const value=readingAt(s,time), row=element('div');
      row.append(element('span',s.name),element('b',value===null?'No sample':`${number(value,3)}${s.unit?' '+s.unit:''}`));rows.push(row);
      if(value!==null){ctx.fillStyle=s.color;ctx.beginPath();ctx.arc(x,Y(value),3.5,0,Math.PI*2);ctx.fill();}
    }
    state.tooltip.replaceChildren(title,...rows);state.tooltip.hidden=false;
    state.tooltip.style.left=`${Math.max(4,Math.min(width-224,x+12))}px`;
    const spoken=[title.textContent,...rows.map(row=>[...row.children].map(e=>e.textContent).join(': '))].join('. ');
    if(state.readout.textContent!==spoken)state.readout.textContent=spoken;
  }

  const api = {accounting, checkDescription, renderChecks, renderOverview, sampleTimes, nearestIndex, readingAt, drawChart};
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  else root.GoMeterResults = api;
})(typeof window !== 'undefined' ? window : globalThis);
