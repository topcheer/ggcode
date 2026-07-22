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
        { label: 'Input', data: dailyData.map(d=>d.input), backgroundColor: 'rgba(88,166,255,0.7)', stack: 'a' },
        { label: 'Output', data: dailyData.map(d=>d.output), backgroundColor: 'rgba(63,185,80,0.7)', stack: 'a' },
        { label: 'Cache', data: dailyData.map(d=>d.cache), backgroundColor: 'rgba(188,140,255,0.5)', stack: 'a' },
      ]
    },
    options: {
      responsive: true, maintainAspectRatio: false,
      scales: { x: { stacked: true, grid: { display: false } }, y: { stacked: true, ticks: { callback: fmt } } },
      plugins: { legend: { position: 'top' } }
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

  // === Sessions table ===
  const cols = [
    { key: 'title', label: 'Title', fmt: s => s.title || s.id.slice(0,8) },
    { key: 'workspace', label: 'Workspace', fmt: s => { const p=s.workspace||''; return p.split('/').pop()||p||'-'; } },
    { key: 'model', label: 'Model', fmt: s => s.model || '-' },
    { key: 'createdAt', label: 'Created', fmt: s => new Date(s.createdAt).toLocaleDateString() },
    { key: 'msgCount', label: 'Msgs', fmt: s => s.msgCount, num: true },
    { key: 'llmCalls', label: 'LLM', fmt: s => s.llmCalls, num: true },
    { key: 'toolCalls', label: 'Tools', fmt: s => s.toolCalls, num: true },
    { key: 'totalInput', label: 'Input', fmt: s => fmt(s.totalInput), num: true },
    { key: 'totalOutput', label: 'Output', fmt: s => fmt(s.totalOutput), num: true },
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
    const sorted = [...D.sessions].sort((a,b) => {
      const va = cols.find(c=>c.key===sortKey).fmt(a);
      const vb = cols.find(c=>c.key===sortKey).fmt(b);
      if (typeof va === 'number') return (va-vb)*sortDir;
      return String(va).localeCompare(String(vb))*sortDir;
    });
    document.getElementById('sessionsBody').innerHTML = sorted.map(s =>
      '<tr class="session-row" data-id="'+s.id+'">' +
      cols.map(c => '<td'+(c.num?' class="num"':'')+'>'+c.fmt(s)+'</td>').join('') +
      '</tr>'
    ).join('');
    document.querySelectorAll('.session-row').forEach(row => {
      row.addEventListener('click', () => showDetail(row.dataset.id));
    });
  }
  renderTable();

  // === Session Detail ===
  let turnTokenChart = null, ttftChart = null;
  function showDetail(id) {
    const s = D.sessions.find(x => x.id === id);
    if (!s) return;
    switchTab('detail');
    document.getElementById('detailEmpty').style.display = 'none';
    document.getElementById('detailContent').style.display = 'block';
    document.getElementById('detailTitle').textContent = s.title || s.id.slice(0,12);
    document.getElementById('detailInfo').innerHTML =
      (s.model||'') + ' · ' + (s.vendor||'') + ' · ' + new Date(s.createdAt).toLocaleString();

    document.getElementById('detailCards').innerHTML =
      card('Messages', s.msgCount) +
      card('LLM Calls', s.llmCalls) +
      card('Tool Calls', s.toolCalls) +
      card('Input Tokens', fmt(s.totalInput)) +
      card('Output Tokens', fmt(s.totalOutput)) +
      card('Cache Read', fmt(s.totalCache));

    // Per-turn token chart
    if (turnTokenChart) turnTokenChart.destroy();
    const labels = s.turns.map(t => '#'+t.index);
    turnTokenChart = new Chart(document.getElementById('turnTokenChart'), {
      type: 'bar',
      data: {
        labels,
        datasets: [
          { label: 'Input', data: s.turns.map(t=>t.input), backgroundColor: 'rgba(88,166,255,0.7)', stack: 'a' },
          { label: 'Output', data: s.turns.map(t=>t.output), backgroundColor: 'rgba(63,185,80,0.7)', stack: 'a' },
          { label: 'Cache', data: s.turns.map(t=>t.cache), backgroundColor: 'rgba(188,140,255,0.5)', stack: 'a' },
        ]
      },
      options: {
        responsive: true, maintainAspectRatio: false,
        scales: { x: { stacked: true, grid:{display:false} }, y: { stacked: true, ticks:{callback:fmt} } },
        plugins: { legend: { position: 'top' } }
      }
    });

    // TTFT comparison by model
    if (ttftChart) ttftChart.destroy();
    const modelColors = ['#58a6ff','#3fb950','#d29922','#bc8cff','#39c5cf','#f778ba'];
    const allTurnIdx = [...new Set(s.turns.map(t=>t.index))].sort((a,b)=>a-b);
    ttftChart = new Chart(document.getElementById('ttftCompareChart'), {
      type: 'line',
      data: {
        labels: allTurnIdx.map(i=>'#'+i),
        datasets: s.modelPerf.map((mp, i) => ({
          label: mp.model + ' (avg ' + fmtMs(mp.avgTtft) + ')',
          data: allTurnIdx.map(idx => {
            const ti = mp.turnIdx.indexOf(idx);
            return ti >= 0 ? mp.ttftMs[ti] : null;
          }),
          borderColor: modelColors[i % modelColors.length],
          backgroundColor: modelColors[i % modelColors.length] + '20',
          tension: 0.2, spanGaps: true, pointRadius: 3,
        }))
      },
      options: {
        responsive: true, maintainAspectRatio: false,
        scales: { y: { ticks: { callback: fmtMs } } },
        plugins: { legend: { position: 'top' } }
      }
    });

    // Tool list
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
