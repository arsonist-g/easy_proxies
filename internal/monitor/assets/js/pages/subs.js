"use strict";
/* ===== Subscriptions 页（订阅） =====
 * P3 决策：去掉单订阅「刷新」按钮（逻辑上 RefreshOne 也是全量重启内核，单按钮反而误导）。
 *          只保留页顶「全量刷新」；全量刷新后轮询各订阅状态，直到无 running 或超时。
 */
const subsState = { page:1, page_size:20 };
let subsPollTimer = null;

EP.registerPage('subs', {
  init: async function(){
    await loadSubs();
    const btn = document.getElementById('subs-refresh-all');
    if(btn) btn.onclick = refreshAllSubs;
  },
  cleanup: function(){
    // 清订阅状态轮询，避免切页后仍在后台刷 /subscriptions
    if(subsPollTimer){ clearInterval(subsPollTimer); subsPollTimer = null; }
  }
});

async function loadSubs(){
  const tbl = document.getElementById('subs-table');
  tbl.innerHTML = skeletonTable(['名称','类型','节点数','最近刷新','状态','']);
  const q = new URLSearchParams({ page:subsState.page, page_size:subsState.page_size });
  let data;
  try { data = await api('/subscriptions?'+q.toString()); }
  catch(e){ if(e.status===401){handle401();return;} tbl.innerHTML=errBanner(e); return; }
  const items = data.items || [];
  document.getElementById('subs-sub').textContent = `${data.total!=null?data.total:items.length} 个订阅源`;
  if(!items.length){ tbl.innerHTML = `<div class="state"><div class="ic-big">⟳</div><div class="title">尚无订阅</div><div class="desc">添加订阅源以自动拉取节点</div><button class="btn btn-primary btn-sm" onclick="subCreateModal()">+ 添加订阅</button></div>`; return; }
  const rows = items.map(s => {
    const cls = s.last_refresh_status==='failed' ? 'status-err' : s.last_refresh_status==='running' ? 'status-warn' : 'status-ok';
    const badge = subStatusBadge(s);
    return `<tr class="${cls}">
      <td><div style="font-weight:600">${esc(s.name)}</div><div class="mono">${esc(short(s.url,40))}</div></td>
      <td>${s.type?`<span class="badge badge-neutral">${esc(s.type)}</span>`:'—'}</td>
      <td class="mono">${s.node_count||0}</td>
      <td class="mono">${fmtTime(s.last_refresh_at)}</td>
      <td>${badge}</td>
      <td>
        <button class="btn btn-secondary btn-sm" data-name="${esc(s.name)}" data-url="${esc(s.url)}" data-type="${esc(s.type||'')}" onclick="subEdit(${s.id}, this)">编辑</button>
        <button class="btn btn-danger btn-sm" data-name="${esc(s.name)}" onclick="subDelete(${s.id}, this)">删除</button>
      </td>
    </tr>`;
  }).join('');
  tbl.innerHTML = `<div class="table-wrap${data.total > subsState.page_size ? ' paginated' : ''}"><table>
    <thead><tr><th>名称</th><th>类型</th><th>节点数</th><th>最近刷新</th><th>状态</th><th></th></tr></thead>
    <tbody>${rows}</tbody></table></div>`;
  renderPagination('subs-pagination', data, (p) => { subsState.page = p; loadSubs(); });
}
function subStatusBadge(s){
  const st = s.last_refresh_status;
  if(st === 'success') return '<span class="badge badge-ok">success</span>';
  if(st === 'failed') return `<span class="badge badge-err">failed · ${esc(short(s.last_error||'错误',20))}</span>`;
  if(st === 'running') return '<span class="badge badge-neutral"><span class="spin"></span> running…</span>';
  return '<span class="badge badge-neutral">未刷新</span>';
}
async function refreshAllSubs(){
  const btn = document.getElementById('subs-refresh-all');
  const orig = btn ? btn.innerHTML : '';
  if(btn){ btn.disabled=true; btn.innerHTML='<span class="spin"></span> 刷新中'; }
  try {
    await api('/subscriptions/refresh', { method:'POST' });
    toast('全量刷新已触发','ok');
    pollSubsStatus();
  } catch(e){ if(e.status===401){handle401();return;} toast('刷新失败：'+e.message,'err'); }
  finally { if(btn){ btn.disabled=false; btn.innerHTML=orig; } }
}
// 全量刷新是异步逐个执行：轮询 /subscriptions 直到无 running 状态（或 30s 超时），实时展示各订阅 success/failed
function pollSubsStatus(){
  if(subsPollTimer) return;
  let count = 0;
  const tick = async () => {
    count++;
    await loadSubs();
    const stillRunning = Array.from(document.querySelectorAll('#subs-table .badge')).some(b => /running/.test(b.textContent));
    if(!stillRunning || count > 20){
      clearInterval(subsPollTimer); subsPollTimer=null;
    }
  };
  tick();
  subsPollTimer = setInterval(tick, 1500);
}
function subCreateModal(){
  openModal(`<h3>添加订阅</h3><div class="modal-sub">从订阅 URL 自动拉取节点</div>
    <div class="form-row"><label>名称</label><input class="input" id="sc-name" placeholder="例：机场A" /></div>
    <div class="form-row"><label>URL</label><input class="input mono" id="sc-url" placeholder="https://example.com/sub" /></div>
    <div class="form-row"><label>类型（可选）</label><select class="input" id="sc-type"><option value="">自动检测</option><option value="base64">base64</option><option value="clash">clash</option><option value="plain">plain</option><option value="singbox">singbox</option></select><div class="form-hint">留空则自动识别</div></div>
    <div class="form-actions"><button class="btn btn-secondary btn-sm" onclick="closeModal()">取消</button><button class="btn btn-primary btn-sm" id="sc-save">添加</button></div>`);
  document.getElementById('sc-save').onclick = async () => {
    const body = {
      name: document.getElementById('sc-name').value.trim(),
      url: document.getElementById('sc-url').value.trim(),
      type: document.getElementById('sc-type').value.trim()
    };
    if(!body.url){ toast('URL 不能为空','warn'); return; }
    if(!body.name) body.name = short(body.url, 20);
    try { await api('/subscriptions', { method:'POST', body }); toast('订阅已添加，正在拉取…','ok'); closeModal(); pollSubsStatus(); }
    catch(e){ if(e.status===401){handle401();return;} toast('添加失败：'+e.message,'err'); }
  };
}
function subDelete(id, btn){
  const name = btn ? btn.dataset.name : '';
  confirmModal('删除订阅', `确定删除订阅「${name}」？\n其来源的节点将被一并移除。`, async () => {
    try { await api('/subscriptions/'+id, { method:'DELETE' }); toast('订阅已删除','ok'); loadSubs(); }
    catch(e){ if(e.status===401){handle401();return;} toast('删除失败：'+e.message,'err'); }
  }, '删除');
}
function subEdit(id, btn){
  const name = btn.dataset.name||'', url = btn.dataset.url||'', type = btn.dataset.type||'';
  openModal(`<h3>编辑订阅</h3><div class="modal-sub">修改后需手动刷新以重新拉取节点</div>
    <div class="form-row"><label>名称</label><input class="input" id="se-name" value="${esc(name)}" /></div>
    <div class="form-row"><label>URL</label><input class="input mono" id="se-url" value="${esc(url)}" /></div>
    <div class="form-row"><label>类型（可选）</label><select class="input" id="se-type"><option value=""${type===''?' selected':''}>自动检测</option><option value="base64"${type==='base64'?' selected':''}>base64</option><option value="clash"${type==='clash'?' selected':''}>clash</option><option value="plain"${type==='plain'?' selected':''}>plain</option><option value="singbox"${type==='singbox'?' selected':''}>singbox</option></select><div class="form-hint">留空则自动识别</div></div>
    <div class="form-actions"><button class="btn btn-secondary btn-sm" onclick="closeModal()">取消</button><button class="btn btn-primary btn-sm" id="se-save">保存</button></div>`);
  document.getElementById('se-save').onclick = async () => {
    const body = {
      name: document.getElementById('se-name').value.trim(),
      url: document.getElementById('se-url').value.trim(),
      type: document.getElementById('se-type').value.trim()
    };
    if(!body.url){ toast('URL 不能为空','warn'); return; }
    try { await api('/subscriptions/'+id, { method:'PATCH', body }); toast('订阅已更新','ok'); closeModal(); loadSubs(); }
    catch(e){ if(e.status===401){handle401();return;} toast('更新失败：'+e.message,'err'); }
  };
}
