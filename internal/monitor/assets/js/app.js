"use strict";
/* ===== Easy Proxies 公共脚本（每页都加载） =====
 * 方案 A + pjax：每页独立真 URL（/alerts），F5 真重载、可书签；
 *   导航则由 pjax 拦截——fetch 目标页 HTML → 只替换 <main>，侧边栏等公共壳不重绘，故不闪。
 * 职责：
 *   1. 注入公共壳（login overlay / toast / modal-root / sidebar nav）—— 单点维护
 *   2. 鉴权（checkAuth / doLogin / doLogout）+ 启动协议（EP.registerPage / runPageInit）
 *   3. pjax 局部刷新（onLinkClick / pjaxNavigate）—— 侧边栏持续存在
 *   4. 全局工具（api / toast / esc / flag / skeletonTable …）供各页 page.js 复用
 * 各 page.js 调 EP.registerPage('x', { init, cleanup }) 注册；cleanup 在切页时清定时器/状态。
 */
const API = '/api/v1';

/* ===== 全局命名空间 ===== */
window.EP = {
  page: document.body.dataset.page || 'index',   // 当前页 key（body[data-page]）；index 页无对应 nav
  pages: {},                  // 注册表 {name:{init,cleanup}}，各 page.js 调 EP.registerPage 注册
  currentPage: null,          // 运行时当前页（pjax cleanup 用）
  settings: { external_ip:'', proxy_username:'', proxy_password:'', mode:'', listener_port:0 }
};
EP.registerPage = function(name, fns){ EP.pages[name] = fns; };

/* ===== 工具函数 ===== */
function esc(s){ return String(s ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }
function flag(cc){ if(!cc || cc.length !== 2) return ''; try { return String.fromCodePoint(...[...cc.toUpperCase()].map(c => 0x1F1E6 + c.charCodeAt(0) - 65)); } catch(e){ return ''; } }
function short(s, n){ s = String(s ?? ''); return s.length > n ? s.slice(0, n) + '…' : s; }
function fmtTime(t){ if(!t) return '—'; const d = new Date(t); if(isNaN(d)) return '—'; return d.toLocaleTimeString('zh-CN', {hour12:false}); }
function fmtNum(n){ const v = Number(n||0); if(v >= 1000) return (v/1000).toFixed(1)+'k'; return String(v); }

/* ===== API 封装 ===== */
async function api(path, { method='GET', body } = {}){
  const opts = { method, credentials:'same-origin', headers:{} };
  if(body !== undefined){ opts.headers['Content-Type']='application/json'; opts.body = JSON.stringify(body); }
  let res;
  try { res = await fetch(API + path, opts); }
  catch(e){ const err = new Error('网络错误：'+e.message); err.status = 0; throw err; }
  const txt = await res.text();
  let data = null;
  if(txt){ try { data = JSON.parse(txt); } catch(e){} }
  if(!res.ok){
    const err = new Error((data && data.message) || ('HTTP '+res.status));
    err.status = res.status; err.code = data && data.code; err.body = data;
    throw err;
  }
  return data;
}

/* ===== Toast ===== */
function toast(msg, kind='ok'){
  const wrap = document.getElementById('toast-wrap');
  if(!wrap) return;
  const el = document.createElement('div');
  el.className = 'toast '+kind;
  el.textContent = msg;
  wrap.appendChild(el);
  setTimeout(() => { el.style.opacity='0'; el.style.transition='opacity .3s'; setTimeout(()=>el.remove(), 300); }, kind==='err'?5000:3000);
}

/* ===== 鉴权 ===== */
const session = { authed:false, hasPassword:false };
async function checkAuth(){
  try { const s = await api('/auth/status'); session.authed = true; session.hasPassword = s.has_password; return true; }
  catch(e){ if(e.status === 401){ session.authed = false; return false; } throw e; }
}
function showLogin(){ const o = document.getElementById('login-overlay'); if(o) o.style.display='flex'; }
function hideLogin(){ const o = document.getElementById('login-overlay'); if(o) o.style.display='none'; }
async function doLogin(){
  const pwd = document.getElementById('login-password').value;
  const btn = document.getElementById('login-btn');
  const errBox = document.getElementById('login-error');
  errBox.style.display='none'; btn.disabled=true; btn.textContent='登录中…';
  try {
    const r = await api('/auth/login', { method:'POST', body:{ password:pwd } });
    session.authed = true; session.hasPassword = !(r && r.no_password);
    hideLogin();
    await onAuthed();
  } catch(e){
    errBox.textContent = e.message || '登录失败'; errBox.style.display='flex';
  } finally { btn.disabled=false; btn.textContent='登录'; }
}
async function doLogout(){
  try { await api('/auth/logout', { method:'POST' }); } catch(e){}
  session.authed = false;
  const lb = document.getElementById('logout-btn'); if(lb) lb.style.display='none';
  // 退出到入口登录页（真跳转：相对路径自动继承 path_pwd 前缀）
  location.href = './';
}
function handle401(){ session.authed=false; showLogin(); toast('会话已失效，请重新登录','warn'); }

/* 鉴权通过后的统一入口：入口页跳 dashboard，功能页执行该页 init */
async function onAuthed(){
  const lb = document.getElementById('logout-btn');
  if(lb) lb.style.display = session.hasPassword ? 'inline-flex' : 'none';
  loadAppSettings(); // 后台预热，不阻塞首屏
  if(EP.page === 'index'){
    // 已登录访问入口 → 进 dashboard（真跳转；入口本就要整页进功能页）
    location.replace('dashboard');
    return;
  }
  await runPageInit(EP.page);
}

/* ===== appSettings（缓存 /settings，供复制代理链接拼 URL） ===== */
async function loadAppSettings(){
  try {
    const s = await api('/settings');
    EP.settings = { external_ip: s.external_ip||'', proxy_username: s.proxy_username||'', proxy_password: s.proxy_password||'', mode: s.mode||'', listener_port: s.listener_port||0 };
  } catch(e){ /* 静默降级：复制时按需重试 */ }
}

/* ===== 侧栏导航状态 ===== */
function updateNavStatus(avail, total, hasIssue){
  const txt = document.getElementById('nav-status-text');
  const dot = document.querySelector('#nav-status .dot');
  if(!txt) return;
  txt.textContent = `${avail} / ${total} 节点可用`;
  if(dot) dot.className = 'dot' + (hasIssue ? ' warn' : '');
}

/* ===== 公共渲染 ===== */
function skeletonTable(headers){
  const skel = `<div class="skeleton" style="margin:4px 0"></div>`;
  const rows = Array.from({length:5}).map(() => `<tr><td>${skel}</td>${headers.slice(1).map(()=>`<td>${skel}</td>`).join('')}</tr>`).join('');
  return `<div class="table-wrap"><table><thead><tr>${headers.map(h=>`<th>${h}</th>`).join('')}</tr></thead><tbody>${rows}</tbody></table></div>`;
}
function errBanner(e){
  // 重试按钮刷新当前页（F5 等价）
  return `<div class="error-banner">⚠ ${esc(e.message)} <button class="btn btn-secondary btn-sm" onclick="location.reload()">重试</button></div>`;
}
function stateInline(msg){ return `<div class="state" style="padding:20px"><div class="desc">${esc(msg)}</div></div>`; }

/* 跨页跳转（pjax 局部刷新，侧边栏不动）：go('subs') → /subs；go('settings','pool') → /settings#pool */
function go(page, anchor){
  if(!PJAX_PAGES.includes(page)) return;
  const base = new URL(page, location.href).href; // 相对路径，自动继承 path_pwd 前缀
  pjaxNavigate(base + (anchor ? '#' + anchor : ''), page, anchor || '', true);
}

/* ===== 侧栏 / mobile ===== */
function toggleSidebar(){ const s = document.getElementById('sidebar'); if(s) s.classList.toggle('open'); }
function closeSidebar(){ const s = document.getElementById('sidebar'); if(s) s.classList.remove('open'); }

/* ===== 公共壳注入（login overlay / toast / modal-root） ===== */
function ensureChrome(){
  if(!document.getElementById('toast-wrap')){
    const t = document.createElement('div'); t.className='toast-wrap'; t.id='toast-wrap'; document.body.insertBefore(t, document.body.firstChild);
  }
  if(!document.getElementById('modal-root')){
    const m = document.createElement('div'); m.id='modal-root'; document.body.appendChild(m);
  }
  if(!document.getElementById('login-overlay')){
    const o = document.createElement('div');
    o.className='login-overlay'; o.id='login-overlay'; o.style.display='none';
    o.innerHTML = `<div class="card login-card">
      <div class="brand"><div class="brand-logo">E</div><div class="brand-name">Easy Proxies</div></div>
      <div class="form-row"><label>管理密码</label><input type="password" class="input" id="login-password" placeholder="输入管理密码" autofocus /></div>
      <div id="login-error" class="error-banner" style="display:none"></div>
      <div class="form-actions"><button class="btn btn-primary" id="login-btn" style="width:100%; justify-content:center">登录</button></div>
    </div>`;
    document.body.appendChild(o);
    document.getElementById('login-btn').onclick = doLogin;
    document.getElementById('login-password').addEventListener('keydown', e => { if(e.key==='Enter') doLogin(); });
  }
}

/* ===== 侧栏 nav 注入（单点维护，避免 7 份 SVG 重复） ===== */
const NAV_ICONS = {
  dashboard: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/></svg>',
  nodes: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="3" width="20" height="6" rx="2"/><rect x="2" y="15" width="20" height="6" rx="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/></svg>',
  subs: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>',
  pools: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="12 2 2 7 12 12 22 7 12 2"/><polyline points="2 17 12 22 22 17"/><polyline points="2 12 12 17 22 12"/></svg>',
  creds: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3"/></svg>',
  alerts: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/></svg>',
  settings: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>',
  access_log: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="9" y1="13" x2="15" y2="13"/><line x1="9" y1="17" x2="15" y2="17"/></svg>'
};
const NAV_ITEMS = [
  { section:'监控' },
  { key:'dashboard', label:'节点概览' },
  { key:'nodes', label:'节点' },
  { section:'管理' },
  { key:'subs', label:'订阅' },
  { key:'pools', label:'虚拟池' },
  { key:'creds', label:'凭证' },
  { key:'alerts', label:'告警' },
  { key:'access_log', label:'访问日志' },
  { key:'settings', label:'设置' }
];
function renderSidebar(){
  const sb = document.getElementById('sidebar');
  if(!sb) return;
  const html = NAV_ITEMS.map(it => {
    if(it.section) return `<div class="nav-section">${it.section}</div>`;
    const active = it.key === EP.page ? ' active' : '';
    return `<a class="nav-item${active}" data-nav="${it.key}" href="${it.key}"><span class="ic">${NAV_ICONS[it.key]}</span> ${it.label}</a>`;
  }).join('');
  sb.innerHTML = `<div class="brand"><div class="brand-logo">E</div><div class="brand-name">Easy Proxies</div></div>
    ${html}
    <div class="nav-spacer"></div>
    <div class="nav-status" id="nav-status"><span class="dot"></span> <span id="nav-status-text">载入中…</span></div>
    <button class="btn btn-secondary btn-sm nav-logout" id="logout-btn" style="display:none">退出登录</button>`;
  const lb = document.getElementById('logout-btn');
  if(lb) lb.onclick = doLogout;
}

/* ===== 节点渲染公共函数（nodes.js / pools.js 共用） ===== */
function nodeRowClass(n){
  if(n.blacklisted) return 'status-black';
  if(!n.initial_check_done) return 'status-warn';
  if(!n.available) return 'status-err';
  if(n.last_latency_ms >= 300) return 'status-warn';
  return 'status-ok';
}
function nodeStatusBadge(n){
  if(n.blacklisted) return '<span class="badge badge-err">● 黑名单</span>';
  if(!n.initial_check_done) return '<span class="badge badge-neutral">● 待测</span>';
  if(!n.available) return '<span class="badge badge-err">● 不可用</span>';
  if(n.last_latency_ms >= 300) return '<span class="badge badge-warn">● 延迟高</span>';
  return '<span class="badge badge-ok">● 可用</span>';
}
// copyText 通用复制 + toast（msg 为完整 toast 文案，覆盖原 SPA 两个同名函数的歧义）
function copyText(text, msg){
  navigator.clipboard.writeText(text).then(
    () => toast(msg || '已复制到剪贴板', 'ok'),
    () => toast('复制失败，请手动选中复制', 'err')
  );
}
// copyLink 从隐藏 input 读 value（避开闭包/转义），虚拟池入口/订阅链接复制共用
function copyLink(elId, msg){
  const el = document.getElementById(elId);
  if(el) copyText(el.value, msg||'链接已复制');
}
// copyNodeProxy 拼节点端口代理链接 http://user:pwd@external_ip:port（点击时按需加载 settings）
async function copyNodeProxy(port){
  if(!EP.settings.external_ip) await loadAppSettings();
  const s = EP.settings;
  if(!s.external_ip){ toast('未获取到 external_ip，请稍后再试','warn'); return; }
  if(!s.proxy_username && !s.proxy_password){ toast('当前模式无统一代理凭证，无法生成链接','warn'); return; }
  // pool 模式节点共享 listener 端口（节点 port 是分配位次，非监听口）→ 用 listener_port；
  // port=0（未分配）同样回退 listener_port。hybrid/multi-port 用节点独立端口。
  const eff = (s.mode === 'pool' || !port) ? s.listener_port : port;
  if(!eff){ toast('未获取到监听端口，请稍后再试','warn'); return; }
  const url = `http://${encodeURIComponent(s.proxy_username)}:${encodeURIComponent(s.proxy_password)}@${s.external_ip}:${eff}`;
  copyText(url, '节点代理链接已复制');
}

/* ===== pjax 局部刷新（侧边栏等公共壳不重绘，只换 <main>） ===== */
const PJAX_PAGES = ['dashboard','nodes','subs','pools','creds','alerts','access_log','settings'];
const scriptLoaded = {}; // 已加载的 page.js（避免重复注入；首次随 HTML 加载的也登记）

// /alerts 或 /alerts.html（兼容旧书签）→ 'alerts'；非已知 nav 页返回 null（交给浏览器默认行为）
function pageKeyFromPath(pathname){
  const seg = pathname.replace(/\.html$/,'').replace(/\/+$/,'').split('/').pop();
  return PJAX_PAGES.includes(seg) ? seg : null;
}
function updateNavActive(page){
  document.querySelectorAll('.nav-item').forEach(a => {
    a.classList.toggle('active', a.dataset.nav === page);
  });
}
// 切页前清理旧页：调其 cleanup（清定时器/状态）+ 清残留模态。侧边栏保持不动。
function cleanupCurrentPage(){
  if(EP.currentPage && EP.pages[EP.currentPage]){
    try { const c = EP.pages[EP.currentPage].cleanup; if(typeof c === 'function') c(); } catch(e){}
  }
  const mr = document.getElementById('modal-root'); if(mr) mr.innerHTML = '';
}
async function runPageInit(name){
  EP.currentPage = name;
  const p = EP.pages[name];
  if(p && typeof p.init === 'function'){
    try { await p.init(); }
    catch(e){ if(e.status !== 401) toast(e.message, 'err'); }
  }
}
// 按需加载目标页 page.js（首次 pjax 到某页才注入其 script；已加载则跳过）
function ensurePageScript(key){
  if(scriptLoaded[key] || (EP.pages[key] && EP.pages[key].init)){ scriptLoaded[key] = true; return Promise.resolve(); }
  return new Promise(resolve => {
    const s = document.createElement('script');
    s.src = '/assets/js/pages/' + key + '.js';
    s.onload = () => { scriptLoaded[key] = true; resolve(); };
    s.onerror = () => resolve();
    document.body.appendChild(s);
  });
}
// 内部链接拦截：同源 + 已知 nav 页 + 无修饰键 → pjax；否则交给浏览器默认（外链/锚点/下载等）
function onLinkClick(e){
  if(e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  const a = e.target.closest ? e.target.closest('a') : null;
  if(!a || a.target === '_blank' || a.hasAttribute('download')) return;
  const href = a.getAttribute('href');
  if(!href || href.startsWith('#') || href.startsWith('javascript:')) return;
  let url; try { url = new URL(a.href, location.href); } catch(_){ return; }
  if(url.origin !== location.origin) return;
  const key = pageKeyFromPath(url.pathname);
  if(!key) return; // 非已知 nav 页（/api /assets /sub 等）→ 浏览器默认行为
  e.preventDefault();
  pjaxNavigate(url.href, key, url.hash, true);
}
function onPopState(){
  const key = pageKeyFromPath(location.pathname);
  if(key) pjaxNavigate(location.href, key, location.hash, false);
  else location.reload(); // 回退到非 pjax 页（入口/外站）→ 真 reload
}
// pjax 核心：fetch 目标页 HTML → 提取 main → cleanup 旧页 → 替换 main → 更新状态 → init 新页
async function pjaxNavigate(href, key, hash, push){
  if(key === EP.currentPage && !hash) return;
  let res;
  try { res = await fetch(href, { credentials:'same-origin', headers:{ 'X-PJAX':'1' } }); }
  catch(_){ location.href = href; return; }
  if(!res.ok){ location.href = href; return; }
  const doc = new DOMParser().parseFromString(await res.text(), 'text/html');
  const newMain = doc.querySelector('main.main');
  const newPage = doc.body && doc.body.dataset.page;
  if(!newMain || newPage !== key){ location.href = href; return; } // 异常 → 真 fallback
  // ① cleanup 旧页
  cleanupCurrentPage();
  // ② 替换 <main>（侧边栏 / toast / modal-root / login-overlay 都在 main 之外，不重绘 → 不闪）
  const cur = document.querySelector('main.main');
  cur.replaceWith(newMain);
  // ③ 状态：title / data-page / nav 高亮
  if(doc.title) document.title = doc.title;
  document.body.dataset.page = newPage;
  EP.page = newPage;
  updateNavActive(newPage);
  // ④ history（pushState；前进/后退走 onPopState，push=false）
  if(push !== false) history.pushState({ page:newPage }, '', href);
  // ⑤ 确保目标页 script 已加载 → init
  await ensurePageScript(newPage);
  await runPageInit(newPage);
  // ⑥ 滚动（hash 定位 / 否则回顶）
  if(hash){
    const t = document.querySelector(hash) || document.getElementById(hash.slice(1));
    if(t) t.scrollIntoView({ behavior:'smooth', block:'center' });
  } else {
    window.scrollTo(0, 0);
  }
}

/* ===== 启动 ===== */
document.addEventListener('DOMContentLoaded', async () => {
  ensureChrome();
  renderSidebar();
  scriptLoaded[EP.page] = true; // 初始页的 page.js 已随 HTML 加载
  // pjax：拦截内部链接 + 浏览器前进/后退
  document.addEventListener('click', onLinkClick);
  window.addEventListener('popstate', onPopState);
  // ESC 关闭最上层弹窗（不波及底层；country-picker 嵌套时只关自身）
  document.addEventListener('keydown', e => {
    if(e.key === 'Escape'){
      const backs = document.querySelectorAll('#modal-root > .modal-backdrop');
      if(backs.length) backs[backs.length-1].remove();
    }
  });
  let authed;
  try { authed = await checkAuth(); }
  catch(e){ authed = false; }
  if(authed){ hideLogin(); await onAuthed(); }
  else { showLogin(); }
});
