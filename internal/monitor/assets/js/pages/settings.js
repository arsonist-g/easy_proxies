"use strict";
/* ===== Settings 页（运行参数 · 入口凭证 · 安全告警 · 访问控制） =====
 * 每个卡片独立保存（各自的保存按钮 → PUT /settings 或 /access-control），互不冲突。
 * 后端 external_ip/skip_cert/alert_enabled/入口凭证均为指针字段，未提供=保留，故分卡片保存不会互相覆盖。
 * 入口凭证变更重建内核（短暂断连）；访问控制热换（不断连）——两套语义不同，各自独立按钮。
 * 跨页锚点（告警→设置某入口块）通过 location.hash 传递。
 */
let settingsOrig = { pool_username:'', pool_password:'', multi_port_username:'', multi_port_password:'' };

// 访问控制：运营商候选（与 config.example.yaml 一致）；省份选项由后端 region.StandardNames()
//（按省级码升序：北京11…广西45…澳门82）返回，前端直接铺网格，不分区、不再排序。
const AC_ISPS = ['电信', '联通', '移动', '广电', '教育网'];
let acData = null;

EP.registerPage('settings', {
  init: async function(){ await loadSettings(); }
});

// 卡片底部右对齐的保存按钮
function cardFoot(btnId, label){
  return `<div style="margin-top:14px;text-align:right"><button class="btn btn-primary btn-sm" id="${btnId}">${label}</button></div>`;
}

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
  const entryHint = '修改后保存会重建内核，期间代理入口短暂中断（与节点增删改同等）';
  // 按模式渲染入口组：pool 模式只显代理池入口，multi-port 只显多端口入口，hybrid 两组都显。各入口卡片自带保存按钮。
  let entryCards = '';
  if(pool.enabled === true){
    entryCards += `<div class="card" id="set-pool-card" style="padding:18px;max-width:680px;margin-top:14px">
      <div style="font-weight:600;margin-bottom:4px">代理池入口凭证（listener）</div>
      <div style="font-size:11px;color:var(--text-tertiary);margin-bottom:14px">⚠ ${entryHint}。监听端口 <span class="mono">${esc(String(pool.port||''))}</span>；留空=不设认证，仅改一项则另一项保留原值</div>
      <div style="display:flex;gap:12px">
        <div class="form-row" style="flex:1"><label>用户名</label><input class="input" id="set-pool-user" value="${esc(pool.username||'')}" /></div>
        <div class="form-row" style="flex:1"><label>密码</label><input class="input mono" id="set-pool-pwd" value="${esc(pool.password||'')}" /></div>
      </div>
      ${cardFoot('set-save-pool', '保存')}
    </div>`;
  }
  if(mp.enabled === true){
    entryCards += `<div class="card" id="set-mp-card" style="padding:18px;max-width:680px;margin-top:14px">
      <div style="font-weight:600;margin-bottom:4px">多端口入口凭证（multi_port）</div>
      <div style="font-size:11px;color:var(--text-tertiary);margin-bottom:14px">⚠ ${entryHint}。起始端口 <span class="mono">${esc(String(mp.base_port||''))}</span>，应用于全部节点端口；留空=不设认证，仅改一项则另一项保留原值</div>
      <div style="display:flex;gap:12px">
        <div class="form-row" style="flex:1"><label>用户名</label><input class="input" id="set-mp-user" value="${esc(mp.username||'')}" /></div>
        <div class="form-row" style="flex:1"><label>密码</label><input class="input mono" id="set-mp-pwd" value="${esc(mp.password||'')}" /></div>
      </div>
      ${cardFoot('set-save-mp', '保存')}
    </div>`;
  }
  // 访问控制配置（独立 API；取不到则不渲染该卡片，不阻塞其它设置）
  let ac = null;
  try { ac = await api('/access-control'); acData = ac; }
  catch(e){ if(e.status===401){handle401();return;} }
  body.innerHTML = `
    <div class="card" style="padding:18px;max-width:680px">
      <div style="font-weight:600;margin-bottom:14px">基本</div>
      <div class="form-row"><label>外部 IP（external_ip，导出/复制代理链接时替换 0.0.0.0）</label><input class="input mono" id="set-external-ip" value="${esc(s.external_ip||'')}" placeholder="留空保留 0.0.0.0" /></div>
      ${cardFoot('set-save-basic', '保存')}
    </div>
    ${entryCards}
    <div class="card" style="padding:18px;max-width:680px;margin-top:14px">
      <div style="font-weight:600;margin-bottom:14px">安全</div>
      <label style="display:flex;align-items:center;gap:10px;cursor:pointer;margin-bottom:10px"><input type="checkbox" id="set-alert-enabled" ${s.alert_enabled?'checked':''} style="width:16px;height:16px" /> 启用安全告警扫描（空密码 / 弱认证入口）</label>
      <label style="display:flex;align-items:center;gap:10px;cursor:pointer"><input type="checkbox" id="set-skip-cert" ${s.skip_cert_verify?'checked':''} style="width:16px;height:16px" /> 跳过节点 TLS 证书验证（skip_cert_verify）</label>
      ${cardFoot('set-save-sec', '保存')}
    </div>
    ${renderAccessControlCard(ac)}`;
  // 绑定各卡片保存按钮（基本/入口/安全走 /settings；访问控制走 /access-control）
  const bind = (id, fn) => { const b = document.getElementById(id); if(b) b.onclick = () => fn(b); };
  bind('set-save-basic', saveBasic);
  bind('set-save-pool', savePoolEntry);
  bind('set-save-mp', saveMpEntry);
  bind('set-save-sec', saveSecurity);
  if(ac) bindAccessControl(ac);
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

/* ===== /settings 卡片保存（共用：external_ip/skip/alert/入口凭证均为指针，未提供=保留） ===== */
async function doSaveSettings(btn, body, entryChanged){
  const orig = btn.textContent;
  btn.disabled = true; btn.textContent = '保存中…';
  try {
    const r = await api('/settings', { method:'PUT', body });
    toast(r && r.message ? r.message : '已保存', 'ok');
    await loadAppSettings();
    if(entryChanged) toast('入口凭证已更新，内核正在重建…', 'warn');
  } catch(e){
    if(e.status===401){handle401();return;}
    toast(e.message || '保存失败', 'err');
  } finally {
    btn.disabled = false; btn.textContent = orig;
  }
}

// 基本卡片：只发 external_ip
async function saveBasic(btn){
  await doSaveSettings(btn, { external_ip: document.getElementById('set-external-ip').value.trim() }, false);
}

// 代理池入口凭证：仅变更项发送（留空=不设认证，nil=保留原值）
async function savePoolEntry(btn){
  const u = document.getElementById('set-pool-user');
  const p = document.getElementById('set-pool-pwd');
  const body = {};
  let changed = false;
  if(u && u.value !== settingsOrig.pool_username){ body.pool_username = u.value; changed = true; }
  if(p && p.value !== settingsOrig.pool_password){ body.pool_password = p.value; changed = true; }
  await doSaveSettings(btn, body, changed);
  if(changed && u && p){ settingsOrig.pool_username = u.value; settingsOrig.pool_password = p.value; }
}

// 多端口入口凭证：仅变更项发送
async function saveMpEntry(btn){
  const u = document.getElementById('set-mp-user');
  const p = document.getElementById('set-mp-pwd');
  const body = {};
  let changed = false;
  if(u && u.value !== settingsOrig.multi_port_username){ body.multi_port_username = u.value; changed = true; }
  if(p && p.value !== settingsOrig.multi_port_password){ body.multi_port_password = p.value; changed = true; }
  await doSaveSettings(btn, body, changed);
  if(changed && u && p){ settingsOrig.multi_port_username = u.value; settingsOrig.multi_port_password = p.value; }
}

// 安全卡片：告警扫描 + 跳过证书
async function saveSecurity(btn){
  await doSaveSettings(btn, {
    alert_enabled: document.getElementById('set-alert-enabled').checked,
    skip_cert_verify: document.getElementById('set-skip-cert').checked,
  }, false);
}

/* ===== 访问控制 section（渲染于设置页，独立 /access-control API） ===== */
function renderAccessControlCard(d){
  if(!d) return '';
  const provinces = d.provinces_options || [];
  const selProv = new Set(d.allow_provinces || []);
  const selIsp = new Set(d.allow_isps || []);
  const allowIps = (d.allow_ips || []).join('\n');
  // GeoCN 就绪只看库是否加载：database 配了即自动下载，故"未配置"几乎不发生；
  // 真正需要禁用的是"下载失败/未就绪"——此时省份/运营商/仅中国/IDC/未知ISP 整块禁用。
  const geoReady = d.geocn_loaded;
  let geoNote = '';
  if(!d.geocn_configured){
    geoNote = '未启用 GeoCN（geocn.database_path 为空），省份/运营商/仅中国判定不可用——仅 IP 白名单会生效';
  } else if(!d.geocn_loaded){
    geoNote = 'GeoCN 库未就绪（文件缺失或下载失败），省份/运营商判定暂不可用';
  }
  const dis = geoReady ? '' : ' disabled';
  const provTiles = provinces.map(p =>
    `<div class="prov-tile${selProv.has(p)?' on':''}" data-prov="${esc(p)}">${esc(p)}</div>`).join('');
  const ispTiles = AC_ISPS.map(isp =>
    `<div class="prov-tile${selIsp.has(isp)?' on':''}" data-isp="${esc(isp)}">${esc(isp)}</div>`).join('');
  return `
    <div class="card" id="set-ac-card" style="padding:18px;max-width:680px;margin-top:14px">
      <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:8px">
        <div style="font-weight:600">访问控制</div>
        <label style="display:flex;align-items:center;gap:8px;cursor:pointer"><input type="checkbox" id="ac-enabled" ${d.enabled?'checked':''} style="width:16px;height:16px" /> 启用</label>
      </div>
      <div class="form-hint" style="margin-bottom:14px">保存后立即生效、不断连；关闭=全部放行。</div>

      <div class="form-row"><label>固定 IP 白名单（allow_ips，每行一个 CIDR）</label>
        <textarea class="input mono" id="ac-allow-ips" rows="3" placeholder="127.0.0.0/8&#10;你的服务器公网IP/32" style="white-space:pre">${esc(allowIps)}</textarea>
      </div>

      ${geoNote ? `<div class="ac-geo-note">${esc(geoNote)}</div>` : ''}
      <div class="ac-geo-block${geoReady?'':' ac-disabled'}">
        <label class="ac-check" style="margin:12px 0"><input type="checkbox" id="ac-china-only" ${d.china_only?'checked':''}${dis} style="width:16px;height:16px" /> 仅允许中国 IP（china_only）</label>

        <div class="form-row"><label>省份白名单（allow_provinces，留空=不限省份）</label>
          <div class="prov-grid" id="ac-provinces">${provTiles}</div>
        </div>

        <div class="form-row"><label>允许的运营商（allow_isps，留空=不限运营商）</label>
          <div class="prov-grid" id="ac-isps">${ispTiles}</div>
        </div>

        <label class="ac-check" style="margin:12px 0"><input type="checkbox" id="ac-block-idc" ${d.block_idc?'checked':''}${dis} style="width:16px;height:16px" /> 拦截机房/数据中心 IP（block_idc）</label>

        <div class="form-row"><label>未知 ISP 处理（unknown_isp）</label>
          <select class="select" id="ac-unknown-isp" style="max-width:240px"${dis}>
            <option value="deny" ${d.unknown_isp==='deny'?'selected':''}>deny 拒绝</option>
            <option value="allow" ${d.unknown_isp==='allow'?'selected':''}>allow 放行</option>
          </select>
        </div>
      </div>

      ${cardFoot('ac-save', '保存访问控制')}
    </div>`;
}

function bindAccessControl(d){
  const provGrid = document.getElementById('ac-provinces');
  if(provGrid) provGrid.querySelectorAll('.prov-tile').forEach(el => { el.onclick = () => el.classList.toggle('on'); });
  const ispGrid = document.getElementById('ac-isps');
  if(ispGrid) ispGrid.querySelectorAll('.prov-tile').forEach(el => { el.onclick = () => el.classList.toggle('on'); });
  const saveBtn = document.getElementById('ac-save');
  if(saveBtn) saveBtn.onclick = saveAccessControl;
}

async function saveAccessControl(){
  const body = {
    enabled: document.getElementById('ac-enabled').checked,
    allow_ips: document.getElementById('ac-allow-ips').value.split('\n').map(s => s.trim()).filter(Boolean),
    china_only: document.getElementById('ac-china-only').checked,
    allow_provinces: [...document.querySelectorAll('#ac-provinces .prov-tile.on')].map(el => el.dataset.prov),
    allow_isps: [...document.querySelectorAll('#ac-isps .prov-tile.on')].map(el => el.dataset.isp),
    block_idc: document.getElementById('ac-block-idc').checked,
    unknown_isp: document.getElementById('ac-unknown-isp').value,
  };
  const btn = document.getElementById('ac-save');
  btn.disabled = true; btn.textContent = '保存中…';
  try {
    const r = await api('/access-control', { method:'PUT', body });
    toast(r && r.message ? r.message : '访问控制已更新', 'ok');
    // 局部刷新访问控制卡片（省份归一后回显），不重载其它设置项
    const fresh = await api('/access-control'); acData = fresh;
    const card = document.getElementById('set-ac-card');
    if(card){ card.outerHTML = renderAccessControlCard(fresh); bindAccessControl(fresh); }
  } catch(e){
    if(e.status===401){handle401();return;}
    toast(e.message || '保存失败', 'err');
  } finally {
    btn.disabled = false; btn.textContent = '保存访问控制';
  }
}
