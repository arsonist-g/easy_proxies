"use strict";
/* ===== Alerts 页（安全告警） =====
 * 运行时扫描（空密码/弱认证入口），不持久化。跳转目标用 go()（跨页真跳转）。
 */
EP.registerPage('alerts', {
  init: async function(){
    await loadAlerts();
    const btn = document.getElementById('alerts-refresh');
    if(btn) btn.onclick = () => loadAlerts();
  }
});

async function loadAlerts(){
  const body = document.getElementById('alerts-body');
  body.innerHTML = `<div class="card" style="padding:20px"><div class="skeleton" style="height:40px"></div></div>`;
  if(!EP.settings.mode) await loadAppSettings(); // 按模式选节点告警跳转锚点
  let data; try { data = await api('/alerts'); }
  catch(e){ if(e.status===401){handle401();return;} body.innerHTML=errBanner(e); return; }
  if(data && data.enabled === false){
    body.innerHTML = `<div class="state"><div class="ic-big" style="color:var(--text-tertiary)">⊘</div><div class="title">安全告警已关闭</div><div class="desc">在「设置」页开启扫描</div></div>`;
    return;
  }
  const items = (data && data.alerts) || [];
  if(!items.length){
    body.innerHTML = `<div class="state"><div class="ic-big" style="color:var(--success);opacity:1">✓</div><div class="title">无安全告警</div><div class="desc">所有代理入口均已设置认证</div></div>`;
    return;
  }
  const critical = items.filter(a => a.level === 'critical').length;
  const warn = `<div class="warn-strip">⚠ 检测到 ${items.length} 项安全告警${critical ? `（其中 ${critical} 项严重）` : ''}，建议尽快处理。</div>`;
  const list = items.map(a => {
    const isPool = a.code === 'empty_pool_auth' || a.code === 'weak_pool_auth';
    const color = a.level === 'critical' ? 'var(--error)' : 'var(--warning)';
    // 节点密码告警：按运行模式定位到对应入口块（pool→代理池入口；hybrid/multi-port→多端口入口）
    const nodeEntry = EP.settings.mode === 'pool' ? 'pool' : 'multi_port';
    const onclickAttr = isPool ? `go('pools')` : `go('settings','${nodeEntry}')`;
    const btn = isPool ? '去虚拟池设置' : '去设置入口密码';
    return `<div class="alert-item">
      <div class="summary-ic" style="color:${color}">${a.level === 'critical' ? '⛔' : '⚠'}</div>
      <div style="flex:1">
        <div style="font-weight:600">${esc(a.message || '')}</div>
        <div class="mono">code: ${esc(a.code || '')}${a.ref ? ' · ref: ' + esc(a.ref) : ''}</div>
      </div>
      <button class="btn btn-secondary btn-sm" onclick="${onclickAttr}">${btn}</button>
    </div>`;
  }).join('');
  body.innerHTML = warn + `<div class="card" style="padding:0">${list}</div>`;
}
