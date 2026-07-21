"use strict";
/* ===== 访问日志页（GET /access-logs，内存环形，倒序，支持 ip/result 过滤） ===== */
const alState = { page: 1, page_size: 50, ip: '', result: '' };

EP.registerPage('access_log', {
  init: async function () { await loadAccessLogs(); }
});

// 带日期的时间格式化（访问日志跨天，需显示月日）
function alTime(t) {
  if (!t) return '—';
  const d = new Date(t);
  if (isNaN(d)) return '—';
  return d.toLocaleString('zh-CN', { hour12: false, month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

async function loadAccessLogs() {
  const filterBar = document.getElementById('al-filter');
  if (filterBar && !filterBar.dataset.built) {
    filterBar.innerHTML = `
      <input class="input mono" id="al-ip" placeholder="按源 IP 过滤" style="flex:1;min-width:160px" value="${esc(alState.ip)}" />
      <select class="select" id="al-result">
        <option value="">全部结果</option>
        <option value="allow">仅放行</option>
        <option value="deny">仅拒绝</option>
      </select>
      <button class="btn btn-secondary btn-sm" id="al-apply">筛选</button>`;
    filterBar.dataset.built = '1';
    const resSel = document.getElementById('al-result');
    if (resSel) resSel.value = alState.result;
    document.getElementById('al-apply').onclick = () => {
      alState.page = 1;
      alState.ip = document.getElementById('al-ip').value.trim();
      alState.result = document.getElementById('al-result').value;
      loadAccessLogs();
    };
  }
  const tbl = document.getElementById('al-table');
  tbl.innerHTML = skeletonTable(['时间', '源IP', '结果', '原因', '省份', '运营商', '类型', '入口']);
  const q = new URLSearchParams();
  q.set('page', alState.page); q.set('page_size', alState.page_size);
  if (alState.ip) q.set('ip', alState.ip);
  if (alState.result) q.set('result', alState.result);
  let data;
  try { data = await api('/access-logs?' + q.toString()); }
  catch (e) { if (e.status === 401) { handle401(); return; } tbl.innerHTML = errBanner(e); return; }
  renderAccessLogs(data);
}

function renderAccessLogs(data) {
  const items = data.items || [];
  const tbl = document.getElementById('al-table');
  const sub = document.getElementById('al-sub');
  if (!items.length) {
    tbl.innerHTML = `<div class="state"><div class="ic-big">🔒</div><div class="title">暂无访问日志</div><div class="desc">${alState.ip || alState.result ? '无匹配记录' : '尚未记录任何访问（启用访问日志后生效，重启后清空）'}</div></div>`;
    document.getElementById('al-pagination').style.display = 'none';
    if (sub) sub.textContent = '谁在调用代理';
    return;
  }
  const rows = items.map(e => {
    const verdict = e.verdict === 'allow'
      ? '<span class="badge badge-ok">放行</span>'
      : '<span class="badge badge-err">拒绝</span>';
    return `<tr>
      <td class="mono" style="white-space:nowrap">${alTime(e.time)}</td>
      <td class="mono">${esc(e.src_ip || '—')}</td>
      <td>${verdict}</td>
      <td>${esc(e.reason || '—')}</td>
      <td>${esc(e.province || '—')}</td>
      <td>${esc(e.isp || '—')}</td>
      <td>${esc(e.net_type || '—')}</td>
      <td class="mono" style="font-size:11px">${esc(e.inbound || '—')}</td>
    </tr>`;
  }).join('');
  tbl.innerHTML = `<div class="table-wrap"><table class="tbl-fixed"><colgroup>
    <col style="width:13%"><col style="width:13%"><col style="width:7%"><col style="width:24%"><col style="width:9%"><col style="width:11%"><col style="width:8%"><col style="width:15%"></colgroup>
    <thead><tr><th>时间</th><th>源IP</th><th>结果</th><th>原因</th><th>省份</th><th>运营商</th><th>类型</th><th>入口</th></tr></thead>
    <tbody>${rows}</tbody></table></div>`;
  if (sub) sub.textContent = `${data.total} 条记录 · 已显示 ${items.length}`;
  renderPagination('al-pagination', data, (p) => { alState.page = p; loadAccessLogs(); });
}
