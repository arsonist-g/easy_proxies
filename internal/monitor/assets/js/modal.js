"use strict";
/* ===== 弹窗组件（P5）=====
 * 决策：所有弹窗统一「叉号 + ESC 关闭」，不响应遮罩点击（避免误关丢数据）。
 * - openModal 自动在右上角注入叉号按钮；ESC 由 app.js 全局监听关闭最上层。
 * - closeModal 清空全部弹窗（表单「取消」按钮用）；country-picker 嵌套时用 back.remove() 只关自身。
 * - opts.sticky 参数保留兼容（现统一不响应遮罩，参数无实际作用）。
 */
function openModal(html, opts={}){
  const root = document.getElementById('modal-root');
  if(!root) return null;
  const back = document.createElement('div');
  back.className = 'modal-backdrop';
  const box = document.createElement('div');
  box.className = 'modal' + (opts.extraWide?' extra-wide':(opts.wide?' wide':''));
  // 叉号：所有弹窗统一注入，点击关自身（openModal 弹窗不嵌套，等价 closeModal）
  box.innerHTML = `<button class="modal-x" type="button" aria-label="关闭">×</button>` + html;
  box.querySelector('.modal-x').onclick = () => back.remove();
  back.appendChild(box);
  root.appendChild(back);
  return back;
}
function closeModal(){ const root = document.getElementById('modal-root'); if(root) root.innerHTML = ''; }
function confirmModal(title, msg, onYes, yesLabel='确认'){
  openModal(`<h3>${esc(title)}</h3><div class="modal-sub">${esc(msg)}</div>
    <div class="form-actions">
      <button class="btn btn-secondary btn-sm" onclick="closeModal()">取消</button>
      <button class="btn btn-danger btn-sm" id="cf-yes">${esc(yesLabel)}</button>
    </div>`);
  const y = document.getElementById('cf-yes');
  if(y) y.onclick = () => { closeModal(); onYes(); };
}
