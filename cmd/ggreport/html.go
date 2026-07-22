package main

import (
	_ "embed"
)

//go:embed assets/chart.umd.min.js
var chartJS string

// htmlTemplate returns the complete HTML document with embedded CSS, Chart.js, and data.
func htmlTemplate(jsonData string) string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>ggreport — Session Analytics</title>
<style>
:root {
  --bg: #0d1117; --surface: #161b22; --border: #30363d; --text: #c9d1d9;
  --text-dim: #8b949e; --accent: #58a6ff; --green: #3fb950; --red: #f85149;
  --orange: #d29922; --purple: #bc8cff; --cyan: #39c5cf;
}
* { margin: 0; padding: 0; box-sizing: border-box; }
body { background: var(--bg); color: var(--text); font-family: -apple-system, 'Segoe UI', Helvetica, Arial, sans-serif; font-size: 14px; line-height: 1.5; }
.header { background: var(--surface); border-bottom: 1px solid var(--border); padding: 16px 24px; display: flex; justify-content: space-between; align-items: center; position: sticky; top: 0; z-index: 100; }
.header h1 { font-size: 18px; font-weight: 600; }
.header .meta { color: var(--text-dim); font-size: 12px; }
.tabs { display: flex; gap: 0; background: var(--surface); border-bottom: 1px solid var(--border); padding: 0 24px; position: sticky; top: 55px; z-index: 99; }
.tab { padding: 10px 20px; cursor: pointer; color: var(--text-dim); border-bottom: 2px solid transparent; transition: all 0.15s; font-size: 13px; font-weight: 500; }
.tab:hover { color: var(--text); }
.tab.active { color: var(--accent); border-bottom-color: var(--accent); }
.content { padding: 24px; max-width: 1400px; margin: 0 auto; }
.tab-panel { display: none; }
.tab-panel.active { display: block; }

/* Cards */
.cards { display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); gap: 16px; margin-bottom: 24px; }
.card { background: var(--surface); border: 1px solid var(--border); border-radius: 8px; padding: 16px; }
.card .label { color: var(--text-dim); font-size: 12px; text-transform: uppercase; letter-spacing: 0.5px; margin-bottom: 6px; }
.card .value { font-size: 24px; font-weight: 600; }
.card .sub { color: var(--text-dim); font-size: 11px; margin-top: 4px; }

/* Chart container */
.chart-box { background: var(--surface); border: 1px solid var(--border); border-radius: 8px; padding: 20px; margin-bottom: 24px; }
.chart-box h3 { font-size: 14px; margin-bottom: 16px; color: var(--text); }
.chart-container { position: relative; height: 320px; }
.chart-container.half { height: 280px; }
.grid-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 24px; }

/* Table */
table { width: 100%; border-collapse: collapse; }
th, td { text-align: left; padding: 10px 12px; border-bottom: 1px solid var(--border); font-size: 13px; }
th { color: var(--text-dim); font-weight: 500; text-transform: uppercase; font-size: 11px; letter-spacing: 0.5px; cursor: pointer; user-select: none; }
th:hover { color: var(--accent); }
th.sort-asc::after { content: " \25B2"; color: var(--accent); }
th.sort-desc::after { content: " \25BC"; color: var(--accent); }
tr:hover { background: rgba(88,166,255,0.06); }
tr.session-row { cursor: pointer; }
td.num { text-align: right; font-variant-numeric: tabular-nums; }
.pill { display: inline-block; padding: 2px 8px; border-radius: 12px; font-size: 11px; font-weight: 500; }
.pill-green { background: rgba(63,185,80,0.15); color: var(--green); }
.pill-red { background: rgba(248,81,73,0.15); color: var(--red); }

/* Detail view */
.detail-header { display: flex; align-items: center; gap: 12px; margin-bottom: 24px; }
.back-btn { background: var(--surface); border: 1px solid var(--border); color: var(--text); padding: 8px 16px; border-radius: 6px; cursor: pointer; font-size: 13px; }
.back-btn:hover { border-color: var(--accent); }
.detail-title { font-size: 18px; font-weight: 600; }
.detail-info { color: var(--text-dim); font-size: 12px; }
.muted { color: var(--text-dim); }
.empty-state { text-align: center; padding: 60px 20px; color: var(--text-dim); }
.empty-state h3 { font-size: 16px; margin-bottom: 8px; }

/* Tool breakdown */
.tool-row { display: flex; align-items: center; gap: 8px; padding: 8px 0; border-bottom: 1px solid var(--border); }
.tool-name { min-width: 140px; font-weight: 500; }
.tool-bar { flex: 1; height: 8px; background: var(--bg); border-radius: 4px; overflow: hidden; }
.tool-bar-fill { height: 100%; border-radius: 4px; background: var(--accent); transition: width 0.3s; }
.tool-stat { min-width: 60px; text-align: right; font-variant-numeric: tabular-nums; color: var(--text-dim); }

/* Filter bar */
.filter-bar { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; margin-bottom: 16px; }
.filter-group { display: flex; align-items: center; gap: 6px; }
.filter-label { color: var(--text-dim); font-size: 12px; text-transform: uppercase; letter-spacing: 0.5px; }
.filter-select, .filter-input { background: var(--surface); border: 1px solid var(--border); color: var(--text); padding: 6px 10px; border-radius: 6px; font-size: 13px; outline: none; }
.filter-select:focus, .filter-input:focus { border-color: var(--accent); }
.filter-select { min-width: 120px; }
.filter-input { width: 130px; }
.filter-hint { color: var(--text-dim); font-size: 12px; }
.filter-count { color: var(--text-dim); font-size: 12px; margin-left: auto; }

/* Range slider */
.range-slider { position: relative; height: 40px; margin: 8px 0 4px; user-select: none; }
.range-track { position: absolute; top: 50%; left: 0; right: 0; height: 6px; background: var(--border); border-radius: 3px; transform: translateY(-50%); }
.range-fill { position: absolute; top: 50%; height: 6px; background: var(--accent); border-radius: 3px; transform: translateY(-50%); }
.range-handle { position: absolute; top: 50%; width: 18px; height: 18px; background: var(--surface); border: 2px solid var(--accent); border-radius: 50%; cursor: grab; transform: translate(-50%, -50%); z-index: 2; transition: box-shadow 0.15s; }
.range-handle:hover, .range-handle.dragging { box-shadow: 0 0 0 6px rgba(88,166,255,0.15); }
.range-handle.dragging { cursor: grabbing; }
.range-labels { display: flex; justify-content: space-between; color: var(--text-dim); font-size: 12px; font-variant-numeric: tabular-nums; margin-top: 2px; }
</style>
</head>
<body>

<div class="header">
  <h1>ggreport</h1>
  <div class="meta" id="generatedAt"></div>
</div>

<div class="tabs">
  <div class="tab active" data-tab="overview">Overview</div>
  <div class="tab" data-tab="sessions">Sessions</div>
  <div class="tab" data-tab="detail">Session Detail</div>
  <div class="tab" data-tab="performance">Performance</div>
</div>

<div class="content">

<!-- OVERVIEW -->
<div class="tab-panel active" id="tab-overview">
  <div class="cards" id="overviewCards"></div>
  <div class="chart-box">
    <h3>Daily Token Usage</h3>
    <div class="chart-container"><canvas id="dailyChart"></canvas></div>
  </div>
  <div class="grid-2">
    <div class="chart-box">
      <h3>Token Distribution by Workspace</h3>
      <div class="chart-container half"><canvas id="wsChart"></canvas></div>
    </div>
    <div class="chart-box">
      <h3>Tool Call Summary</h3>
      <div id="toolSummaryList"></div>
    </div>
  </div>
</div>

<!-- SESSIONS -->
<div class="tab-panel" id="tab-sessions">
  <div class="filter-bar" id="filterBar"></div>
  <div class="chart-box" style="padding:0;overflow:hidden;">
    <table id="sessionsTable">
      <thead><tr id="sessionsHeader"></tr></thead>
      <tbody id="sessionsBody"></tbody>
    </table>
  </div>
</div>

<!-- DETAIL -->
<div class="tab-panel" id="tab-detail">
  <div id="detailEmpty" class="empty-state">
    <h3>No session selected</h3>
    <p>Go to the Sessions tab and click a row to see details.</p>
  </div>
  <div id="detailContent" style="display:none;">
    <div class="detail-header">
      <button class="back-btn" onclick="switchTab('sessions')">← Back</button>
      <div>
        <div class="detail-title" id="detailTitle"></div>
        <div class="detail-info" id="detailInfo"></div>
      </div>
    </div>
    <div class="cards" id="detailCards"></div>
    <div class="chart-box">
      <h3>Turn Range Filter</h3>
      <div class="range-slider" id="rangeSlider">
        <div class="range-track"></div>
        <div class="range-fill" id="rangeFill"></div>
        <div class="range-handle" id="rangeHandleL"></div>
        <div class="range-handle" id="rangeHandleR"></div>
      </div>
      <div class="range-labels"><span id="rangeLabelL"></span><span id="rangeLabelR"></span></div>
    </div>
    <div class="chart-box">
      <h3>Per-Turn Token Usage</h3>
      <div class="chart-container"><canvas id="turnTokenChart"></canvas></div>
    </div>
    <div class="chart-box">
      <h3>TTFT Comparison by Model</h3>
      <div class="chart-container"><canvas id="ttftCompareChart"></canvas></div>
    </div>
    <div class="chart-box">
      <h3>Tool Statistics</h3>
      <div id="detailToolList"></div>
    </div>
  </div>
</div>

<!-- PERFORMANCE -->
<div class="tab-panel" id="tab-performance">
  <div class="chart-box">
    <h3>TTFT Distribution (all sessions)</h3>
    <div class="chart-container"><canvas id="ttftHistChart"></canvas></div>
  </div>
  <div class="chart-box">
    <h3>LLM Call Duration Distribution</h3>
    <div class="chart-container"><canvas id="durHistChart"></canvas></div>
  </div>
  <div class="grid-2">
    <div class="chart-box">
      <h3>Tool Success Rate</h3>
      <div class="chart-container half"><canvas id="toolSuccessChart"></canvas></div>
    </div>
    <div class="chart-box">
      <h3>Slowest Tool Calls (Top 10)</h3>
      <div id="slowestTools"></div>
    </div>
  </div>
</div>

</div><!-- /content -->

<script>` + chartJS + `</script>
<script>
window.__DATA__ = ` + jsonData + `;
</script>
<script>
(function() {
  'use strict';
  const D = window.__DATA__;
  const fmt = (n) => {
    if (n >= 1e12) return (n/1e12).toFixed(1)+'T';
    if (n >= 1e9) return (n/1e9).toFixed(1)+'B';
    if (n >= 1e6) return (n/1e6).toFixed(1)+'M';
    if (n >= 1e3) return (n/1e3).toFixed(1)+'K';
    return ''+n;
  };
  const fmtDate = (s) => { try { return new Date(s).toLocaleString(); } catch(e){ return s; } };
  const fmtDay = (s) => s;
  const fmtMs = (ms) => {
    if (ms <= 0) return '-';
    if (ms < 1000) return ms+'ms';
    return (ms/1000).toFixed(1)+'s';
  };
  const fmtTurnTime = (t) => {
    if (t.ts) { try { return new Date(t.ts).toLocaleString([], {month:'short',day:'numeric',hour:'2-digit',minute:'2-digit'}); } catch(e){} }
    if (t.day) return t.day;
    return '#'+t.index;
  };

  // === Global stats ===
  let totalInput=0, totalOutput=0, totalCache=0, totalLLM=0, totalTool=0;
  let allTTFT=[], allDur=[];
  let slowestToolCalls=[];

  D.sessions.forEach(s => {
    totalInput += s.totalInput; totalOutput += s.totalOutput;
    totalCache += s.totalCache; totalLLM += s.llmCalls;
    totalTool += s.toolCalls;
    s.turns.forEach(t => {
      if (t.ttftMs > 0) allTTFT.push(t.ttftMs);
      if (t.durMs > 0) allDur.push(t.durMs);
    });
  });

  // === Header ===
  document.getElementById('generatedAt').textContent =
    'Generated ' + fmtDate(D.generatedAt) + ' · ' + D.sessions.length + ' sessions';

  // === Tab switching ===
  function switchTab(name) {
    document.querySelectorAll('.tab').forEach(t => t.classList.toggle('active', t.dataset.tab === name));
    document.querySelectorAll('.tab-panel').forEach(p => p.classList.toggle('active', p.id === 'tab-'+name));
    window.scrollTo(0, 0);
  }
  window.switchTab = switchTab;
  document.querySelectorAll('.tab').forEach(t => {
    t.addEventListener('click', () => switchTab(t.dataset.tab));
  });

  // === Overview cards ===
  const cardsEl = document.getElementById('overviewCards');
  function card(label, value, sub) {
    return '<div class="card"><div class="label">'+label+'</div><div class="value">'+value+'</div>'+(sub?'<div class="sub">'+sub+'</div>':'')+'</div>';
  }
  cardsEl.innerHTML =
    card('Sessions', D.sessions.length, '') +
    card('Total Input', fmt(totalInput), 'tokens') +
    card('Total Output', fmt(totalOutput), 'tokens') +
    card('Cache Read', fmt(totalCache), 'tokens') +
    card('LLM Calls', fmt(totalLLM), '') +
    card('Tool Calls', fmt(totalTool), '');

  // === Daily chart ===
  Chart.defaults.color = '#8b949e';
  Chart.defaults.borderColor = '#30363d';
  const dailyData = D.dailyTokens;
  new Chart(document.getElementById('dailyChart'), {
    type: 'bar',
    data: {
      labels: dailyData.map(d => d.date),
      datasets: [
        { label: 'Input', data: dailyData.map(d=>d.input), backgroundColor: 'rgba(88,166,255,0.7)', yAxisID: 'yLeft' },
        { label: 'Cache', data: dailyData.map(d=>d.cache), backgroundColor: 'rgba(188,140,255,0.5)', yAxisID: 'yLeft' },
        { label: 'Output', data: dailyData.map(d=>d.output), backgroundColor: 'rgba(63,185,80,0.7)', yAxisID: 'yRight' },
      ]
    },
    options: {
      responsive: true, maintainAspectRatio: false,
      scales: {
        x: { grid: { display: false } },
        yLeft: { position: 'left', ticks: { callback: fmt }, title: { display: true, text: 'Input / Cache', color: '#8b949e' } },
        yRight: { position: 'right', ticks: { callback: fmt }, title: { display: true, text: 'Output', color: '#8b949e' }, grid: { drawOnChartArea: false } },
      },
      plugins: { legend: { position: 'top' }, tooltip: { callbacks: { label: function(ctx) { return ctx.dataset.label + ': ' + fmt(ctx.parsed.y); } } } }
    }
  });

  // === Workspace pie ===
  const wsData = D.workspaces.slice(0, 12);
  const wsColors = ['#58a6ff','#3fb950','#f85149','#d29922','#bc8cff','#39c5cf','#f778ba','#79c0ff','#7ee787','#ffa657','#ff7b72','#e3b341'];
  new Chart(document.getElementById('wsChart'), {
    type: 'doughnut',
    data: {
      labels: wsData.map(w => w.workspace.split('/').pop() || w.workspace),
      datasets: [{ data: wsData.map(w => w.input+w.output), backgroundColor: wsColors }]
    },
    options: {
      responsive: true, maintainAspectRatio: false,
      plugins: { legend: { position: 'right', labels: { boxWidth: 12, font: { size: 11 } } } }
    }
  });

  // === Tool summary list ===
  const toolSumEl = document.getElementById('toolSummaryList');
  const maxToolCalls = Math.max(...D.toolSummary.map(t=>t.calls), 1);
  toolSumEl.innerHTML = D.toolSummary.slice(0, 15).map(t => {
    const pct = (t.calls / maxToolCalls * 100).toFixed(0);
    const failRate = t.calls > 0 ? (t.failures/t.calls*100).toFixed(0) : 0;
    const failColor = failRate > 0 ? 'var(--red)' : 'var(--text-dim)';
    return '<div class="tool-row"><span class="tool-name">'+t.name+'</span>' +
      '<div class="tool-bar"><div class="tool-bar-fill" style="width:'+pct+'%"></div></div>' +
      '<span class="tool-stat">'+t.calls+' calls</span>' +
      '<span class="tool-stat" style="color:'+failColor+'">'+(failRate>0?failRate+'% fail':'ok')+'</span></div>';
  }).join('');

  // === Sessions filter bar ===
  const workspaces = [...new Set(D.sessions.map(s => s.workspace || '(unknown)'))].sort();
  const dates = D.sessions.map(s => { try { return new Date(s.createdAt); } catch(e) { return null; } }).filter(Boolean);
  const minDate = dates.length ? new Date(Math.min(...dates)) : null;
  const maxDate = dates.length ? new Date(Math.max(...dates)) : null;
  const todateInput = (d) => d.toISOString().slice(0,10);
  let filterWs = '', filterFrom = '', filterTo = '';

  const filterBar = document.getElementById('filterBar');
  function buildFilterBar() {
    let html = '<div class="filter-group"><span class="filter-label">Workspace</span>' +
      '<select class="filter-select" id="fWs"><option value="">All</option>';
    workspaces.forEach(ws => {
      const label = ws.split('/').pop() || ws;
      html += '<option value="'+esc(ws)+'">'+esc(label)+'</option>';
    });
    html += '</select></div>';
    html += '<div class="filter-group"><span class="filter-label">From</span>' +
      '<input type="date" class="filter-input" id="fFrom"' + (minDate ? ' min="'+todateInput(minDate)+'"' : '') + '></div>';
    html += '<div class="filter-group"><span class="filter-label">To</span>' +
      '<input type="date" class="filter-input" id="fTo"' + (maxDate ? ' max="'+todateInput(maxDate)+'"' : '') + '></div>';
    html += '<div class="filter-group"><button class="back-btn" id="fReset">Reset</button></div>';
    html += '<div class="filter-count" id="fCount"></div>';
    filterBar.innerHTML = html;
    document.getElementById('fWs').addEventListener('change', e => { filterWs = e.target.value; renderTable(); });
    document.getElementById('fFrom').addEventListener('change', e => { filterFrom = e.target.value; renderTable(); });
    document.getElementById('fTo').addEventListener('change', e => { filterTo = e.target.value; renderTable(); });
    document.getElementById('fReset').addEventListener('click', () => {
      filterWs = filterFrom = filterTo = '';
      document.getElementById('fWs').value = '';
      document.getElementById('fFrom').value = '';
      document.getElementById('fTo').value = '';
      renderTable();
    });
  }
  function esc(s) { const d = document.createElement('div'); d.textContent = s; return d.innerHTML; }
  buildFilterBar();

  function getFilteredSessions() {
    return D.sessions.filter(s => {
      if (filterWs && (s.workspace || '(unknown)') !== filterWs) return false;
      try {
        const d = new Date(s.createdAt);
        if (filterFrom && d < new Date(filterFrom + 'T00:00:00')) return false;
        if (filterTo && d > new Date(filterTo + 'T23:59:59')) return false;
      } catch(e) {}
      return true;
    });
  }

  // === Sessions table ===
  const cols = [
    { key: 'title', label: 'Title', sort: s => s.title || s.id.slice(0,8), display: s => s.title || s.id.slice(0,8) },
    { key: 'workspace', label: 'Workspace', sort: s => { const p=s.workspace||''; return p.split('/').pop()||p||'-'; }, display: s => { const p=s.workspace||''; return p.split('/').pop()||p||'-'; } },
    { key: 'model', label: 'Model', sort: s => s.model || '-', display: s => s.model || '-' },
    { key: 'createdAt', label: 'Created', sort: s => new Date(s.createdAt).getTime(), display: s => { try { return new Date(s.createdAt).toLocaleString(); } catch(e) { return s.createdAt; } } },
    { key: 'msgCount', label: 'Msgs', sort: s => s.msgCount, display: s => s.msgCount },
    { key: 'llmCalls', label: 'LLM', sort: s => s.llmCalls, display: s => s.llmCalls },
    { key: 'toolCalls', label: 'Tools', sort: s => s.toolCalls, display: s => s.toolCalls },
    { key: 'totalInput', label: 'Input', sort: s => s.totalInput, display: s => fmt(s.totalInput) },
    { key: 'totalOutput', label: 'Output', sort: s => s.totalOutput, display: s => fmt(s.totalOutput) },
  ];
  let sortKey = 'createdAt', sortDir = -1;
  const headerEl = document.getElementById('sessionsHeader');
  headerEl.innerHTML = cols.map(c => '<th data-key="'+c.key+'">'+c.label+'</th>').join('');
  headerEl.querySelectorAll('th').forEach((th, i) => {
    th.addEventListener('click', () => {
      const key = cols[i].key;
      if (sortKey === key) sortDir = -sortDir; else { sortKey = key; sortDir = 1; }
      renderTable();
    });
  });
  function renderTable() {
    headerEl.querySelectorAll('th').forEach((th, i) => {
      th.classList.remove('sort-asc','sort-desc');
      if (cols[i].key === sortKey) th.classList.add(sortDir===1?'sort-asc':'sort-desc');
    });
    const col = cols.find(c=>c.key===sortKey);
    const filtered = getFilteredSessions();
    document.getElementById('fCount').textContent = filtered.length + ' / ' + D.sessions.length + ' sessions';
    const sorted = filtered.sort((a,b) => {
      const va = col.sort(a);
      const vb = col.sort(b);
      if (typeof va === 'number' && typeof vb === 'number') return (va-vb)*sortDir;
      return String(va).localeCompare(String(vb))*sortDir;
    });
    document.getElementById('sessionsBody').innerHTML = sorted.map(s =>
      '<tr class="session-row" data-id="'+s.id+'">' +
      cols.map(c => '<td class="'+(typeof c.sort(s)==='number'?'num':'')+'">'+c.display(s)+'</td>').join('') +
      '</tr>'
    ).join('');
    document.querySelectorAll('.session-row').forEach(row => {
      row.addEventListener('click', () => showDetail(row.dataset.id));
    });
  }
  renderTable();

  // === Session Detail ===
  let turnTokenChart = null, ttftChart = null;
  let currentDetail = null; // {session, turns, rangeL, rangeR}

  function renderDetailCharts() {
    if (!currentDetail) return;
    const { session: s, turns, rangeL, rangeR } = currentDetail;
    const filtered = turns.filter(t => t.index >= rangeL && t.index <= rangeR);

    // Update detail cards to reflect filtered range
    const sumIn = filtered.reduce((a,t)=>a+t.input,0);
    const sumOut = filtered.reduce((a,t)=>a+t.output,0);
    const sumCache = filtered.reduce((a,t)=>a+t.cache,0);
    const tStart = filtered.length ? fmtTurnTime(filtered[0]) : '-';
    const tEnd = filtered.length ? fmtTurnTime(filtered[filtered.length-1]) : '-';
    document.getElementById('detailCards').innerHTML =
      card('Messages', s.msgCount) +
      card('LLM Calls', filtered.length, tStart + ' ~ ' + tEnd) +
      card('Tool Calls', s.toolCalls) +
      card('Input', fmt(sumIn), 'tokens') +
      card('Output', fmt(sumOut), 'tokens') +
      card('Cache', fmt(sumCache), 'tokens');

    // Per-turn token chart
    if (turnTokenChart) turnTokenChart.destroy();
    turnTokenChart = new Chart(document.getElementById('turnTokenChart'), {
      type: 'bar',
      data: {
        labels: filtered.map(t => '#'+t.index),
        datasets: [
          { label: 'Input', data: filtered.map(t=>t.input), backgroundColor: 'rgba(88,166,255,0.7)', yAxisID: 'yLeft' },
          { label: 'Cache', data: filtered.map(t=>t.cache), backgroundColor: 'rgba(188,140,255,0.5)', yAxisID: 'yLeft' },
          { label: 'Output', data: filtered.map(t=>t.output), backgroundColor: 'rgba(63,185,80,0.7)', yAxisID: 'yRight' },
        ]
      },
      options: {
        responsive: true, maintainAspectRatio: false,
        scales: {
          x: { grid:{display:false} },
          yLeft: { position: 'left', ticks:{callback:fmt}, title: { display: true, text: 'Input / Cache', color: '#8b949e' } },
          yRight: { position: 'right', ticks:{callback:fmt}, title: { display: true, text: 'Output', color: '#8b949e' }, grid: { drawOnChartArea: false } },
        },
        plugins: { legend: { position: 'top' }, tooltip: { callbacks: { label: function(ctx) { return ctx.dataset.label + ': ' + fmt(ctx.parsed.y); } } } }
      }
    });

    // TTFT comparison by model (only for filtered turns)
    if (ttftChart) ttftChart.destroy();
    const modelColors = ['#58a6ff','#3fb950','#d29922','#bc8cff','#39c5cf','#f778ba'];
    const allTurnIdx = filtered.map(t=>t.index);
    // Group by model
    const byModel = {};
    filtered.forEach(t => {
      const m = t.model || '(unknown)';
      if (!byModel[m]) byModel[m] = { turns: [], ttfts: [] };
      byModel[m].turns.push(t.index);
      byModel[m].ttfts.push(t.ttftMs);
    });
    const modelNames = Object.keys(byModel);
    ttftChart = new Chart(document.getElementById('ttftCompareChart'), {
      type: 'line',
      data: {
        labels: allTurnIdx.map(i=>'#'+i),
        datasets: modelNames.map((mname, i) => {
          const md = byModel[mname];
          const avg = md.ttfts.reduce((a,b)=>a+b,0) / Math.max(1, md.ttfts.filter(v=>v>0).length);
          return {
            label: mname + ' (avg ' + fmtMs(avg) + ')',
            data: allTurnIdx.map(idx => {
              const ti = md.turns.indexOf(idx);
              return ti >= 0 ? md.ttfts[ti] : null;
            }),
            borderColor: modelColors[i % modelColors.length],
            backgroundColor: modelColors[i % modelColors.length] + '20',
            tension: 0.2, spanGaps: true, pointRadius: 3,
          };
        })
      },
      options: {
        responsive: true, maintainAspectRatio: false,
        scales: { y: { ticks: { callback: fmtMs } } },
        plugins: { legend: { position: 'top' } }
      }
    });
  }

  // === Range slider ===
  function initRangeSlider(turns, onChange) {
    if (turns.length === 0) return;
    const indices = turns.map(t => t.index);
    const minV = Math.min(...indices), maxV = Math.max(...indices);
    let lo = minV, hi = maxV;

    const slider = document.getElementById('rangeSlider');
    const fill = document.getElementById('rangeFill');
    const hL = document.getElementById('rangeHandleL');
    const hR = document.getElementById('rangeHandleR');
    const lblL = document.getElementById('rangeLabelL');
    const lblR = document.getElementById('rangeLabelR');

    function update() {
      const pctL = maxV === minV ? 0 : (lo - minV) / (maxV - minV) * 100;
      const pctR = maxV === minV ? 100 : (hi - minV) / (maxV - minV) * 100;
      fill.style.left = pctL + '%';
      fill.style.width = (pctR - pctL) + '%';
      hL.style.left = pctL + '%';
      hR.style.left = pctR + '%';
      const turnL = turns.find(t => t.index === lo);
      const turnR = turns.find(t => t.index === hi);
      lblL.textContent = turnL ? fmtTurnTime(turnL) : ('#' + lo);
      lblR.textContent = turnR ? fmtTurnTime(turnR) : ('#' + hi);
      onChange(lo, hi);
    }

    function startDrag(handle, isLeft) {
      return function(e) {
        e.preventDefault();
        handle.classList.add('dragging');
        const rect = slider.getBoundingClientRect();
        function onMove(ev) {
          const clientX = ev.touches ? ev.touches[0].clientX : ev.clientX;
          let pct = (clientX - rect.left) / rect.width;
          pct = Math.max(0, Math.min(1, pct));
          const val = Math.round(minV + pct * (maxV - minV));
          if (isLeft) lo = Math.min(val, hi - (maxV > minV ? 1 : 0));
          else hi = Math.max(val, lo + (maxV > minV ? 1 : 0));
          update();
        }
        function onUp() {
          handle.classList.remove('dragging');
          document.removeEventListener('mousemove', onMove);
          document.removeEventListener('mouseup', onUp);
          document.removeEventListener('touchmove', onMove);
          document.removeEventListener('touchend', onUp);
        }
        document.addEventListener('mousemove', onMove);
        document.addEventListener('mouseup', onUp);
        document.addEventListener('touchmove', onMove, { passive: false });
        document.addEventListener('touchend', onUp);
      };
    }

    hL.addEventListener('mousedown', startDrag(hL, true));
    hR.addEventListener('mousedown', startDrag(hR, false));
    hL.addEventListener('touchstart', startDrag(hL, true), { passive: false });
    hR.addEventListener('touchstart', startDrag(hR, false), { passive: false });

    // Click on track to move nearest handle
    slider.addEventListener('mousedown', function(e) {
      if (e.target === hL || e.target === hR) return;
      const rect = slider.getBoundingClientRect();
      let pct = (e.clientX - rect.left) / rect.width;
      pct = Math.max(0, Math.min(1, pct));
      const val = Math.round(minV + pct * (maxV - minV));
      if (Math.abs(val - lo) < Math.abs(val - hi)) { lo = Math.min(val, hi - 1); hL.dispatchEvent(new MouseEvent('mousedown')); }
      else { hi = Math.max(val, lo + 1); }
      update();
    });

    // Initialize at full range
    lo = minV; hi = maxV;
    update();
  }

  function showDetail(id) {
    const s = D.sessions.find(x => x.id === id);
    if (!s) return;
    switchTab('detail');
    document.getElementById('detailEmpty').style.display = 'none';
    document.getElementById('detailContent').style.display = 'block';
    document.getElementById('detailTitle').textContent = s.title || s.id.slice(0,12);
    document.getElementById('detailInfo').innerHTML =
      (s.model||'') + ' \u00b7 ' + (s.vendor||'') + ' \u00b7 ' + new Date(s.createdAt).toLocaleString();

    currentDetail = { session: s, turns: s.turns, rangeL: 0, rangeR: 0 };

    initRangeSlider(s.turns, function(lo, hi) {
      currentDetail.rangeL = lo;
      currentDetail.rangeR = hi;
      renderDetailCharts();
    });

    // Tool list (static — not affected by range slider)
    const toolEl = document.getElementById('detailToolList');
    const maxCalls = Math.max(...s.tools.map(t=>t.calls), 1);
    toolEl.innerHTML = s.tools.slice().sort((a,b)=>b.calls-a.calls).map(t => {
      const pct = (t.calls/maxCalls*100).toFixed(0);
      const failRate = t.calls>0 ? (t.failures/t.calls*100).toFixed(0) : 0;
      const failColor = failRate > 0 ? 'var(--red)' : 'var(--text-dim)';
      return '<div class="tool-row"><span class="tool-name">'+t.name+'</span>' +
        '<div class="tool-bar"><div class="tool-bar-fill" style="width:'+pct+'%"></div></div>' +
        '<span class="tool-stat">'+t.calls+'x</span>' +
        '<span class="tool-stat">'+fmtMs(t.avgMs)+'</span>' +
        '<span class="tool-stat" style="color:'+failColor+'">'+(failRate>0?failRate+'% fail':'ok')+'</span></div>';
    }).join('');
  }
  window.showDetail = showDetail;

  // === Performance histograms ===
  function histogram(data, binCount) {
    if (data.length === 0) return { labels: [], counts: [] };
    const min = 0, max = Math.max(...data);
    const binSize = Math.max(1, (max - min) / binCount);
    const labels = [], counts = [];
    for (let i = 0; i < binCount; i++) {
      const lo = min + i*binSize, hi = lo+binSize;
      labels.push(fmtMs(Math.round(lo)));
      counts.push(data.filter(v => v >= lo && v < hi).length);
    }
    return { labels, counts };
  }
  function percentile(sortedArr, p) {
    if (sortedArr.length === 0) return 0;
    const idx = Math.ceil(sortedArr.length * p / 100) - 1;
    return sortedArr[Math.max(0, idx)];
  }
  const ttftSorted = [...allTTFT].sort((a,b)=>a-b);
  const h1 = histogram(ttftSorted, 20);
  new Chart(document.getElementById('ttftHistChart'), {
    type: 'bar',
    data: { labels: h1.labels, datasets: [{ label: 'Count', data: h1.counts, backgroundColor: 'rgba(88,166,255,0.6)' }] },
    options: {
      responsive: true, maintainAspectRatio: false,
      scales: { y: { beginAtZero: true } },
      plugins: {
        title: { display: true, text: 'P50: '+fmtMs(percentile(ttftSorted,50))+'  ·  P95: '+fmtMs(percentile(ttftSorted,95))+'  ·  P99: '+fmtMs(percentile(ttftSorted,99)), color: '#8b949e', font: { size: 12 } },
        legend: { display: false }
      }
    }
  });
  const durSorted = [...allDur].sort((a,b)=>a-b);
  const h2 = histogram(durSorted, 20);
  new Chart(document.getElementById('durHistChart'), {
    type: 'bar',
    data: { labels: h2.labels, datasets: [{ label: 'Count', data: h2.counts, backgroundColor: 'rgba(63,185,80,0.6)' }] },
    options: {
      responsive: true, maintainAspectRatio: false,
      scales: { y: { beginAtZero: true } },
      plugins: {
        title: { display: true, text: 'P50: '+fmtMs(percentile(durSorted,50))+'  ·  P95: '+fmtMs(percentile(durSorted,95)), color: '#8b949e', font: { size: 12 } },
        legend: { display: false }
      }
    }
  });

  // Tool success rate
  const tsData = D.toolSummary.filter(t => t.calls >= 3).slice(0, 15);
  new Chart(document.getElementById('toolSuccessChart'), {
    type: 'bar',
    data: {
      labels: tsData.map(t=>t.name),
      datasets: [{
        label: 'Success Rate %',
        data: tsData.map(t => t.calls > 0 ? ((t.calls-t.failures)/t.calls*100) : 0),
        backgroundColor: tsData.map(t => {
          const rate = t.calls > 0 ? (t.calls-t.failures)/t.calls : 1;
          if (rate >= 0.95) return 'rgba(63,185,80,0.7)';
          if (rate >= 0.8) return 'rgba(210,153,34,0.7)';
          return 'rgba(248,81,73,0.7)';
        })
      }]
    },
    options: {
      indexAxis: 'y', responsive: true, maintainAspectRatio: false,
      scales: { x: { max: 100, ticks: { callback: v => v+'%' } } },
      plugins: { legend: { display: false } }
    }
  });

  // Slowest tools — aggregate from all session tool data
  const slowTools = [];
  D.sessions.forEach(s => {
    s.tools.forEach(t => {
      if (t.avgMs > 0) slowTools.push({ name: t.name, avgMs: t.avgMs, calls: t.calls, session: s.title || s.id.slice(0,8) });
    });
  });
  slowTools.sort((a,b) => b.avgMs - a.avgMs);
  document.getElementById('slowestTools').innerHTML =
    slowTools.slice(0, 10).map((t,i) =>
      '<div class="tool-row"><span class="tool-name" style="min-width:30px;color:var(--text-dim)">'+(i+1)+'</span>' +
      '<span class="tool-name">'+t.name+'</span>' +
      '<span class="tool-stat">'+fmtMs(t.avgMs)+'</span>' +
      '<span class="tool-stat muted">'+t.calls+'x</span></div>'
    ).join('') || '<div class="muted">No data</div>';

})();
</script>
</body>
</html>`
}
