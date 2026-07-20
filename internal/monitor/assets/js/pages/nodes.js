"use strict";
/* ===== Nodes 页（节点列表） =====
 * 筛选 / 排序 / 分页 / 单节点探测（P1 就地刷新失败态）。nodeRowClass/nodeStatusBadge/copyText 见 app.js。
 */
const nodesState = { page:1, page_size:20, country:'', protocol:'', available:false, duplicate:false, name_regex:'', sort:'latency', dir:'asc' };

EP.registerPage('nodes', {
  init: async function(){ await loadNodes(); }
});

async function loadNodes(){
  const filterBar = document.getElementById('nodes-filter');
  if(filterBar && !filterBar.dataset.built){
    filterBar.innerHTML = `
      <select class="select" id="f-country"><option value="">全部国家</option></select>
      <select class="select" id="f-protocol">
        <option value="">全部协议</option>
        <option value="vmess">vmess</option><option value="vless">vless</option>
        <option value="trojan">trojan</option><option value="ss">ss</option><option value="ssr">ssr</option>
        <option value="hysteria2">hysteria2</option>
      </select>
      <select class="select" id="f-status">
        <option value="">全部状态</option><option value="available">仅可用</option>
        <option value="duplicate">含重复</option>
      </select>
      <input class="input mono" id="f-regex" placeholder="name_regex 例: 日本|东京" style="flex:1;min-width:180px" />
      <button class="btn btn-secondary btn-sm" id="f-apply">筛选</button>`;
    filterBar.dataset.built = '1';
    document.getElementById('f-apply').onclick = () => {
      nodesState.page = 1;
      nodesState.country = document.getElementById('f-country').value;
      nodesState.protocol = document.getElementById('f-protocol').value;
      const st = document.getElementById('f-status').value;
      nodesState.available = (st === 'available');
      nodesState.duplicate = (st === 'duplicate');
      nodesState.name_regex = document.getElementById('f-regex').value.trim();
      loadNodes();
    };
  }
  const tbl = document.getElementById('nodes-table');
  tbl.innerHTML = skeletonTable(['节点','国家','ASN','协议','端口','延迟','可用率','状态','']);
  const q = new URLSearchParams();
  q.set('page', nodesState.page); q.set('page_size', nodesState.page_size);
  if(nodesState.country) q.set('country', nodesState.country);
  if(nodesState.protocol) q.set('protocol', nodesState.protocol);
  if(nodesState.available) q.set('available','true');
  if(nodesState.duplicate) q.set('duplicate','true');
  if(nodesState.name_regex) q.set('name_regex', nodesState.name_regex);
  q.set('sort', (nodesState.dir==='desc'?'-':'')+nodesState.sort);
  let data;
  try { data = await api('/nodes?'+q.toString()); }
  catch(e){
    if(e.status===401){ handle401(); return; }
    if(e.status===422){ tbl.innerHTML = `<div class="error-banner">⚠ 正则无效：${esc(e.message)} <button class="btn btn-secondary btn-sm" onclick="loadNodes()">重试</button></div>`; return; }
    tbl.innerHTML = errBanner(e); return;
  }
  renderNodes(data);
}
function renderNodes(data){
  const items = data.items || [];
  const countries = new Set();
  const cname = {}; // country_code → country_name（从已加载节点收集，下拉显示全名）
  items.forEach(n => { if(n.country_code){ countries.add(n.country_code); if(n.country_name) cname[n.country_code]=n.country_name; } });
  const sel = document.getElementById('f-country');
  if(sel){
    const known = new Set([...sel.options].map(o => o.value));
    [...countries].sort().forEach(c => { if(!known.has(c)){ const o=document.createElement('option'); o.value=c; o.textContent=flag(c)+' '+(cname[c]||c); sel.appendChild(o); } });
  }

  const tbl = document.getElementById('nodes-table');
  const filtered = nodesState.name_regex||nodesState.country||nodesState.protocol||nodesState.available||nodesState.duplicate;
  if(!items.length){
    tbl.innerHTML = `<div class="state"><div class="ic-big">≡</div><div class="title">无节点</div><div class="desc">${filtered?'无匹配节点，调整筛选':'尚无节点'}</div>${filtered?'<button class="btn btn-secondary btn-sm" onclick="clearNodeFilters()">清除筛选</button>':'<button class="btn btn-primary btn-sm" onclick="nodeCreateModal()">+ 添加节点</button>'}</div>`;
    document.getElementById('nodes-pagination').style.display='none';
    return;
  }
  const rows = items.map(renderNodeRow).join('');
  tbl.innerHTML = `<div class="table-wrap"><table class="tbl-fixed"><colgroup>
    <col style="width:19%"><col style="width:10%"><col style="width:11%"><col style="width:10%"><col style="width:7%"><col style="width:6%"><col style="width:8%"><col style="width:12%"><col style="width:7%"><col style="width:10%"></colgroup>
    <thead><tr>${thSort('name','节点')}${thSort('country','国家')}${thSort('exit_ip','实际 IP')}<th>ASN</th>${thSort('protocol','协议')}${thSort('port','端口')}${thSort('latency','延迟')}${thSort('availability','可用率')}<th>状态</th><th></th></tr></thead>
    <tbody>${rows}</tbody></table></div>`;
  document.getElementById('nodes-sub').textContent = `${data.total} 个节点 · 已显示 ${items.length}`;
  renderPagination('nodes-pagination', data, (p) => { nodesState.page = p; loadNodes(); });
}
// thSort 渲染可排序列头（▲/▼ 指示当前列与方向）；sortBy 处理点击：同列翻转方向、切列重置为升序、回到第 1 页。
function thSort(field, label){
  const active = nodesState.sort === field;
  const arrow = active ? (nodesState.dir==='asc'?' ▲':' ▼') : '';
  return `<th class="${active?'th-active':''}" style="cursor:pointer;user-select:none" onclick="sortBy('${field}')">${label}${arrow}</th>`;
}
function sortBy(field){
  if(nodesState.sort === field){ nodesState.dir = (nodesState.dir==='asc'?'desc':'asc'); }
  else { nodesState.sort = field; nodesState.dir = 'asc'; }
  nodesState.page = 1;
  loadNodes();
}
function clearNodeFilters(){
  nodesState.page=1; nodesState.country=''; nodesState.protocol=''; nodesState.name_regex=''; nodesState.available=false; nodesState.duplicate=false;
  const c=document.getElementById('f-country'); if(c) c.value='';
  const p=document.getElementById('f-protocol'); if(p) p.value='';
  const s=document.getElementById('f-status'); if(s) s.value='';
  const r=document.getElementById('f-regex'); if(r) r.value='';
  loadNodes();
}
// renderNodeRow 渲染单节点 <tr>（含 data-id）。renderNodes 全量渲染与 nodeProbe 就地刷新共用，
// 避免探测成功后 loadNodes() 全量重建导致节点跳位。
function renderNodeRow(n){
  const cls = nodeRowClass(n);
  const badge = nodeStatusBadge(n);
  const lat = n.last_latency_ms >= 0 ? n.last_latency_ms+' ms' : '—';
  const rate = Math.round((n.availability_rate||0)*100);
  const rateColor = rate>=90?'var(--success)':rate>=50?'var(--warning)':'var(--error)';
  // ASN：优先显示组织名（可读），无则显示 AS 号，均无则 —；hover 给完整 AS号·组织名
  const asnCell = n.asn_org ? `<span class="mono asn-cell" title="AS${n.asn||0} · ${esc(n.asn_org)}">${esc(n.asn_org)}</span>` : (n.asn ? `<span class="mono" title="AS${n.asn}">AS${n.asn}</span>` : '<span class="mono">—</span>');
  return `<tr class="${cls}" data-id="${esc(n.stable_id)}">
      <td><div class="node-name" style="font-weight:600" title="${esc(n.name)}">${esc(n.name)}</div><div class="mono">${esc(n.mode||'')} ${n.duplicate_of?'· <span style="color:var(--text-tertiary)">重复</span>':''}</div></td>
      <td>${n.country_code ? `<span class="country" title="${esc(n.country_code)}"><span class="flag">${flag(n.country_code)}</span><span class="code">${esc(n.country_name||n.country_code)}</span></span>` : '<span class="mono">—</span>'}</td>
      <td class="mono">${n.exit_ip ? `<span class="copyable-ip" title="${esc(n.exit_ip)} · 点击复制" onclick="copyText('${esc(n.exit_ip)}','节点 IP 已复制')">${esc(n.exit_ip)}</span>` : '<span class="mono">—</span>'}</td>
      <td>${asnCell}</td>
      <td>${n.mode?`<span class="badge badge-neutral">${esc(n.mode)}</span>`:'—'}</td>
      <td class="mono">${n.port||'—'}</td>
      <td class="mono">${lat}</td>
      <td><div class="avail"><div class="avail-bar"><div class="avail-fill" style="width:${Math.max(rate,2)}%;background:${rateColor}"></div></div><span class="avail-num">${rate}%</span></div></td>
      <td>${badge}</td>
      <td>
        <button class="btn btn-secondary btn-sm" onclick="nodeProbe('${esc(n.stable_id)}', this)">探测</button>
        <button class="btn btn-secondary btn-sm" title="复制节点代理链接" onclick="copyNodeProxy(${n.port||0})">复制</button>
        <button class="btn btn-danger btn-sm" data-name="${esc(n.name)}" onclick="nodeDelete('${esc(n.stable_id)}', this)">删除</button>
      </td>
    </tr>`;
}
async function nodeProbe(stableId, btn){
  const orig = btn.innerHTML; btn.disabled=true; btn.innerHTML='<span class="spin"></span>';
  try {
    const r = await api('/nodes/'+encodeURIComponent(stableId)+'/probe', { method:'POST' });
    // P1: 后端探测失败也返回 200 + success:false + 原因；前端据 success 显示成功延迟或失败原因
    if(r.success === false){
      toast('探测失败：'+(r.error||'未知原因'), 'err');
    } else {
      const country = r.node && r.node.country_name ? ' · ' + r.node.country_name : '';
      toast('探测成功 · '+r.latency_ms+' ms'+country, 'ok');
    }
    // 就地替换该行（保持列表位置，不重排）；后端响应 r.node 为探测后最新快照
    if(r.node){
      const tr = [...document.querySelectorAll('#nodes-table tr[data-id]')].find(t => t.dataset.id === stableId);
      if(tr) tr.outerHTML = renderNodeRow(r.node);
    }
  } catch(e){ if(e.status===401){ handle401(); return; } toast('探测失败：'+e.message, 'err'); }
  finally { btn.disabled=false; btn.innerHTML=orig; }
}
function nodeCreateModal(){
  openModal(`<h3>添加节点</h3><div class="modal-sub">手动添加单个代理节点</div>
    <div class="form-row"><label>名称</label><input class="input" id="nc-name" placeholder="例：日本-东京01" /></div>
    <div class="form-row"><label>URI</label><input class="input mono" id="nc-uri" placeholder="vmess://... / trojan://..." /></div>
    <div class="form-row"><label>用户名（可选）</label><input class="input" id="nc-username" /></div>
    <div class="form-row"><label>密码（可选）</label><input class="input mono" id="nc-password" /></div>
    <div class="form-actions">
      <button class="btn btn-secondary btn-sm" onclick="closeModal()">取消</button>
      <button class="btn btn-primary btn-sm" id="nc-save">添加</button>
    </div>`);
  document.getElementById('nc-save').onclick = async () => {
    const node = {
      name: document.getElementById('nc-name').value.trim(),
      uri: document.getElementById('nc-uri').value.trim(),
      username: document.getElementById('nc-username').value.trim(),
      password: document.getElementById('nc-password').value
    };
    if(!node.uri){ toast('URI 不能为空','warn'); return; }
    if(!node.name) node.name = node.uri.slice(0, 24);
    try {
      await api('/nodes', { method:'POST', body:node });
      toast('节点已添加','ok'); closeModal(); loadNodes();
    } catch(e){ if(e.status===401){handle401();return;} toast('添加失败：'+e.message,'err'); }
  };
}
function nodeDelete(stableId, btn){
  const name = btn ? btn.dataset.name : '';
  confirmModal('删除节点', `确定删除节点「${name}」？此操作不可撤销，且会触发配置重载。`, async () => {
    try { await api('/nodes/'+encodeURIComponent(stableId), { method:'DELETE' }); toast('节点已删除','ok'); loadNodes(); }
    catch(e){ if(e.status===401){handle401();return;} toast('删除失败：'+e.message,'err'); }
  }, '删除');
}
