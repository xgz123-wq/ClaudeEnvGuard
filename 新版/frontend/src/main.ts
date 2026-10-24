import './style.css';
import './pages.css';
import { api, errText } from './api';
import { checkBadge, checkLastText, isChecking, lastReport, mountCheck, runCheck } from './check';
import { ctx, type Page } from './ctx';
import { mountRecords, recordsBadge, recordsLastText, recordsShown } from './records';
import { mountSettings, showSettings } from './settings';
import { mountUpdate, runUpdateCheck, updateBadge, updateLastText } from './update';
import { $, $$, toast } from './util';

// ---------------------------------------------------------------- 骨架

// 顶栏「体检 / 更新 / 记录」三个页签（带角标）；右边上次检查时间、⚙ 设置（单独一页）、⋯。
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
          <button class="ptab" role="tab" data-page="records">记录<span class="pbadge" id="badge-records"></span></button>
        </nav>
      </div>
      <div class="tb-right">
        <span class="last" id="last"></span>
        <button class="btn ghost small gear" id="btn-settings">⚙ 设置</button>
        <button class="btn ghost small more" id="btn-more" aria-label="更多" aria-haspopup="menu">⋯</button>
        <div class="menu" id="menu" role="menu" hidden>
          <button role="menuitem" data-m="offline"><span>离线检查</span><em>只看本机</em></button>
          <button role="menuitem" data-m="copy"><span>复制报告</span><em>发给别人</em></button>
          <button role="menuitem" data-m="logs"><span>打开日志目录</span><em>保留 7 天</em></button>
        </div>
      </div>
    </header>
    <div class="page page-check" id="page-check"></div>
    <div class="page page-update" id="page-update" hidden></div>
    <div class="page page-records" id="page-records" hidden></div>
    <div class="page page-settings" id="page-settings" hidden></div>
  </div>`;
  $$('.ptab').forEach((b) => b.addEventListener('click', () => showPage(b.dataset.page as Page)));
  $('#btn-settings')!.addEventListener('click', () => showPage(ctx.page === 'settings' ? 'check' : 'settings'));
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
      case 'logs': {
        const dir = ctx.init?.paths?.logs || ctx.init?.logDir;
        const err = dir ? await api.openPath(dir) : '还不知道日志目录';
        if (err) toast('打不开日志目录：' + err, 'err');
        break;
      }
    }
  }));
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      menu.hidden = true;
      if (ctx.page === 'settings') showPage('check');
    }
    if (e.key === 'F5') {
      e.preventDefault();
      if (ctx.page === 'update') runUpdateCheck();
      else if (ctx.page === 'check' && !isChecking()) runCheck();
    }
  });
  mountCheck($('#page-check')!, () => runUpdateCheck());
  mountUpdate($('#page-update')!, renderTabs);
  mountRecords($('#page-records')!);
  mountSettings($('#page-settings')!, () => (lastReport()?.browsers ?? []).map((b) => b.name));
}

function showPage(p: Page) {
  const from = ctx.page;
  ctx.page = p;
  for (const id of ['check', 'update', 'records', 'settings'] as Page[]) $('#page-' + id)!.hidden = id !== p;
  if (p === 'settings') showSettings(from);
  if (p === 'records') recordsShown();
  renderTabs();
}

function renderTabs() {
  $$('.ptab').forEach((b) => {
    const on = b.dataset.page === ctx.page;
    b.classList.toggle('on', on);
    b.setAttribute('aria-selected', String(on));
  });
  $('#btn-settings')!.classList.toggle('on', ctx.page === 'settings');
  const t = checkBadge();
  const bc = $('#badge-check')!;
  bc.textContent = t.n ? String(t.n) : '';
  bc.className = 'pbadge ' + (t.fail ? 'fail' : 'warn');
  const n = updateBadge();
  $('#badge-update')!.textContent = n ? String(n) : '';
  const r = recordsBadge();
  $('#badge-records')!.textContent = r ? String(r) : '';
  let last = '';
  if (ctx.page === 'update') last = updateLastText();
  else if (ctx.page === 'records') last = recordsLastText();
  else if (ctx.page === 'check') last = checkLastText();
  $('#last')!.textContent = last;
  const v = ctx.init?.version ? 'v' + ctx.init.version : '';
  $('#ver')!.title = `${v} · 检查 · 修复 · 还原 · 更新${ctx.init?.isAdmin ? ' · 管理员' : ''}`;
}

// ---------------------------------------------------------------- 启动

async function boot() {
  if (import.meta.env.DEV && !(window as any).go) await import('./mock');
  window.addEventListener('error', (e) => api.log('脚本错误：' + e.message + ' @' + e.filename + ':' + e.lineno));
  window.addEventListener('unhandledrejection', (e) => api.log('未处理的异步错误：' + errText(e.reason)));
  ctx.showPage = showPage;
  ctx.renderTabs = renderTabs;
  try {
    ctx.init = await api.init();
    if (ctx.init.note) toast(ctx.init.note, 'info');
  } catch (e) {
    toast('初始化失败：' + errText(e), 'err');
  }
  shell();
  renderTabs();
  runCheck();
}

boot();
