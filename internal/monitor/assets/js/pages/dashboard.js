"use strict";
/* ===== Dashboard 页（节点概览） =====
 * 含 P8 全节点探测进度展示：触发 /probe/all 后轮询 /probe/progress，进度条显示 x/y，完成 toast + 刷新。
 */
let probeTimer = null;
let dashTimer = null;

EP.registerPage('dashboard', {
  init: async function(){
    loadDashboard();
    document.getElementById('dash-refresh').onclick = () => loadDashboard().catch(e=>{ if(e.status!==401) toast(e.message,'err'); });
    document.getElementById('dash-probe').onclick = probeAll;
    // 30s 自动刷新仪表盘（运行时计数会变）
    dashTimer = setInterval(() => { if(document.visibilityState !== 'hidden') loadDashboard().catch(()=>{}); }, 30000);
    // 进入页面检测：若后台正在探测，恢复进度展示
    syncProbeProgress();
  },
  cleanup: function(){
    // pjax 切页前清两个轮询，避免离开 dashboard 后仍在后台请求
    if(probeTimer){ clearInterval(probeTimer); probeTimer = null; }
    if(dashTimer){ clearInterval(dashTimer); dashTimer = null; }
  }
});

async function loadDashboard(){
  const sub = document.getElementById('dash-sub');
  if(sub) sub.textContent = '加载中…';
  let s;
  try { s = await api('/stats'); }
  catch(e){ renderDashError(e); return; }
  renderDashboard(s);
}
function renderDashError(e){
  document.getElementById('dash-stats').innerHTML = '';
  document.getElementById('dash-charts').innerHTML = '';
  document.getElementById('dash-summary').innerHTML = '';
  if(e.status === 401){ handle401(); return; }
  document.getElementById('dash-charts').innerHTML = `<div class="error-banner">⚠ ${esc(e.message)} <button class="btn btn-secondary btn-sm" onclick="loadDashboard()">重试</button></div>`;
}

function renderDashboard(s){
  const total = s.total_nodes||0, avail = s.available_nodes||0, dup = s.duplicate_nodes||0, subs = s.active_subscriptions||0;
  const rate = total ? Math.round(avail/total*1000)/10 : 0;
  document.getElementById('dash-sub').textContent = '更新于 ' + new Date().toLocaleTimeString('zh-CN',{hour12:false});
  document.getElementById('dash-stats').innerHTML = `
    <div class="stat-card"><div class="stat-label">总节点</div><div class="stat-value">${total}</div><div class="stat-sub">订阅 + 手动</div></div>
    <div class="stat-card"><div class="stat-label">可用节点</div><div class="stat-value" style="color:var(--success)">${avail}</div><div class="stat-sub">可用率 ${rate}%</div></div>
    <div class="stat-card"><div class="stat-label">重复节点(已隐藏)</div><div class="stat-value">${dup}</div><div class="stat-sub">按落地 IP 去重</div></div>
    <div class="stat-card"><div class="stat-label">活跃订阅</div><div class="stat-value">${subs}</div><div class="stat-sub">订阅源</div></div>`;

  const ad = s.availability_distribution || {};
  const h = ad.high||0, m = ad.medium||0, l = ad.low||0;
  const tot = h+m+l || 1;
  const hEnd = (h/tot*100), mEnd = ((h+m)/tot*100);
  const donut = `conic-gradient(var(--primary) 0 ${hEnd}%, var(--success) ${hEnd}% ${mEnd}%, var(--warning) ${mEnd}% 100%)`;

  const cd = s.country_distribution || [];
  const cdMax = cd.reduce((mx,c) => Math.max(mx, c.count), 0) || 1;
  const cdHtml = cd.length ? cd.slice(0,6).map(c =>
    `<div class="bar-row"><div class="bar-label" title="${esc(c.country_name||c.country_code||'')}"><span class="flag">${flag(c.country_code)}</span> ${esc(c.country_name||c.country_code||'?')}</div><div class="bar-track"><div class="bar-fill" style="width:${c.count/cdMax*100}%"></div></div><div class="bar-val">${c.count}</div></div>`
  ).join('') : stateInline('尚无国家数据');

  const tc = (s.top_called_nodes||[]).filter(n => n.call_total > 0);
  const tcMax = tc.reduce((mx,n) => Math.max(mx, n.call_total), 0) || 1;
  const tcHtml = tc.length ? tc.slice(0,5).map(n =>
    `<div class="bar-row"><div class="bar-label">${esc(short(n.name,10))}</div><div class="bar-track"><div class="bar-fill" style="width:${n.call_total/tcMax*100}%"></div></div><div class="bar-val">${fmtNum(n.call_total)}</div></div>`
  ).join('') : stateInline('暂无调用数据（运行时计数，重启清零）');

  const ld = s.latency_distribution || {};
  const latRows = [
    ['<100ms', ld.lt100||0, 'var(--success)'], ['100-300', ld['100_300']||0, 'var(--success)'],
    ['300-500', ld['300_500']||0, 'var(--warning)'], ['>500ms', ld.gt500||0, 'var(--error)'],
    ['未知', ld.unknown||0, 'var(--text-tertiary)']
  ];
  const latMax = latRows.reduce((mx,r) => Math.max(mx, r[1]), 0) || 1;
  const latHtml = latRows.map(r =>
    `<div class="bar-row"><div class="bar-label">${r[0]}</div><div class="bar-track"><div class="bar-fill" style="width:${r[1]/latMax*100}%;background:${r[2]}"></div></div><div class="bar-val">${r[1]}</div></div>`
  ).join('');

  document.getElementById('dash-charts').innerHTML = `
    <div class="chart-card">
      <h3>可用率分布</h3>
      <div class="donut" style="background:${donut}"></div>
      <div class="donut-legend">
        <div><span class="legend-dot" style="background:var(--primary)"></span>高 (>90%) · ${h}</div>
        <div><span class="legend-dot" style="background:var(--success)"></span>中 (50-90%) · ${m}</div>
        <div><span class="legend-dot" style="background:var(--warning)"></span>低 (<50%) · ${l}</div>
      </div>
    </div>
    <div class="chart-card"><h3>国家分布</h3>${cdHtml}</div>
    <div class="chart-card"><h3>被调用 Top 5<span class="chart-note">运行时·重启清零</span></h3>${tcHtml}</div>
    <div class="chart-card"><h3>延迟分布</h3>${latHtml}</div>`;

  const sh = s.subscription_health || {};
  const shFailed = sh.failed||0;
  const shFailedNames = (sh.failed_names||[]).map(n => `<span class="mono">${esc(n)}</span>`).join('、') || '—';
  const als = s.alert_summary || {};
  const alertTotal = als.alert_count||0;
  const alertCritical = als.alert_critical||0;

  document.getElementById('dash-summary').innerHTML = `
    <div class="card summary-card">
      <div class="summary-head"><span class="summary-ic" style="color:${shFailed? 'var(--warning)':'var(--success)'}">${shFailed?'⚠':'✓'}</span> 订阅健康</div>
      <div class="summary-body">${shFailed ?
        `<span class="summary-num" style="color:var(--warning)">${shFailed}</span> 个订阅刷新失败：${shFailedNames}` :
        `<span class="summary-num" style="color:var(--success)">0</span> 全部订阅正常`}</div>
      <a class="summary-link" onclick="go('subs')">查看订阅 →</a>
    </div>
    <div class="card summary-card">
      <div class="summary-head"><span class="summary-ic" style="color:${alertTotal?'var(--error)':'var(--success)'}">${alertTotal?'⚠':'✓'}</span> 安全告警</div>
      <div class="summary-body">${alertTotal ?
        `<span class="summary-num" style="color:var(--error)">${alertTotal}</span> 项安全告警${alertCritical?`（${alertCritical} 项严重）`:''}` :
        `<span class="summary-num" style="color:var(--success)">0</span> 无安全告警`}</div>
      <a class="summary-link" onclick="go('alerts')">查看告警 →</a>
    </div>`;

  updateNavStatus(avail, total, shFailed || alertTotal);
}

/* ===== P8 全节点探测进度 ===== */
async function probeAll(){
  try {
    await api('/probe/all', { method:'POST' });
    toast('全节点探测已启动','ok');
    syncProbeProgress();
  } catch(e){ if(e.status===401){handle401();return;} toast('探测触发失败：'+e.message,'err'); }
}
async function syncProbeProgress(){
  let p;
  try { p = await api('/probe/progress'); } catch(e){ return; }
  renderProbeBar(p);
  // running → 启动轮询；完成 → 停轮询 + toast + 刷新仪表盘
  if(p.running && !probeTimer){ probeTimer = setInterval(syncProbeProgress, 1000); }
  if(!p.running && probeTimer){
    clearInterval(probeTimer); probeTimer=null;
    toast('本轮探测完成','ok');
    loadDashboard();
  }
}
function renderProbeBar(p){
  const bar = document.getElementById('probe-progress');
  if(!bar) return;
  if(!p.running){ bar.classList.add('hidden'); return; }
  bar.classList.remove('hidden');
  const total = p.total||0, probed = p.probed||0;
  const pct = total>0 ? Math.round(probed/total*100) : 0;
  bar.querySelector('.pp-fill').style.width = pct+'%';
  bar.querySelector('.pp-label').innerHTML = '<span class="spin"></span> 正在探测节点…';
  bar.querySelector('.pp-num').textContent = `${probed} / ${total}（${pct}%）`;
}
