"use strict";
/* ===== 分页组件（P6）=====
 * 原 renderPagination 仅 ‹ › 两按钮，无法跳页/回首页。改为：
 * « 首页 · ‹ 上一页 · [窗口页码 1 2 3 …] · › 下一页 · » 末页 · 跳页输入
 * total <= page_size 时隐藏分页器（与原逻辑一致）。
 */
function renderPagination(elId, data, onPage){
  const el = document.getElementById(elId);
  if(!el) return;
  const total = data.total||0, page = data.page||1, size = data.page_size||20;
  const pages = Math.max(1, Math.ceil(total/size));
  if(total <= size){ el.style.display='none'; return; }
  el.style.display='flex';

  const btn = (p, label, title, disabled, primary) =>
    `<button class="btn ${primary?'btn-primary':'btn-secondary'} btn-sm" ${disabled?'disabled':''} data-p="${p}" ${title?`title="${title}"`:''}>${label}</button>`;

  // 页码窗口：当前页左右各 2 页，超界用省略号
  const win = 2;
  const start = Math.max(1, page-win), end = Math.min(pages, page+win);
  const nums = [];
  if(start > 1){ nums.push(`<span class="pg-ellipsis">…</span>`); }
  for(let i=start; i<=end; i++){ nums.push(btn(i, String(i), '', i===page, i===page)); }
  if(end < pages){ nums.push(`<span class="pg-ellipsis">…</span>`); }

  const jump = `<span class="pg-jump">跳至 <input class="input" type="number" min="1" max="${pages}" value="${page}" id="${elId}-jump" style="width:54px" /> 页 <button class="btn btn-secondary btn-sm" id="${elId}-go">Go</button></span>`;

  el.innerHTML = `<span>共 ${total} 条 · 第 ${page}/${pages} 页</span>
    <div class="pager">
      ${btn(1, '«', '首页', page<=1, false)}
      ${btn(page-1, '‹', '', page<=1, false)}
      ${nums.join('')}
      ${btn(page+1, '›', '', page>=pages, false)}
      ${btn(pages, '»', '末页', page>=pages, false)}
      ${jump}
    </div>`;

  el.querySelectorAll('button[data-p]').forEach(b => {
    b.onclick = () => { if(!b.disabled){ const p = parseInt(b.dataset.p,10); if(p>=1 && p<=pages) onPage(p); } };
  });
  const go = document.getElementById(elId+'-go');
  const jumpInput = document.getElementById(elId+'-jump');
  if(go && jumpInput){
    const doJump = () => { const v = parseInt(jumpInput.value, 10); if(v>=1 && v<=pages) onPage(v); };
    go.onclick = doJump;
    jumpInput.onkeydown = e => { if(e.key==='Enter') doJump(); };
  }
}
