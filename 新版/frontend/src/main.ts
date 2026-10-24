import './style.css';
import { api, errText } from './api';
import { closeModal, modalOpen, openClean, openFix, openRestore, openSettings } from './modals';
import { renderRoute } from './route';
import { mountUpdate, runUpdateCheck, updateBadge, updateLastText } from './update';
import type { Group, InitState, Report, Result, Status } from './types';
import { $, $$, esc, kindHint, kindLabel, statusLabel, toast } from './util';

type Tab = Group;
type Page = 'check' | 'update';

const state = {
  init: null as InitState | null,
  report: null as Report | null,
  checking: false,
  page: 'check' as Page,
  tab: 'overview' as Tab,
  expanded: new Set<string>(),
  touched: new Set<string>(), // 用户手动展开/收起过的项，不再自动改
  offline: false,
};

const TABS: { id: Tab; label: string; hint: string }[] = [
  { id: 'overview', label: '总览', hint: '需要处理的问题和出口' },
  { id: 'browser', label: '专用浏览器', hint: '网页版 Claude' },
  { id: 'cli', label: 'Claude Code', hint: '命令行 / VS Code' },
  { id: 'desktop', label: 'Claude Desktop', hint: '桌面客户端' },
  { id: 'system', label: '系统', hint: '时区、语言、IPv6' },
];

const PRIORITY = ['P1', 'P2', 'P4', 'B1', 'B2', 'B3', 'B4', 'B5', 'B7', 'D2', 'C2', 'C3', 'P3', 'B6', 'S1', 'S4', 'C4', 'B8', 'C6', 'D4', 'P5', 'C5', 'D3', 'S2', 'S3', 'S7', 'S6', 'S5'];
const rank = (s: Status) => ({ fail: 4, warn: 3, unknown: 2, pass: 1, na: 0 }[s] ?? 0);

// ---------------------------------------------------------------- 骨架

// 顶栏两个页签：体检（沿用 v2.0 的内容，阶段 2 再改版）、更新（internal/update）。「记录」页签阶段 2 加。
function shell() {
  document.querySelector('#app')!.innerHTML = `
  <div class="shell">
    <header class="topbar">
      <div class="tb-left">
        <svg class="mark" viewBox="0 0 32 32" aria-hidden="true"><path d="M16 2.5 4.5 7v8.2c0 7.1 4.8 12.4 11.5 14.3 6.7-1.9 11.5-7.2 11.5-14.3V7z" class="m-shield"/><path d="m10.5 16.2 3.8 3.8 7.4-8" class="m-tick"/></svg>
        <span class="name" title="" id="ver">Claude 环境卫士</span>
        <nav class="ptabs" role="tablist">
          <button class="ptab" role="tab" data-page="check">体检<span class="pbadge" id="badge-check"></span></button>
          <button class="ptab" role="tab" data-page="update">更新<span class="pbadge" id="badge-update"></span></button>
        </nav>
      </div>
      <div class="tb-right">
        <span class="last" id="last"></span>
        <button class="btn ghost small" id="btn-settings">⚙ 设置</button>
        <button class="btn ghost small more" id="btn-more" aria-label="更多" aria-haspopup="menu">⋯</button>
        <div class="menu" id="menu" role="menu" hidden>
          <button role="menuitem" data-m="offline"><span>离线检查</span><em>只看本机</em></button>
          <button role="menuitem" data-m="copy"><span>复制报告</span><em>发给别人</em></button>
          <button role="menuitem" data-m="restore"><span>还原修复…</span><em>撤销一键修复</em></button>
          <button role="menuitem" data-m="logs"><span>打开日志目录</span><em>保留 7 天</em></button>
        </div>
      </div>
    </header>
    <div class="page page-check" id="page-check">
      <section class="hero">
        <div class="verdict" id="verdict"></div>
        <div class="route-card"><div class="card-title">线路 <span>每个入口经自己的端口实测出口</span></div><div id="route"></div></div>
      </section>
      <section class="target-bar" id="target"></section>
      <main class="body">
        <nav class="rail" id="rail" role="tablist"></nav>
        <section class="panel" id="panel" role="tabpanel"></section>
      </main>
      <footer class="status-line" id="status"></footer>
    </div>
    <div class="page page-update" id="page-update" hidden></div>
  </div>`;
  $$('.ptab').forEach((b) => b.addEventListener('click', () => showPage(b.dataset.page as Page)));
  $('#btn-settings')!.addEventListener('click', () => state.init && openSettings(state.init, (s) => { state.init!.settings = s; showPage('check'); runCheck(); }));
  const menu = $('#menu')!;
  $('#btn-more')!.addEventListener('click', (e) => { e.stopPropagation(); menu.hidden = !menu.hidden; });
  document.addEventListener('click', (e) => { if (!menu.contains(e.target as Node)) menu.hidden = true; });
  $$('[data-m]', menu).forEach((b) => b.addEventListener('click', async () => {
    menu.hidden = true;
    switch (b.dataset.m) {
      case 'offline':
        showPage('check');
        runCheck(true);
        break;
      case 'copy': {
        const t = await api.reportText();
        if (!t) return toast('还没有完整的检查结果', 'err');
        await api.copy(t);
        toast('已复制文本报告（不含任何密钥）', 'ok');
        break;
      }
      case 'restore':
        openRestore(runCheck);
        break;
      case 'logs': {
        const err = state.init?.logDir ? await api.openPath(state.init.logDir) : '还不知道日志目录';
        if (err) toast('打不开日志目录：' + err, 'err');
        break;
      }
    }
  }));
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') { if (modalOpen()) closeModal(); menu.hidden = true; }
    if (e.key === 'F5') {
      e.preventDefault();
      if (modalOpen()) return;
      if (state.page === 'update') runUpdateCheck();
      else if (!state.checking) runCheck();
    }
  });
  mountUpdate($('#page-update')!, renderTabs);
}

function showPage(p: Page) {
  state.page = p;
  $('#page-check')!.hidden = p !== 'check';
  $('#page-update')!.hidden = p !== 'update';
  renderTabs();
}

function todoCount(): { n: number; fail: boolean } {
  const rep = state.report;
  if (!rep?.final) return { n: 0, fail: false };
  const todo = (rep.results ?? []).filter((r) => !r.advanced && !r.pending && (r.status === 'fail' || r.status === 'warn'));
  return { n: todo.length, fail: todo.some((r) => r.status === 'fail') };
}

function renderTabs() {
  $$('.ptab').forEach((b) => {
    const on = b.dataset.page === state.page;
    b.classList.toggle('on', on);
    b.setAttribute('aria-selected', String(on));
  });
  const t = todoCount();
  const bc = $('#badge-check')!;
  bc.textContent = t.n ? String(t.n) : '';
  bc.className = 'pbadge ' + (t.fail ? 'fail' : 'warn');
  const n = updateBadge();
  $('#badge-update')!.textContent = n ? String(n) : '';
  let last = '';
  if (state.page === 'update') last = updateLastText();
  else if (state.checking) last = '正在检查';
  else if (state.report?.final) {
    const at = new Date(state.report.startedAt);
    const hm = isNaN(at.getTime()) ? '' : at.toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' });
    last = `${hm} 检查 · ${(state.report.durationMs / 1000).toFixed(1)} 秒`;
  }
  $('#last')!.textContent = last;
}

// ---------------------------------------------------------------- 检查

let firstCheck = true;

async function runCheck(offline = false) {
  if (state.checking) return;
  state.checking = true;
  state.offline = offline;
  renderTop();
  try {
    const browser = state.report?.selectedBrowser ?? '';
    const rep = await api.check(offline, browser);
    applyReport(rep);
    api.log(`结果已显示：${rep.summary?.headline ?? ''}（${rep.results?.length ?? 0} 项）`);
  } catch (e) {
    toast('检查失败：' + errText(e), 'err');
  } finally {
    state.checking = false;
    render();
    // 打开卫士：体检完成后在后台查一次三端版本（需求文档第 3 节「打开卫士就查」）
    if (firstCheck) {
      firstCheck = false;
      runUpdateCheck();
    }
  }
}

function applyReport(rep: Report) {
  state.report = rep;
  for (const r of rep.results ?? []) {
    if (state.touched.has(r.id)) continue;
    if (r.status === 'fail' && !r.pending) state.expanded.add(r.id);
    else state.expanded.delete(r.id);
  }
  render();
}

// ---------------------------------------------------------------- 渲染

function render() {
  renderVerdict();
  renderTop();
  $('#route')!.innerHTML = renderRoute(state.report, ports());
  renderTarget();
  renderRail();
  renderPanel();
  renderStatus();
  renderTabs();
}

function ports(): Record<string, number> {
  const s = state.init?.settings;
  return { browser: s?.portBrowser ?? 43866, cli: s?.portCli ?? 43865, desktop: s?.portDesktop ?? 43867 };
}

function renderTop() {
  const b = $('#btn-check')!;
  b.classList.toggle('loading', state.checking);
  $('.lbl', b)!.textContent = state.checking ? '停止' : '重新检查';
  const n = state.report?.final ? state.report.summary.fixable : 0;
  $('#fix-count')!.textContent = n ? String(n) : '';
  $<HTMLButtonElement>('#btn-fix')!.disabled = state.checking || !state.report?.final;
  renderTabs();
}

function stampText(rep: Report | null) {
  if (!rep) return { cls: 'wait', big: '检查中', small: '正在读取本机配置' };
  if (!rep.final) return { cls: 'wait', big: '检查中', small: '本机项已出结果，正在联网测出口' };
  const o = rep.summary.overall;
  if (o === 'fail') return { cls: 'fail', big: '待处理', small: '' };
  if (o === 'warn') return { cls: 'warn', big: '可用', small: '' };
  if (o === 'unknown') return { cls: 'unknown', big: '待确认', small: '' };
  return { cls: 'pass', big: '合格', small: '' };
}

function renderVerdict() {
  const rep = state.report;
  const st = stampText(rep);
  const s = rep?.summary;
  const counters = s
    ? `<div class="counters">
        <span class="ct pass"><b>${s.pass}</b>通过</span><span class="ct warn"><b>${s.warn}</b>警告</span>
        <span class="ct fail"><b>${s.fail}</b>失败</span><span class="ct unknown"><b>${s.unknown}</b>未知</span>
        <span class="ct na"><b>${s.na}</b>不适用</span></div>`
    : '';
  const next = rep?.final && s?.nextStep
    ? `<div class="next"><span class="next-tag">下一步</span>${esc(s.nextStep)}</div>`
    : rep?.final ? '<div class="next calm">没有需要你动手的地方。换节点、更新 Claude 之后再来查一次。</div>' : '';
  const notes = (rep?.notes ?? []).map((n) => `<div class="note small">${esc(n)}</div>`).join('');
  $('#verdict')!.className = 'verdict s-' + st.cls;
  $('#verdict')!.innerHTML = `
    <div class="stamp ${st.cls}"><span>${st.big}</span></div>
    <div class="v-main">
      <div class="headline">${esc(rep?.final ? s!.headline : st.small)}</div>
      ${next}
      <div class="v-foot">${counters}
        <div class="v-actions">
          <button class="btn" id="btn-check"><span class="spinner"></span><span class="lbl">重新检查</span></button>
          <button class="btn primary" id="btn-fix">一键修复<span class="count" id="fix-count"></span></button>
        </div>
      </div>${notes}
    </div>`;
  // 原来在顶栏的两个按钮挪进结论卡片：顶栏改成页签后，它们只属于体检页
  $('#btn-check')!.addEventListener('click', () => (state.checking ? api.cancel() : runCheck()));
  $('#btn-fix')!.addEventListener('click', () => openFix(runCheck));
}

function renderTarget() {
  const t = state.report?.target;
  if (!t) {
    $('#target')!.innerHTML = '<span class="t-label">目标环境</span><span class="t-muted">等待出口检测…</span>';
    return;
  }
  const parts = t.source === 'none'
    ? `<span class="t-muted">${esc(t.sourceText)}</span>`
    : `<span class="t-item"><i>地区</i>${esc(t.country)} <span class="cc small">${esc(t.countryCode)}</span></span>
       <span class="t-item"><i>时区</i><span class="mono">${esc(t.iana)}</span></span>
       <span class="t-item"><i>语言</i><span class="mono">${esc(t.lang)}</span></span>
       <span class="t-src">${esc(t.sourceText)}</span>`;
  $('#target')!.innerHTML = `<span class="t-label" title="随 IP 的检查项按这里判断">目标环境</span>${parts}
    <button class="link" id="t-change">${t.source === 'manual' ? '改回跟随出口' : '手选地区'}</button>`;
  $('#t-change')!.addEventListener('click', () => state.init && openSettings(state.init, (s) => { state.init!.settings = s; runCheck(); }));
}

function groupResults(g: Group): Result[] {
  return (state.report?.results ?? []).filter((r) => r.group === g);
}

function badge(rs: Result[]) {
  const main = rs.filter((r) => !r.advanced && !r.pending);
  const bad = main.filter((r) => r.status === 'fail' || r.status === 'warn');
  if (rs.some((r) => r.pending)) return '<span class="tab-badge wait">…</span>';
  if (!bad.length) return main.some((r) => r.status === 'pass') ? '<span class="tab-badge pass">✓</span>' : '';
  const worst = bad.some((r) => r.status === 'fail') ? 'fail' : 'warn';
  return `<span class="tab-badge ${worst}">${bad.length}</span>`;
}

function renderRail() {
  const rail = $('#rail')!;
  rail.innerHTML = TABS.map((t) => {
    const rs = t.id === 'overview' ? groupResults('overview') : groupResults(t.id);
    return `<button role="tab" class="tab ${state.tab === t.id ? 'on' : ''}" data-tab="${t.id}" aria-selected="${state.tab === t.id}">
      <span class="tab-l"><span class="tab-name">${t.label}</span><span class="tab-hint">${t.hint}</span></span>${state.report ? badge(rs) : ''}</button>`;
  }).join('') + `<div class="rail-legend">
      <div><span class="chip kind-fixed">固定</span>谁都该这样配</div>
      <div><span class="chip kind-ip">随 IP</span>按出口所在地判断</div>
      <div><span class="chip kind-info">信息</span>只做参考</div></div>`;
  $$('.tab', rail).forEach((b) => b.addEventListener('click', () => {
    state.tab = b.dataset.tab as Tab;
    renderRail();
    renderPanel();
  }));
}

function rowHTML(r: Result, idx: number) {
  const open = state.expanded.has(r.id);
  const st = r.pending ? 'pending' : r.status;
  const detail = [
    r.target ? `<div class="kv"><i>目标</i><span>${esc(r.target)}</span></div>` : '',
    r.detail ? `<div class="kv"><i>${r.status === 'pass' ? '说明' : '原因'}</i><span>${esc(r.detail)}</span></div>` : '',
    r.advice && !r.fixId ? `<div class="kv"><i>怎么修</i><span>${esc(r.advice)}</span></div>` : '',
    r.fixId && (r.status === 'warn' || r.status === 'fail')
      ? `<div class="kv"><i>怎么修</i><span>${esc(r.advice || '可以一键修复。')}</span></div><div class="row-fix"><button class="btn primary small" data-fix>一键修复…</button></div>`
      : '',
  ].join('');
  return `<div class="row s-${st} ${open ? 'open' : ''} ${r.advanced ? 'adv' : ''}" data-id="${esc(r.id)}" style="--i:${idx}">
    <button class="row-head" aria-expanded="${open}">
      <span class="glyph" aria-label="${esc(statusLabel[r.status])}"></span>
      <span class="r-title">${esc(r.title)}<span class="rid">${esc(r.id)}</span>${r.advanced ? '<span class="chip opt">可选</span>' : ''}</span>
      <span class="chip kind-${r.kind}" title="${esc(kindHint[r.kind])}">${kindLabel[r.kind]}</span>
      <span class="r-cur mono" title="${esc(r.current ?? '')}">${esc(r.current ?? '')}</span>
      <span class="r-state">${r.pending ? '检测中' : statusLabel[r.status]}</span>
      <span class="chev" aria-hidden="true"></span>
    </button>
    <div class="row-detail"><div class="row-detail-in">${detail || '<div class="kv"><i>说明</i><span>没有更多信息。</span></div>'}</div></div>
  </div>`;
}

function toolbar(): string {
  const rep = state.report;
  if (state.tab === 'browser') {
    const list = rep?.browsers ?? [];
    const opts = list.map((b) => `<option value="${esc(b.name)}" ${b.name === rep?.selectedBrowser ? 'selected' : ''}>${esc(b.name)}${b.lastUsed ? '　最近使用 ' + esc(b.lastUsed) : ''}${b.standard ? '' : '（非标准目录）'}</option>`).join('');
    return `<div class="toolbar">
      ${list.length ? `<label class="sel"><span>检查哪个专用浏览器</span><select id="br-sel">${opts}</select></label>` : '<span class="t-muted">还没有专用浏览器</span>'}
      <span class="spacer"></span>
      ${list.length ? `<button class="btn ghost small" id="br-open" title="按桌面快捷方式里的完整防护参数打开">打开这个浏览器</button>
      <button class="btn ghost small" id="br-coffee" title="在这个专用浏览器里打开 ip.net.coffee/claude 人工复核">在浏览器里复核 IP</button>
      <button class="btn ghost small danger-text" id="br-clean">清理登录…</button>` : ''}
    </div>`;
  }
  if (state.tab === 'cli') return '<div class="toolbar"><span class="t-muted">Claude Code 读取 ~/.claude/settings.json，修改后要重启 Claude Code 才生效。</span><span class="spacer"></span><button class="btn ghost small danger-text" id="clean-code">清理登录…</button></div>';
  if (state.tab === 'desktop') return '<div class="toolbar"><span class="t-muted">Desktop 的代理来自 HKLM 策略，修改后要完全退出（含托盘）再打开。</span><span class="spacer"></span><button class="btn ghost small danger-text" id="clean-desktop">清理登录…</button></div>';
  if (state.tab === 'system') return '<div class="toolbar"><span class="t-muted">时区和区域格式「随 IP」：换了出口地区，这里的目标会跟着变。</span></div>';
  return '';
}

function renderPanel() {
  const panel = $('#panel')!;
  const rep = state.report;
  if (!rep) {
    panel.innerHTML = '<div class="skeleton">' + '<div class="sk-row"></div>'.repeat(6) + '</div>';
    return;
  }
  let html = toolbar();
  let i = 0;
  if (state.tab === 'overview') {
    const all = rep.results ?? [];
    const todo = all
      .filter((r) => !r.advanced && !r.pending && (r.status === 'fail' || r.status === 'warn' || r.status === 'unknown'))
      .sort((a, b) => rank(b.status) - rank(a.status) || PRIORITY.indexOf(a.id) - PRIORITY.indexOf(b.id));
    const optional = all.filter((r) => r.advanced && (r.status === 'fail' || r.status === 'warn'));
    html += `<h3 class="sec">需要处理 <span>${todo.length ? todo.length + ' 项' : ''}</span></h3>`;
    html += todo.length
      ? todo.map((r) => rowHTML(r, i++)).join('')
      : `<div class="all-good">${rep.final ? '必检项都没问题。' : '本机项没问题，联网项还在检测…'}</div>`;
    const todoIds = new Set(todo.map((r) => r.id));
    const rest = groupResults('overview').filter((r) => !todoIds.has(r.id));
    if (rest.length) html += `<h3 class="sec">出口与代理</h3>` + rest.map((r) => rowHTML(r, i++)).join('');
    if (optional.length) html += `<h3 class="sec">可选优化 <span>不影响使用，按需处理</span></h3>` + optional.map((r) => rowHTML(r, i++)).join('');
  } else {
    const rs = groupResults(state.tab);
    const main = rs.filter((r) => !r.advanced);
    const adv = rs.filter((r) => r.advanced);
    html += main.map((r) => rowHTML(r, i++)).join('');
    if (adv.length) html += `<h3 class="sec">进阶 / 可选</h3>` + adv.map((r) => rowHTML(r, i++)).join('');
  }
  panel.innerHTML = html;
  bindPanel(panel);
}

function bindPanel(panel: HTMLElement) {
  $$('.row-head', panel).forEach((b) => b.addEventListener('click', () => {
    const row = b.closest('.row') as HTMLElement;
    const id = row.dataset.id!;
    state.touched.add(id);
    if (state.expanded.has(id)) state.expanded.delete(id);
    else state.expanded.add(id);
    row.classList.toggle('open');
    b.setAttribute('aria-expanded', String(row.classList.contains('open')));
  }));
  $$('[data-fix]', panel).forEach((b) => b.addEventListener('click', () => openFix(runCheck)));
  $('#br-sel', panel)?.addEventListener('change', async (e) => {
    const name = (e.target as HTMLSelectElement).value;
    await api.selectBrowser(name);
    if (state.report) state.report.selectedBrowser = name;
    runCheck();
  });
  const selected = () => state.report?.selectedBrowser ?? '';
  $('#br-open', panel)?.addEventListener('click', async () => {
    const err = await api.launchBrowser(selected(), '');
    toast(err || '已按快捷方式参数打开「' + selected() + '」', err ? 'err' : 'ok');
  });
  $('#br-coffee', panel)?.addEventListener('click', async () => {
    const err = await api.launchBrowser(selected(), 'https://ip.net.coffee/claude/');
    toast(err || '已在「' + selected() + '」里打开 ip.net.coffee', err ? 'err' : 'ok');
  });
  $('#br-clean', panel)?.addEventListener('click', () => openClean('browser', runCheck));
  $('#clean-code', panel)?.addEventListener('click', () => openClean('code', runCheck));
  $('#clean-desktop', panel)?.addEventListener('click', () => openClean('desktop', runCheck));
}

function renderStatus() {
  const rep = state.report;
  const v = state.init?.version ? 'v' + state.init.version : '';
  $('#ver')!.title = `${v} · 检查 · 修复 · 还原 · 更新${state.init?.isAdmin ? ' · 管理员' : ''}`;
  if (!rep) {
    $('#status')!.innerHTML = `<span>${state.init?.note ? esc(state.init.note) : '启动检查…'}</span>`;
    return;
  }
  const t = new Date(rep.startedAt);
  const time = isNaN(t.getTime()) ? '' : t.toLocaleTimeString('zh-CN', { hour12: false });
  $('#status')!.innerHTML = `<span>${rep.final ? '上次检查' : '正在检查'} ${esc(time)}${rep.final ? ` · 用时 ${(rep.durationMs / 1000).toFixed(1)} 秒` : ''}${rep.offline ? ' · 离线模式' : ''}</span>
    <span class="spacer"></span><span>F5 重新检查 · 命令行版：ceg.exe check</span>`;
}

// ---------------------------------------------------------------- 启动

async function boot() {
  if (import.meta.env.DEV && !(window as any).go) await import('./mock');
  window.addEventListener('error', (e) => api.log('脚本错误：' + e.message + ' @' + e.filename + ':' + e.lineno));
  window.addEventListener('unhandledrejection', (e) => api.log('未处理的异步错误：' + errText(e.reason)));
  shell();
  render();
  api.onReport((partial) => {
    if (state.checking) applyReport(partial);
  });
  try {
    state.init = await api.init();
    if (state.init.note) toast(state.init.note, 'info');
  } catch (e) {
    toast('初始化失败：' + errText(e), 'err');
  }
  render();
  runCheck();
}

boot();
