"use strict";
/* ===== Settings 页（运行参数 · 入口凭证 · 安全告警） =====
 * 入口凭证变更后内核自动重建。跨页锚点（告警→设置某入口块）通过 location.hash 传递（替代原 SPA viewAnchor）。
 */
let settingsOrig = { pool_username:'', pool_password:'', multi_port_username:'', multi_port_password:'' };

EP.registerPage('settings', {
  init: async function(){ await loadSettings(); }
});

async function loadSettings(){
  const body = document.getElementById('settings-body');
  body.innerHTML = `<div class="card" style="padding:20px"><div class="skeleton" style="height:40px"></div></div>`;
  let s; try { s = await api('/settings'); }
  catch(e){ if(e.status===401){handle401();return;} body.innerHTML=errBanner(e); return; }
  EP.settings = { external_ip: s.external_ip||'', proxy_username: s.proxy_username||'', proxy_password: s.proxy_password||'', mode: s.mode||'', listener_port: s.listener_port||0 };
  const pool = s.pool || {}, mp = s.multi_port || {};
  settingsOrig = {
    pool_username: pool.username||'', pool_password: pool.password||'',
    multi_port_username: mp.username||'', multi_port_password: mp.password||'',
  };
  const entryHint = '修改后自动重建内核，期间代理入口短暂中断（与节点增删改同等）';
  // 按模式渲染入口组：pool 模式只显代理池入口，multi-port 只显多端口入口，hybrid 两组都显
  let entryCards = '';
  if(pool.enabled === true){
    entryCards += `<div class="card" id="set-pool-card" style="padding:18px;max-width:680px;margin-top:14px">
      <div style="font-weight:600;margin-bottom:4px">代理池入口凭证（listener）</div>
      <div style="font-size:11px;color:var(--text-tertiary);margin-bottom:14px">⚠ ${entryHint}。监听端口 <span class="mono">${esc(String(pool.port||''))}</span>；留空保存=不设认证，仅改一项则另一项保留原值</div>
      <div style="display:flex;gap:12px">
        <div class="form-row" style="flex:1"><label>用户名</label><input class="input" id="set-pool-user" value="${esc(pool.username||'')}" /></div>
        <div class="form-row" style="flex:1"><label>密码</label><input class="input mono" id="set-pool-pwd" value="${esc(pool.password||'')}" /></div>
      </div>
    </div>`;
  }
  if(mp.enabled === true){
    entryCards += `<div class="card" id="set-mp-card" style="padding:18px;max-width:680px;margin-top:14px">
      <div style="font-weight:600;margin-bottom:4px">多端口入口凭证（multi_port）</div>
      <div style="font-size:11px;color:var(--text-tertiary);margin-bottom:14px">⚠ ${entryHint}。起始端口 <span class="mono">${esc(String(mp.base_port||''))}</span>，应用于全部节点端口；留空保存=不设认证，仅改一项则另一项保留原值</div>
      <div style="display:flex;gap:12px">
        <div class="form-row" style="flex:1"><label>用户名</label><input class="input" id="set-mp-user" value="${esc(mp.username||'')}" /></div>
        <div class="form-row" style="flex:1"><label>密码</label><input class="input mono" id="set-mp-pwd" value="${esc(mp.password||'')}" /></div>
      </div>
    </div>`;
  }
  body.innerHTML = `
    <div class="card" style="padding:18px;max-width:680px">
      <div style="font-weight:600;margin-bottom:14px">基本</div>
      <div class="form-row"><label>外部 IP（external_ip，导出/复制代理链接时替换 0.0.0.0）</label><input class="input mono" id="set-external-ip" value="${esc(s.external_ip||'')}" placeholder="留空保留 0.0.0.0" /></div>
    </div>
    ${entryCards}
    <div class="card" style="padding:18px;max-width:680px;margin-top:14px">
      <div style="font-weight:600;margin-bottom:14px">安全</div>
      <label style="display:flex;align-items:center;gap:10px;cursor:pointer;margin-bottom:10px"><input type="checkbox" id="set-alert-enabled" ${s.alert_enabled?'checked':''} style="width:16px;height:16px" /> 启用安全告警扫描（空密码 / 弱认证入口）</label>
      <label style="display:flex;align-items:center;gap:10px;cursor:pointer"><input type="checkbox" id="set-skip-cert" ${s.skip_cert_verify?'checked':''} style="width:16px;height:16px" /> 跳过节点 TLS 证书验证（skip_cert_verify）</label>
    </div>
    <div style="margin-top:16px;max-width:680px"><button class="btn btn-primary" id="set-save">保存设置</button></div>`;
  document.getElementById('set-save').onclick = saveSettings;
  // 跨页锚点（告警→设置页某入口块）：滚动定位 + 闪烁高亮，用后即清
  const anchor = location.hash.slice(1);
  if(anchor === 'pool' || anchor === 'multi_port'){
    const card = document.getElementById(anchor === 'pool' ? 'set-pool-card' : 'set-mp-card');
    if(card){
      card.scrollIntoView({behavior:'smooth', block:'center'});
      card.classList.add('flash');
      setTimeout(() => card.classList.remove('flash'), 3000);
    }
    history.replaceState(null, '', location.pathname);
  }
}
async function saveSettings(){
  const external_ip = document.getElementById('set-external-ip').value.trim();
  const alert_enabled = document.getElementById('set-alert-enabled').checked;
  const skip_cert_verify = document.getElementById('set-skip-cert').checked;
  const body = { external_ip, alert_enabled, skip_cert_verify };
  // 仅在入口凭证实际变更时发送对应组，避免未改也触发内核重建
  let entryChanged = false;
  const pu = document.getElementById('set-pool-user');
  const pp = document.getElementById('set-pool-pwd');
  const mu = document.getElementById('set-mp-user');
  const mpwd = document.getElementById('set-mp-pwd');
  if(pu && pu.value !== settingsOrig.pool_username){ body.pool_username = pu.value; entryChanged = true; }
  if(pp && pp.value !== settingsOrig.pool_password){ body.pool_password = pp.value; entryChanged = true; }
  if(mu && mu.value !== settingsOrig.multi_port_username){ body.multi_port_username = mu.value; entryChanged = true; }
  if(mpwd && mpwd.value !== settingsOrig.multi_port_password){ body.multi_port_password = mpwd.value; entryChanged = true; }
  const btn = document.getElementById('set-save');
  btn.disabled = true; btn.textContent = '保存中…';
  try {
    const r = await api('/settings', { method:'PUT', body });
    toast(r && r.message ? r.message : '设置已更新', 'ok');
    await loadAppSettings();
    if(entryChanged){
      toast('入口凭证已更新，内核正在重建…', 'warn');
    }
  } catch(e){
    if(e.status===401){handle401();return;}
    toast(e.message || '保存失败', 'err');
  } finally {
    btn.disabled = false; btn.textContent = '保存设置';
  }
}
