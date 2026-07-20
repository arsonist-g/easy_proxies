"use strict";
/* ===== Virtual Pools 页（虚拟池） =====
 * 含国家选择器（与 creds 页订阅 token 共用逻辑结构）。P5：选择器不响应遮罩点击，靠取消按钮/ESC 关。
 * external_ip 来自 EP.settings（app.js loadAppSettings 缓存）。
 */
let vpListState = { items:[], page:1, pageSize:20 };
let vpCountryField = null;

EP.registerPage('pools', {
  init: async function(){ await loadPools(); }
});

// 虚拟池入口链接：有认证带 user:pass@，无认证仅 http://ip:port
function vpEntryUrl(p){
  if(!EP.settings.external_ip || !p.port) return '';
  return p.username
    ? `http://${encodeURIComponent(p.username)}:${encodeURIComponent(p.password||'')}@${EP.settings.external_ip}:${p.port}`
    : `http://${EP.settings.external_ip}:${p.port}`;
}
async function loadPools(){
  const tbl = document.getElementById('pools-table');
  tbl.innerHTML = skeletonTable(['名称','正则','监听','策略','匹配节点','状态','']);
  await loadAppSettings(); // 取 external_ip 以拼接虚拟池入口链接
  let data;
  try { data = await api('/virtual-pools'); }
  catch(e){ if(e.status===401){handle401();return;} tbl.innerHTML=errBanner(e); return; }
  vpListState.items = Array.isArray(data) ? data : (data.items || []);
  vpListState.page = 1;
  const pg = document.getElementById('vp-pagination'); if(pg) pg.style.display='none';
  if(!vpListState.items.length){ tbl.innerHTML = `<div class="state"><div class="ic-big">▦</div><div class="title">尚无虚拟池</div><div class="desc">新建虚拟池以聚合节点为独立入口</div><button class="btn btn-primary btn-sm" onclick="poolCreateModal()">+ 新建虚拟池</button></div>`; return; }
  renderPoolsPage();
}
function renderPoolsPage(){
  const { items, page, pageSize } = vpListState;
  const total = items.length;
  const pages = Math.max(1, Math.ceil(total/pageSize));
  const p = Math.min(page, pages);
  const slice = items.slice((p-1)*pageSize, p*pageSize);
  const rows = slice.map(pl => {
    const cls = pl.running ? 'status-ok' : 'status-err';
    const entryUrl = vpEntryUrl(pl);
    return `<tr class="${cls}">
      <td style="font-weight:600">${esc(pl.name)}</td>
      <td class="mono">${esc(pl.regular||'—')}</td>
      <td>${poolCountriesCell(pl)}</td>
      <td class="mono">:${pl.port||'—'}</td>
      <td>${pl.strategy?`<span class="badge badge-neutral">${esc(strategyLabel(pl.strategy))}</span>`:'—'}</td>
      <td class="mono">${pl.node_count||0}</td>
      <td>${pl.running?'<span class="badge badge-ok">运行中</span>':'<span class="badge badge-err">已停止</span>'}</td>
      <td>
        <input type="hidden" id="pool-entry-${pl.id}" value="${esc(entryUrl)}" />
        <button class="btn btn-secondary btn-sm" onclick="poolViewNodes(${pl.id}, '${esc(pl.name).replace(/'/g,'')}')">查看节点</button>
        ${entryUrl ? `<button class="btn btn-secondary btn-sm" title="复制虚拟池入口链接" onclick="copyLink('pool-entry-${pl.id}','虚拟池入口链接已复制')">复制入口</button>` : ''}
        <button class="btn btn-secondary btn-sm" onclick="poolEditModal(${pl.id})">编辑</button>
        <button class="btn btn-danger btn-sm" onclick="poolDelete(${pl.id}, '${esc(pl.name).replace(/'/g,'')}')">删除</button>
      </td>
    </tr>`;
  }).join('');
  document.getElementById('pools-table').innerHTML = `<div class="table-wrap"><table>
    <thead><tr><th>名称</th><th>正则</th><th>国家</th><th>监听</th><th>策略</th><th>匹配节点</th><th>状态</th><th></th></tr></thead>
    <tbody>${rows}</tbody></table></div>`;
  renderPagination('vp-pagination', { total, page:p, page_size:pageSize }, (np) => { vpListState.page = np; renderPoolsPage(); });
}
// 虚拟池策略中文标签（value 仍存英文，保证 API/配置兼容）
const STRATEGY_OPTIONS = [
  {v:'sequential', l:'顺序轮询'},
  {v:'random',     l:'随机'},
  {v:'balance',    l:'最少连接'},
  {v:'weighted',   l:'智能加权（延迟+可用率）'},
];
function strategyLabel(s){ const o = STRATEGY_OPTIONS.find(x=>x.v===s); return o ? o.l : (s || '—'); }
// 虚拟池列表"国家"列：国旗 emoji + 国名；包含=绿、排除=红；多于 6 个截断 +N（hover 显示全部）
function poolCountriesCell(pl){
  const inc = pl.country_codes || [], exc = pl.excluded_country_codes || [];
  if(!inc.length && !exc.length) return '<span class="muted">全部国家</span>';
  const MAX = 6;
  const chip = (code, kind) => `<span class="vp-cc vp-cc-${kind}" title="${esc(countryName(code))}（${kind==='inc'?'包含':'排除'}）"><span class="flag">${flag(code)}</span><span class="vp-cc-n">${esc(countryName(code))}</span></span>`;
  const more = (list) => `<span class="vp-cc-more" title="${esc(list.map(countryName).join('、'))}">+${list.length-MAX}</span>`;
  let parts = inc.slice(0,MAX).map(c => chip(c,'inc'));
  if(inc.length > MAX) parts.push(more(inc));
  if(exc.length){
    parts.push('<span class="vp-cc-label">⊘ 排除</span>');
    parts = parts.concat(exc.slice(0,MAX).map(c => chip(c,'exc')));
    if(exc.length > MAX) parts.push(more(exc));
  }
  return `<div class="vp-cc-wrap">${parts.join('')}</div>`;
}

/* ===== 国家选择器 ===== */
let COUNTRY_MAP = null; // code -> {name, code, count}；null 表示未加载
// 完整国家/地区表（ISO 3166-1 alpha-2 + 中文名 + 大洲分区）。弹窗显示全部，count 由探测结果叠加
const COUNTRY_LIST = [
  // 东亚
  {c:'CN',n:'中国',r:'东亚'},{c:'TW',n:'台湾',r:'东亚'},{c:'HK',n:'香港',r:'东亚'},{c:'MO',n:'澳门',r:'东亚'},{c:'JP',n:'日本',r:'东亚'},{c:'KR',n:'韩国',r:'东亚'},{c:'KP',n:'朝鲜',r:'东亚'},{c:'MN',n:'蒙古',r:'东亚'},
  // 东南亚
  {c:'SG',n:'新加坡',r:'东南亚'},{c:'MY',n:'马来西亚',r:'东南亚'},{c:'TH',n:'泰国',r:'东南亚'},{c:'ID',n:'印度尼西亚',r:'东南亚'},{c:'VN',n:'越南',r:'东南亚'},{c:'PH',n:'菲律宾',r:'东南亚'},{c:'KH',n:'柬埔寨',r:'东南亚'},{c:'MM',n:'缅甸',r:'东南亚'},{c:'LA',n:'老挝',r:'东南亚'},{c:'BN',n:'文莱',r:'东南亚'},{c:'TL',n:'东帝汶',r:'东南亚'},
  // 南亚
  {c:'IN',n:'印度',r:'南亚'},{c:'PK',n:'巴基斯坦',r:'南亚'},{c:'BD',n:'孟加拉国',r:'南亚'},{c:'LK',n:'斯里兰卡',r:'南亚'},{c:'NP',n:'尼泊尔',r:'南亚'},{c:'BT',n:'不丹',r:'南亚'},{c:'MV',n:'马尔代夫',r:'南亚'},{c:'AF',n:'阿富汗',r:'南亚'},
  // 中亚
  {c:'KZ',n:'哈萨克斯坦',r:'中亚'},{c:'UZ',n:'乌兹别克斯坦',r:'中亚'},{c:'KG',n:'吉尔吉斯斯坦',r:'中亚'},{c:'TJ',n:'塔吉克斯坦',r:'中亚'},{c:'TM',n:'土库曼斯坦',r:'中亚'},
  // 中东
  {c:'AE',n:'阿联酋',r:'中东'},{c:'SA',n:'沙特阿拉伯',r:'中东'},{c:'IL',n:'以色列',r:'中东'},{c:'TR',n:'土耳其',r:'中东'},{c:'IR',n:'伊朗',r:'中东'},{c:'IQ',n:'伊拉克',r:'中东'},{c:'QA',n:'卡塔尔',r:'中东'},{c:'KW',n:'科威特',r:'中东'},{c:'OM',n:'阿曼',r:'中东'},{c:'BH',n:'巴林',r:'中东'},{c:'JO',n:'约旦',r:'中东'},{c:'LB',n:'黎巴嫩',r:'中东'},{c:'SY',n:'叙利亚',r:'中东'},{c:'YE',n:'也门',r:'中东'},{c:'PS',n:'巴勒斯坦',r:'中东'},
  // 西欧
  {c:'GB',n:'英国',r:'西欧'},{c:'FR',n:'法国',r:'西欧'},{c:'DE',n:'德国',r:'西欧'},{c:'NL',n:'荷兰',r:'西欧'},{c:'BE',n:'比利时',r:'西欧'},{c:'IE',n:'爱尔兰',r:'西欧'},{c:'LU',n:'卢森堡',r:'西欧'},{c:'MC',n:'摩纳哥',r:'西欧'},{c:'AT',n:'奥地利',r:'西欧'},{c:'CH',n:'瑞士',r:'西欧'},{c:'LI',n:'列支敦士登',r:'西欧'},
  // 北欧
  {c:'NO',n:'挪威',r:'北欧'},{c:'SE',n:'瑞典',r:'北欧'},{c:'FI',n:'芬兰',r:'北欧'},{c:'DK',n:'丹麦',r:'北欧'},{c:'IS',n:'冰岛',r:'北欧'},
  // 南欧
  {c:'IT',n:'意大利',r:'南欧'},{c:'ES',n:'西班牙',r:'南欧'},{c:'PT',n:'葡萄牙',r:'南欧'},{c:'GR',n:'希腊',r:'南欧'},{c:'MT',n:'马耳他',r:'南欧'},{c:'CY',n:'塞浦路斯',r:'南欧'},{c:'AD',n:'安道尔',r:'南欧'},{c:'SM',n:'圣马力诺',r:'南欧'},{c:'VA',n:'梵蒂冈',r:'南欧'},
  // 东欧
  {c:'RU',n:'俄罗斯',r:'东欧'},{c:'UA',n:'乌克兰',r:'东欧'},{c:'PL',n:'波兰',r:'东欧'},{c:'RO',n:'罗马尼亚',r:'东欧'},{c:'CZ',n:'捷克',r:'东欧'},{c:'HU',n:'匈牙利',r:'东欧'},{c:'BG',n:'保加利亚',r:'东欧'},{c:'SK',n:'斯洛伐克',r:'东欧'},{c:'LT',n:'立陶宛',r:'东欧'},{c:'LV',n:'拉脱维亚',r:'东欧'},{c:'EE',n:'爱沙尼亚',r:'东欧'},{c:'BY',n:'白俄罗斯',r:'东欧'},{c:'MD',n:'摩尔多瓦',r:'东欧'},{c:'AL',n:'阿尔巴尼亚',r:'东欧'},{c:'HR',n:'克罗地亚',r:'东欧'},{c:'RS',n:'塞尔维亚',r:'东欧'},{c:'SI',n:'斯洛文尼亚',r:'东欧'},{c:'BA',n:'波黑',r:'东欧'},{c:'MK',n:'北马其顿',r:'东欧'},{c:'ME',n:'黑山',r:'东欧'},{c:'GE',n:'格鲁吉亚',r:'东欧'},{c:'AM',n:'亚美尼亚',r:'东欧'},{c:'AZ',n:'阿塞拜疆',r:'东欧'},
  // 北美
  {c:'US',n:'美国',r:'北美'},{c:'CA',n:'加拿大',r:'北美'},
  // 中美/加勒比
  {c:'MX',n:'墨西哥',r:'中美'},{c:'GT',n:'危地马拉',r:'中美'},{c:'HN',n:'洪都拉斯',r:'中美'},{c:'SV',n:'萨尔瓦多',r:'中美'},{c:'NI',n:'尼加拉瓜',r:'中美'},{c:'CR',n:'哥斯达黎加',r:'中美'},{c:'PA',n:'巴拿马',r:'中美'},{c:'BZ',n:'伯利兹',r:'中美'},{c:'CU',n:'古巴',r:'中美'},{c:'DO',n:'多米尼加',r:'中美'},{c:'HT',n:'海地',r:'中美'},{c:'JM',n:'牙买加',r:'中美'},{c:'BS',n:'巴哈马',r:'中美'},{c:'TT',n:'特立尼达和多巴哥',r:'中美'},{c:'BB',n:'巴巴多斯',r:'中美'},{c:'GD',n:'格林纳达',r:'中美'},{c:'LC',n:'圣卢西亚',r:'中美'},{c:'DM',n:'多米尼克',r:'中美'},{c:'AG',n:'安提瓜和巴布达',r:'中美'},{c:'KN',n:'圣基茨和尼维斯',r:'中美'},{c:'VC',n:'圣文森特和格林纳丁斯',r:'中美'},{c:'PR',n:'波多黎各',r:'中美'},
  // 南美
  {c:'BR',n:'巴西',r:'南美'},{c:'AR',n:'阿根廷',r:'南美'},{c:'CL',n:'智利',r:'南美'},{c:'CO',n:'哥伦比亚',r:'南美'},{c:'PE',n:'秘鲁',r:'南美'},{c:'VE',n:'委内瑞拉',r:'南美'},{c:'EC',n:'厄瓜多尔',r:'南美'},{c:'BO',n:'玻利维亚',r:'南美'},{c:'PY',n:'巴拉圭',r:'南美'},{c:'UY',n:'乌拉圭',r:'南美'},{c:'GY',n:'圭亚那',r:'南美'},{c:'SR',n:'苏里南',r:'南美'},
  // 大洋洲
  {c:'AU',n:'澳大利亚',r:'大洋洲'},{c:'NZ',n:'新西兰',r:'大洋洲'},{c:'FJ',n:'斐济',r:'大洋洲'},{c:'PG',n:'巴布亚新几内亚',r:'大洋洲'},{c:'SB',n:'所罗门群岛',r:'大洋洲'},{c:'VU',n:'瓦努阿图',r:'大洋洲'},{c:'WS',n:'萨摩亚',r:'大洋洲'},{c:'TO',n:'汤加',r:'大洋洲'},{c:'KI',n:'基里巴斯',r:'大洋洲'},{c:'FM',n:'密克罗尼西亚',r:'大洋洲'},{c:'MH',n:'马绍尔群岛',r:'大洋洲'},{c:'NR',n:'瑙鲁',r:'大洋洲'},{c:'PW',n:'帕劳',r:'大洋洲'},{c:'TV',n:'图瓦卢',r:'大洋洲'},{c:'NU',n:'纽埃',r:'大洋洲'},{c:'CK',n:'库克群岛',r:'大洋洲'},
  // 北非
  {c:'EG',n:'埃及',r:'北非'},{c:'LY',n:'利比亚',r:'北非'},{c:'TN',n:'突尼斯',r:'北非'},{c:'DZ',n:'阿尔及利亚',r:'北非'},{c:'MA',n:'摩洛哥',r:'北非'},{c:'SD',n:'苏丹',r:'北非'},
  // 西非
  {c:'NG',n:'尼日利亚',r:'西非'},{c:'GH',n:'加纳',r:'西非'},{c:'CI',n:'科特迪瓦',r:'西非'},{c:'SN',n:'塞内加尔',r:'西非'},{c:'ML',n:'马里',r:'西非'},{c:'BF',n:'布基纳法索',r:'西非'},{c:'NE',n:'尼日尔',r:'西非'},{c:'GN',n:'几内亚',r:'西非'},{c:'SL',n:'塞拉利昂',r:'西非'},{c:'LR',n:'利比里亚',r:'西非'},{c:'GM',n:'冈比亚',r:'西非'},{c:'BJ',n:'贝宁',r:'西非'},{c:'TG',n:'多哥',r:'西非'},{c:'CV',n:'佛得角',r:'西非'},{c:'MR',n:'毛里塔尼亚',r:'西非'},{c:'GW',n:'几内亚比绍',r:'西非'},
  // 中非
  {c:'CD',n:'刚果(金)',r:'中非'},{c:'CG',n:'刚果(布)',r:'中非'},{c:'CF',n:'中非',r:'中非'},{c:'GA',n:'加蓬',r:'中非'},{c:'TD',n:'乍得',r:'中非'},{c:'GQ',n:'赤道几内亚',r:'中非'},{c:'ST',n:'圣多美和普林西比',r:'中非'},{c:'CM',n:'喀麦隆',r:'中非'},
  // 东非
  {c:'ET',n:'埃塞俄比亚',r:'东非'},{c:'KE',n:'肯尼亚',r:'东非'},{c:'TZ',n:'坦桑尼亚',r:'东非'},{c:'UG',n:'乌干达',r:'东非'},{c:'RW',n:'卢旺达',r:'东非'},{c:'BI',n:'布隆迪',r:'东非'},{c:'SO',n:'索马里',r:'东非'},{c:'DJ',n:'吉布提',r:'东非'},{c:'ER',n:'厄立特里亚',r:'东非'},{c:'MG',n:'马达加斯加',r:'东非'},{c:'MU',n:'毛里求斯',r:'东非'},{c:'SC',n:'塞舌尔',r:'东非'},{c:'KM',n:'科摩罗',r:'东非'},{c:'SS',n:'南苏丹',r:'东非'},
  // 南非
  {c:'ZA',n:'南非',r:'南非'},{c:'BW',n:'博茨瓦纳',r:'南非'},{c:'NA',n:'纳米比亚',r:'南非'},{c:'ZW',n:'津巴布韦',r:'南非'},{c:'ZM',n:'赞比亚',r:'南非'},{c:'MZ',n:'莫桑比克',r:'南非'},{c:'AO',n:'安哥拉',r:'南非'},{c:'SZ',n:'斯威士兰',r:'南非'},{c:'LS',n:'莱索托',r:'南非'},{c:'MW',n:'马拉维',r:'南非'}
];
const COUNTRY_NAME = {}; COUNTRY_LIST.forEach(c => COUNTRY_NAME[c.c] = c.n);
const REGION_ORDER = ['东亚','东南亚','南亚','中亚','中东','西欧','北欧','南欧','东欧','北美','中美','南美','大洋洲','北非','西非','中非','东非','南非'];
// 大洲聚合：18 个细分区域 → 6 大洲，用于弹窗顶部"已探测节点分布"汇总大卡片。
const CONTINENT_OF = {'东亚':'亚洲','东南亚':'亚洲','南亚':'亚洲','中亚':'亚洲','中东':'亚洲','西欧':'欧洲','北欧':'欧洲','南欧':'欧洲','东欧':'欧洲','北美':'北美','中美':'北美','南美':'南美','大洋洲':'大洋洲','北非':'非洲','西非':'非洲','中非':'非洲','东非':'非洲','南非':'非洲'};
const CONTINENT_ORDER = ['亚洲','欧洲','北美','南美','大洋洲','非洲'];
async function ensureCountryMap(){
  if(COUNTRY_MAP) return COUNTRY_MAP;
  try {
    const s = await api('/stats');
    const cd = s.country_distribution || [];
    COUNTRY_MAP = {};
    cd.forEach(c => { if(c.country_code) COUNTRY_MAP[c.country_code] = { name: c.country_name||c.country_code, code: c.country_code, count: c.count||0 }; });
  } catch(e){ COUNTRY_MAP = {}; }
  return COUNTRY_MAP;
}
function countryName(code){ return COUNTRY_NAME[code] || code; }
// 打开国家选择弹窗。opts: { included:[], excluded:[], allowExclude:bool, onApply(inc, exc) }。
// P5：不响应遮罩点击，靠「取消」按钮或 ESC（app.js 全局关最上层）关闭。
function openCountryPicker(opts){
  const included = new Set(opts.included || []);
  const excluded = new Set(opts.excluded || []);
  const allowExclude = opts.allowExclude !== false;
  let tab = 'include';
  const root = document.getElementById('modal-root');
  const back = document.createElement('div');
  back.className = 'modal-backdrop';
  const box = document.createElement('div');
  box.className = 'modal country-picker';
  box.innerHTML = `
    <button class="modal-x" type="button" aria-label="关闭">×</button>
    <h3>选择国家</h3>
    <div class="modal-sub">全部国家/地区 · 数字为已探测节点数（0 表示暂无，可预先配置）${allowExclude?' · 切到「排除」页可排除指定地区':''}</div>
    <div class="country-picker-search"><input class="input" id="cp-search" placeholder="搜索国家名或代码…" autocomplete="off" /></div>
    ${allowExclude ? `<div class="cp-tabs">
      <div class="cp-tab active" data-tab="include">包含 <span id="cp-inc-n">0</span></div>
      <div class="cp-tab" data-tab="exclude">排除 <span id="cp-exc-n">0</span></div>
    </div>` : ''}
    <div class="country-picker-body" id="cp-body"></div>
    <div class="form-actions">
      <span class="cp-count-label" id="cp-count"></span>
      <button class="btn btn-secondary btn-sm" id="cp-cancel">取消</button>
      <button class="btn btn-primary btn-sm" id="cp-apply">应用</button>
    </div>`;
  back.appendChild(box);
  root.appendChild(back);
  const close = () => back.remove();
  box.querySelector('.modal-x').onclick = close;
  document.getElementById('cp-cancel').onclick = close;
  const updateCount = () => {
    const n1 = document.getElementById('cp-inc-n');
    const n2 = document.getElementById('cp-exc-n');
    if(n1) n1.textContent = included.size;
    if(n2) n2.textContent = excluded.size;
    document.getElementById('cp-count').textContent = (included.size||excluded.size) ? ('包含 '+included.size+' · 排除 '+excluded.size) : '';
  };
  const render = (kw) => {
    const counts = COUNTRY_MAP || {};
    const k = (kw||'').trim().toLowerCase();
    let items = COUNTRY_LIST.map(c => ({ code:c.c, name:c.n, region:c.r, count:(counts[c.c] && counts[c.c].count) || 0 }));
    if(k) items = items.filter(c => c.name.toLowerCase().includes(k) || c.code.toLowerCase().includes(k));
    const itemHTML = (c) => {
      const inc = included.has(c.code), exc = excluded.has(c.code);
      const cls = inc?' inc':(exc?' exc':'');
      const mark = inc?'✓':(exc?'✕':'');
      return `<div class="cp-item${cls}" data-code="${esc(c.code)}"><span class="flag">${flag(c.code)}</span><span class="cp-name">${esc(c.name)}</span><span class="cp-count">${c.count}</span><span class="cp-mark">${mark}</span></div>`;
    };
    let overview = '';
    if(!k){
      const probed = items.filter(c => c.count > 0);
      const conts = {};
      probed.forEach(c => { const ct = CONTINENT_OF[c.region] || '其他'; (conts[ct] = conts[ct] || []).push(c); });
      Object.keys(conts).forEach(ct => conts[ct].sort((a,b) => (b.count - a.count) || a.name.localeCompare(b.name,'zh')));
      const order = CONTINENT_ORDER.filter(ct => conts[ct]);
      if(conts['其他']) order.push('其他');
      if(order.length){
        overview = '<div class="cp-overview"><div class="cp-overview-title">已探测节点分布 · 按大洲</div>' +
          order.map(ct => {
            const cs = conts[ct];
            const total = cs.reduce((s,c) => s + c.count, 0);
            return `<div class="cp-continent"><div class="cp-continent-head"><span class="cp-continent-name">${esc(ct)}</span><span class="cp-continent-total">${total} 节点 · ${cs.length} 地区</span></div><div class="cp-grid">${cs.map(itemHTML).join('')}</div></div>`;
          }).join('') + '</div>';
      }
    }
    const groups = {};
    items.forEach(c => { const r = c.region || '其他'; (groups[r] = groups[r] || []).push(c); });
    Object.keys(groups).forEach(r => groups[r].sort((a,b) => (b.count - a.count) || a.name.localeCompare(b.name,'zh')));
    const regions = REGION_ORDER.filter(r => groups[r]);
    if(groups['其他']) regions.push('其他');
    const body = document.getElementById('cp-body');
    const listHTML = regions.length ? regions.map(r =>
      `<div class="cp-region"><div class="cp-region-title">${esc(r)}</div><div class="cp-grid">${groups[r].map(itemHTML).join('')}</div></div>`
    ).join('') : '<div class="state" style="padding:16px"><div class="desc">无匹配国家</div></div>';
    body.innerHTML = overview + listHTML;
    body.querySelectorAll('.cp-item').forEach(el => {
      el.onclick = () => {
        const code = el.dataset.code;
        if(allowExclude && tab === 'exclude'){
          if(excluded.has(code)){ excluded.delete(code); }
          else { excluded.add(code); included.delete(code); }
        } else {
          if(included.has(code)){ included.delete(code); }
          else { included.add(code); excluded.delete(code); }
        }
        body.querySelectorAll('.cp-item[data-code="'+CSS.escape(code)+'"]').forEach(x => {
          const inc = included.has(code), exc = excluded.has(code);
          x.className = 'cp-item' + (inc?' inc':(exc?' exc':''));
          x.querySelector('.cp-mark').textContent = inc?'✓':(exc?'✕':'');
        });
        updateCount();
      };
    });
  };
  if(allowExclude){
    box.querySelectorAll('.cp-tab').forEach(t => {
      t.onclick = () => {
        tab = t.dataset.tab;
        box.querySelectorAll('.cp-tab').forEach(x => x.classList.remove('active','exc'));
        t.classList.add('active');
        if(tab === 'exclude') t.classList.add('exc');
      };
    });
  }
  document.getElementById('cp-search').oninput = (e) => render(e.target.value);
  document.getElementById('cp-apply').onclick = () => { opts.onApply([...included], [...excluded]); close(); };
  render('');
  updateCount();
  ensureCountryMap().then(() => render(document.getElementById('cp-search').value));
  document.getElementById('cp-search').focus();
}
// 通用国家字段（chip 展示 + 选择按钮）。opts.exclude 启用排除集合（虚拟池用）。
function makeCountryField(chipsId, opts={}){
  const exclude = !!opts.exclude;
  let included = [], excluded = [];
  const render = () => {
    const box = document.getElementById(chipsId);
    if(!box) return;
    const chips = [];
    included.forEach(c => chips.push(`<span class="chip chip-inc"><span class="flag">${flag(c)}</span><span>${esc(countryName(c))}</span><button type="button" class="chip-x" data-code="${esc(c)}" data-set="inc" title="移除">×</button></span>`));
    if(exclude) excluded.forEach(c => chips.push(`<span class="chip chip-exc"><span class="flag">${flag(c)}</span><span>${esc(countryName(c))}</span><button type="button" class="chip-x" data-code="${esc(c)}" data-set="exc" title="移除">×</button></span>`));
    box.innerHTML = chips.length ? chips.join('') : '<span class="chip-empty">未选择（匹配所有国家）</span>';
    box.querySelectorAll('.chip-x').forEach(b => b.onclick = () => {
      if(b.dataset.set === 'exc') excluded = excluded.filter(c => c !== b.dataset.code);
      else included = included.filter(c => c !== b.dataset.code);
      render();
    });
  };
  return {
    init(cfg){ cfg = cfg || {}; included = [...(cfg.country_codes||[])]; excluded = [...(cfg.excluded_country_codes||[])]; render(); ensureCountryMap().then(render); },
    pick(){ openCountryPicker({ included, excluded, allowExclude: exclude, onApply: (inc, exc) => { included=inc; excluded=exc; render(); } }); },
    get(){ const r = { country_codes: included }; if(exclude) r.excluded_country_codes = excluded; return r; }
  };
}
function poolFormBody(p){
  p = p || {};
  return `
    <div class="form-row"><label>名称</label><input class="input" id="vp-name" value="${esc(p.name||'')}" /></div>
    <div class="form-row"><label>正则（匹配节点名，可选）</label><input class="input mono" id="vp-regular" value="${esc(p.regular||'')}" placeholder="例：日本|JP" /></div>
    <div class="form-row"><label>国家码过滤（可选）</label>
      <div class="countries-box">
        <div class="chips" id="vp-countries-chips"></div>
        <button type="button" class="btn btn-secondary btn-sm" onclick="vpCountryField&&vpCountryField.pick()">选择国家</button>
      </div>
    </div>
    <div style="display:flex;gap:12px;align-items:flex-end">
      <div class="form-row" style="flex:1;min-width:0"><label>监听地址</label><input class="input mono" id="vp-address" value="${esc(p.address||'0.0.0.0')}" /></div>
      <div class="form-row" style="flex:0 0 auto"><label>端口</label>
        <div style="display:flex;gap:6px;align-items:center">
          <input class="input mono" id="vp-port" type="number" value="${p.port||''}" readonly placeholder="自动" style="width:88px" />
          <button type="button" class="btn btn-secondary btn-sm" onclick="allocVpPort()">分配</button>
        </div>
      </div>
    </div>
    <div style="display:flex;gap:12px">
      <div class="form-row" style="flex:1"><label>用户名（可选）</label><input class="input" id="vp-username" value="${esc(p.username||'')}" /></div>
      <div class="form-row" style="flex:1"><label>密码（可选）</label><input class="input mono" id="vp-password" value="${esc(p.password||'')}" /></div>
    </div>
    <div style="display:flex;gap:12px">
      <div class="form-row" style="flex:1"><label>策略</label><select class="select" id="vp-strategy" style="width:100%" onchange="toggleWeightRow()">
        ${STRATEGY_OPTIONS.map(o => `<option value="${o.v}" ${p.strategy===o.v?'selected':''}>${o.l}</option>`).join('')}
      </select></div>
      <div class="form-row" style="width:160px"><label>最大延迟 ms（可选）</label><input class="input mono" id="vp-maxlat" type="number" value="${p.max_latency_ms||''}" /></div>
    </div>
    <div class="form-row" id="vp-weight-row" style="display:${p.strategy==='weighted'?'flex':'none'};gap:12px;align-items:flex-end">
      <div style="flex:1"><label>延迟权重（必填，>0）</label><input class="input mono" id="vp-latw" type="number" step="0.1" min="0.1" value="${p.latency_weight||''}" placeholder="例：3" /></div>
      <div style="flex:1"><label>可用率权重（必填，>0）</label><input class="input mono" id="vp-availw" type="number" step="0.1" min="0.1" value="${p.availability_weight||''}" placeholder="例：7" /></div>
      <div style="font-size:11px;color:var(--text-tertiary);padding-bottom:6px">相对比例，内部归一化</div>
    </div>`;
}
function toggleWeightRow(){
  const sel = document.getElementById('vp-strategy');
  const row = document.getElementById('vp-weight-row');
  if(row) row.style.display = (sel && sel.value === 'weighted') ? 'flex' : 'none';
}
async function allocVpPort(){
  try { const r = await api('/virtual-pools/next-port'); document.getElementById('vp-port').value = r.port; }
  catch(e){ if(e.status===401){handle401();return;} toast('分配端口失败：'+e.message,'err'); }
}
function poolReadForm(){
  const port = parseInt(document.getElementById('vp-port').value, 10);
  const maxlat = parseInt(document.getElementById('vp-maxlat').value, 10);
  const cc = vpCountryField ? vpCountryField.get() : {};
  const body = {
    name: document.getElementById('vp-name').value.trim(),
    regular: document.getElementById('vp-regular').value.trim(),
    address: document.getElementById('vp-address').value.trim() || '0.0.0.0',
    port: port || 0,
    username: document.getElementById('vp-username').value.trim(),
    password: document.getElementById('vp-password').value,
    strategy: document.getElementById('vp-strategy').value,
    country_codes: cc.country_codes || [],
    excluded_country_codes: cc.excluded_country_codes || []
  };
  if(maxlat) body.max_latency_ms = maxlat;
  if(body.strategy === 'weighted'){
    const latw = parseFloat(document.getElementById('vp-latw').value);
    const availw = parseFloat(document.getElementById('vp-availw').value);
    if(!(latw > 0) || !(availw > 0)){ body._weightError = '智能加权需填写延迟权重和可用率权重（均 >0）'; }
    else { body.latency_weight = latw; body.availability_weight = availw; }
  }
  return body;
}
function poolCreateModal(){
  openModal(`<h3>新建虚拟池</h3><div class="modal-sub">按正则/国家聚合节点为独立负载均衡入口</div>${poolFormBody()}<div class="form-actions"><button class="btn btn-secondary btn-sm" onclick="closeModal()">取消</button><button class="btn btn-primary btn-sm" id="vp-save">创建</button></div>`, {wide:true});
  vpCountryField = makeCountryField('vp-countries-chips', {exclude:true}); vpCountryField.init({});
  document.getElementById('vp-save').onclick = async () => {
    const body = poolReadForm();
    if(!body.name){ toast('名称不能为空','warn'); return; }
    if(body._weightError){ toast(body._weightError,'warn'); return; }
    try { await api('/virtual-pools', { method:'POST', body }); toast('虚拟池已创建','ok'); closeModal(); loadPools(); }
    catch(e){ if(e.status===401){handle401();return;} toast('创建失败：'+e.message,'err'); }
  };
}
async function poolEditModal(id){
  let list; try { list = await api('/virtual-pools'); } catch(e){ if(e.status===401){handle401();return;} toast(e.message,'err'); return; }
  const items = Array.isArray(list)?list:(list.items||[]);
  const p = items.find(x => x.id === id);
  if(!p){ toast('虚拟池不存在','err'); return; }
  openModal(`<h3>编辑虚拟池</h3><div class="modal-sub">${esc(p.name)}</div>${poolFormBody(p)}<div class="form-actions"><button class="btn btn-secondary btn-sm" onclick="closeModal()">取消</button><button class="btn btn-primary btn-sm" id="vp-save">保存</button></div>`, {wide:true});
  vpCountryField = makeCountryField('vp-countries-chips', {exclude:true}); vpCountryField.init(p);
  document.getElementById('vp-save').onclick = async () => {
    const body = poolReadForm();
    if(body._weightError){ toast(body._weightError,'warn'); return; }
    try { await api('/virtual-pools/'+id, { method:'PATCH', body }); toast('虚拟池已更新','ok'); closeModal(); loadPools(); }
    catch(e){ if(e.status===401){handle401();return;} toast('保存失败：'+e.message,'err'); }
  };
}
let vpnState = { name:'', items:[], page:1, pageSize:20 };
async function poolViewNodes(id, name){
  let nodes; try { nodes = await api('/virtual-pools/'+id+'/nodes'); }
  catch(e){ if(e.status===401){handle401();return;} toast(e.message,'err'); return; }
  vpnState = { name, items: Array.isArray(nodes)?nodes:[], page:1, pageSize:20 };
  renderVpnModal();
}
function renderVpnModal(){
  const { name, items, page, pageSize } = vpnState;
  const total = items.length;
  const pages = Math.max(1, Math.ceil(total/pageSize));
  const p = Math.min(page, pages);
  const slice = items.slice((p-1)*pageSize, p*pageSize);
  const rows = slice.length ? slice.map(n => {
    const asn = n.asn_org ? `<span class="mono asn-cell" title="AS${n.asn||0} · ${esc(n.asn_org)}">${esc(n.asn_org)}</span>` : (n.asn ? `<span class="mono" title="AS${n.asn}">AS${n.asn}</span>` : '<span class="mono">—</span>');
    return `<tr class="${nodeRowClass(n)}">
      <td><div style="font-weight:600">${esc(n.name)}</div><div class="mono">${esc(n.mode||'')}</div></td>
      <td>${n.country_code?`<span class="country" title="${esc(n.country_code)}"><span class="flag">${flag(n.country_code)}</span><span class="code">${esc(n.country_name||n.country_code)}</span></span>`:'—'}</td>
      <td>${asn}</td>
      <td class="mono">${n.last_latency_ms>=0?n.last_latency_ms+' ms':'—'}</td>
      <td>${nodeStatusBadge(n)}</td></tr>`;
  }).join('') : '';
  openModal(`<h3>${esc(name)} · 匹配节点</h3><div class="modal-sub">${total} 个节点</div>
    ${total ? `<div class="table-wrap"><table><thead><tr><th>节点</th><th>国家</th><th>ASN</th><th>延迟</th><th>状态</th></tr></thead><tbody>${rows}</tbody></table></div>
    <div class="pagination" id="vpn-pagination" style="display:none"></div>` : stateInline('正则无匹配节点')}
    <div class="form-actions"><button class="btn btn-secondary btn-sm" onclick="closeModal()">关闭</button></div>`, {extraWide:true});
  if(total) renderPagination('vpn-pagination', { total, page:p, page_size:pageSize }, (np) => { vpnState.page = np; renderVpnModal(); });
}
function poolDelete(id, name){
  confirmModal('删除虚拟池', `确定删除虚拟池「${name}」？`, async () => {
    try { await api('/virtual-pools/'+id, { method:'DELETE' }); toast('虚拟池已删除','ok'); loadPools(); }
    catch(e){ if(e.status===401){handle401();return;} toast('删除失败：'+e.message,'err'); }
  }, '删除');
}
