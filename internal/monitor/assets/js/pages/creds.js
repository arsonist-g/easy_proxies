"use strict";
/* ===== Credentials 页（凭证：API Key + 订阅 Token） =====
 * 列表仅返回前缀；点复制调 /{id}/plain 取明文（session 鉴权，db 解密）。
 * copyText/copyLink 见 app.js。国家字段 stCountryField 复用 pools.js 的 makeCountryField/openCountryPicker。
 */
let stCountryField = null;
const SCOPES = [
  ['manage_nodes','管理节点'], ['manage_subscriptions','管理订阅'], ['manage_pools','管理虚拟池'],
  ['manage_credentials','管理凭证'], ['read_only','只读']
];

EP.registerPage('creds', {
  init: async function(){ await loadCreds(); }
});

async function loadCreds(){
  await Promise.all([loadAPIKeys(), loadSubTokens()]);
}
const keysState = { page:1, page_size:20 };
async function loadAPIKeys(){
  const tbl = document.getElementById('apikeys-table');
  tbl.innerHTML = skeletonTable(['名称','前缀','权限','最近使用','']);
  const q = new URLSearchParams({ page:keysState.page, page_size:keysState.page_size });
  let data; try { data = await api('/api-keys?'+q.toString()); }
  catch(e){ if(e.status===401){handle401();return;} if(e.status===501){ tbl.innerHTML=stateInline('凭证功能未启用（存储未初始化）'); return; } tbl.innerHTML=errBanner(e); return; }
  const items = data.items || [];
  if(!items.length){ document.getElementById('apikeys-pagination').style.display='none'; tbl.innerHTML = `<div class="state" style="padding:24px"><div class="desc">尚无 API Key</div><button class="btn btn-primary btn-sm" onclick="apiKeyCreateModal()">+ 新建 API Key</button></div>`; return; }
  const rows = items.map(k => {
    return `<tr>
    <td style="font-weight:600">${esc(k.name)}</td>
    <td class="mono"><span class="desc">${esc(k.key_prefix)}…</span></td>
    <td>${(k.scopes||[]).length?('<span class="badge badge-neutral">'+(k.scopes||[]).map(esc).join(' · ')+'</span>'):'—'}</td>
    <td class="mono">${fmtTime(k.last_used_at)}</td>
    <td>
      <button class="btn btn-secondary btn-sm" onclick="revealAPIKey(${k.id})">复制</button>
      <button class="btn btn-danger btn-sm" data-name="${esc(k.name)}" onclick="apiKeyDelete(${k.id}, this)">删除</button>
    </td>
  </tr>`;
  }).join('');
  tbl.innerHTML = `<div class="table-wrap${data.total > keysState.page_size ? ' paginated' : ''}"><table><thead><tr><th>名称</th><th>Key</th><th>权限</th><th>最近使用</th><th></th></tr></thead><tbody>${rows}</tbody></table></div>`;
  renderPagination('apikeys-pagination', data, (p) => { keysState.page = p; loadAPIKeys(); });
}
const tokensState = { page:1, page_size:20 };
async function loadSubTokens(){
  const tbl = document.getElementById('subtokens-table');
  tbl.innerHTML = skeletonTable(['名称','过滤','代理列表 URL','最近使用','']);
  const q = new URLSearchParams({ page:tokensState.page, page_size:tokensState.page_size });
  let data; try { data = await api('/subscribe-tokens?'+q.toString()); }
  catch(e){ if(e.status===401){handle401();return;} if(e.status===501){ tbl.innerHTML=stateInline('凭证功能未启用'); return; } tbl.innerHTML=errBanner(e); return; }
  const items = data.items || [];
  if(!items.length){ document.getElementById('subtokens-pagination').style.display='none'; tbl.innerHTML = `<div class="state" style="padding:24px"><div class="desc">尚无订阅 Token</div><button class="btn btn-primary btn-sm" onclick="subTokenCreateModal()">+ 新建 Token</button></div>`; return; }
  const rows = items.map(t => {
    const f = t.filters || {};
    const fParts = [];
    if((f.country_codes||[]).length) fParts.push(f.country_codes.join(','));
    if((f.protocols||[]).length) fParts.push(f.protocols.join(','));
    if(f.name_regex) fParts.push('/'+f.name_regex+'/');
    const fStr = fParts.length ? fParts.join(' / ') : '无过滤';
    const url = '/sub/'+t.token_prefix+'…';
    return `<tr>
      <td style="font-weight:600">${esc(t.name)}</td>
      <td class="mono">${esc(fStr)}</td>
      <td class="mono"><span class="desc">${esc(url)}</span></td>
      <td class="mono">${fmtTime(t.last_used_at)}</td>
      <td>
        <button class="btn btn-secondary btn-sm" onclick="revealSubToken(${t.id})">复制</button>
        <button class="btn btn-danger btn-sm" data-name="${esc(t.name)}" onclick="subTokenDelete(${t.id}, this)">删除</button>
      </td>
    </tr>`;
  }).join('');
  tbl.innerHTML = `<div class="table-wrap${data.total > tokensState.page_size ? ' paginated' : ''}"><table><thead><tr><th>名称</th><th>过滤</th><th>代理列表 URL</th><th>最近使用</th><th></th></tr></thead><tbody>${rows}</tbody></table></div>`;
  renderPagination('subtokens-pagination', data, (p) => { tokensState.page = p; loadSubTokens(); });
}
function apiKeyCreateModal(){
  const chips = SCOPES.map(s => `<label class="scope-chip"><input type="checkbox" value="${s[0]}" /> ${s[1]}</label>`).join('');
  openModal(`<h3>新建 API Key</h3><div class="modal-sub">用于程序化调用 /api/v1/*</div>
    <div class="form-row"><label>名称</label><input class="input" id="ak-name" placeholder="例：CI 集成" /></div>
    <div class="form-row"><label>权限范围</label><div class="scope-grid">${chips}</div></div>
    <div class="form-actions"><button class="btn btn-secondary btn-sm" onclick="closeModal()">取消</button><button class="btn btn-primary btn-sm" id="ak-save">创建</button></div>`);
  document.getElementById('ak-save').onclick = async () => {
    const name = document.getElementById('ak-name').value.trim();
    if(!name){ toast('名称不能为空','warn'); return; }
    const scopes = [...document.querySelectorAll('.scope-chip input:checked')].map(c => c.value);
    try {
      const r = await api('/api-keys', { method:'POST', body:{ name, scopes } });
      closeModal();
      secretModal('API Key 已创建', name, r.key, '调用时设置 Header：\nX-API-Key: '+r.key, '可在凭证列表点复制随时获取明文。');
      loadAPIKeys();
    } catch(e){ if(e.status===401){handle401();return;} toast('创建失败：'+e.message,'err'); }
  };
}
function subTokenCreateModal(){
  openModal(`<h3>新建订阅 Token</h3><div class="modal-sub">集成方用此 token 拉取代理列表</div>
    <div class="form-row"><label>名称</label><input class="input" id="st-name" placeholder="例：目标平台A" /></div>
    <div class="form-row"><label>国家码过滤（可选）</label>
      <div class="countries-box">
        <div class="chips" id="st-countries-chips"></div>
        <button type="button" class="btn btn-secondary btn-sm" onclick="stCountryField&&stCountryField.pick()">选择国家</button>
      </div>
    </div>
    <div class="form-row"><label>协议过滤（逗号分隔，可选）</label><input class="input mono" id="st-protocols" placeholder="例：vmess,ss" /></div>
    <div class="form-row"><label>名称正则（可选）</label><input class="input mono" id="st-regex" placeholder="例：日本|东京" /></div>
    <div class="form-actions"><button class="btn btn-secondary btn-sm" onclick="closeModal()">取消</button><button class="btn btn-primary btn-sm" id="st-save">创建</button></div>`);
  stCountryField = makeCountryField('st-countries-chips'); stCountryField.init({});
  document.getElementById('st-save').onclick = async () => {
    const name = document.getElementById('st-name').value.trim();
    if(!name){ toast('名称不能为空','warn'); return; }
    const filters = {
      country_codes: (stCountryField ? stCountryField.get() : {}).country_codes || [],
      protocols: document.getElementById('st-protocols').value.split(',').map(s=>s.trim()).filter(Boolean),
      name_regex: document.getElementById('st-regex').value.trim()
    };
    try {
      const r = await api('/subscribe-tokens', { method:'POST', body:{ name, filters } });
      closeModal();
      const url = location.origin + '/sub/' + r.token;
      secretModal('订阅 Token 已创建', name, r.token, '集成方通过 GET 拉取：\n'+url, '可在凭证列表点复制随时获取订阅链接。', url);
      loadSubTokens();
    } catch(e){ if(e.status===401){handle401();return;} toast('创建失败：'+e.message,'err'); }
  };
}
function secretModal(title, name, secret, secondary, warn, linkUrl){
  const linkBtn = linkUrl ? `<button class="btn btn-secondary btn-sm" onclick="copyLink('sec-link','订阅链接已复制')">复制订阅链接</button>` : '';
  const linkInput = linkUrl ? `<input type="hidden" id="sec-link" value="${esc(linkUrl)}" />` : '';
  openModal(`<h3>${esc(title)}</h3><div class="modal-sub">${esc(name)}</div>
    <div class="warn-strip">⚠ ${esc(warn)}</div>
    <label style="font-size:12px;color:var(--text-secondary);font-weight:500">明文</label>
    <div class="secret-box" id="sec-secret">${esc(secret)}</div>
    <label style="font-size:12px;color:var(--text-secondary);font-weight:500">用法</label>
    <div class="secret-box" id="sec-secondary" style="white-space:pre-wrap">${esc(secondary)}</div>
    <div class="form-actions">
      <button class="btn btn-secondary btn-sm" onclick="copySecret()">复制</button>
      ${linkBtn}
      <button class="btn btn-primary btn-sm" onclick="onSecretSaved()">我已保存</button>
    </div>${linkInput}`);
}
function copySecret(){
  const el = document.getElementById('sec-secret');
  if(el) copyText(el.textContent, '已复制到剪贴板');
}
function onSecretSaved(){ closeModal(); toast('请妥善保管已复制的明文','ok'); }
async function revealAPIKey(id){
  try {
    const r = await api('/api-keys/'+id+'/plain');
    if(!r.plain){ toast('未找到明文','warn'); return; }
    copyText(r.plain, 'API Key 已复制');
  } catch(e){ if(e.status===401){handle401();return;} toast('复制失败：'+e.message,'err'); }
}
async function revealSubToken(id){
  try {
    const r = await api('/subscribe-tokens/'+id+'/plain');
    if(!r.plain){ toast('未找到明文','warn'); return; }
    copyText(location.origin + '/sub/' + r.plain, '订阅链接已复制');
  } catch(e){ if(e.status===401){handle401();return;} toast('复制失败：'+e.message,'err'); }
}
function apiKeyDelete(id, btn){
  const name = btn ? btn.dataset.name : '';
  confirmModal('删除 API Key', `确定删除 API Key「${name}」？删除后立即失效，且不可恢复。`, async () => {
    try { await api('/api-keys/'+id, { method:'DELETE' }); toast('API Key 已删除','ok'); loadAPIKeys(); }
    catch(e){ if(e.status===401){handle401();return;} toast('删除失败：'+e.message,'err'); }
  }, '删除');
}
function subTokenDelete(id, btn){
  const name = btn ? btn.dataset.name : '';
  confirmModal('删除订阅 Token', `确定删除订阅 Token「${name}」？集成方将无法再拉取代理列表。`, async () => {
    try { await api('/subscribe-tokens/'+id, { method:'DELETE' }); toast('订阅 Token 已删除','ok'); loadSubTokens(); }
    catch(e){ if(e.status===401){handle401();return;} toast('删除失败：'+e.message,'err'); }
  }, '删除');
}
