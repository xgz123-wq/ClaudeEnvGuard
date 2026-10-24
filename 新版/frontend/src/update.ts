// 更新页：照原型做「状态条 → 三张产品卡 → 横向 5 步 → 步骤细节 + 最近几次」，一屏放下。
// 状态全部来自 Go 端 internal/update.View（它只读三端更新脚本写的记录文件）；界面只按 phase 选样式、照字段显示。
import './update.css';
import { api, errText } from './api';
import type { UBlocker, UPhase, UProduct, UStep, UpdateView } from './types';
import { $, $$, esc, toast } from './util';

const PHASES = ['检查', '下载与安装 CLI', '授权', '安装与核验', '完成'];

const state = {
  view: null as UpdateView | null,
  pinned: null as number | null, // 用户点选的那一步；null 表示跟着当前步骤走
  checkStart: 0, // 查版本开始的时刻，给转圈配秒表
  busy: false, // 按下主按钮到后端返回之间，按钮保持灰色
  timer: 0,
};

let root: HTMLElement | null = null;
let onChange: () => void = () => {};

// ---------------------------------------------------------------- 对外

export function mountUpdate(el: HTMLElement, changed: () => void) {
  root = el;
  onChange = changed;
  api.onUpdate((v) => setView(v));
  api.updateView().then(setView).catch(() => {});
}

export const updateView = () => state.view;

/** 后台查一次版本（打开卫士、体检完成后自动调用）。 */
export async function runUpdateCheck() {
  const ph = state.view?.phase;
  if (ph === 'checking' || ph === 'running' || state.busy) return;
  state.checkStart = Date.now();
  setView({ ...(state.view ?? blank()), phase: 'checking', title: '正在查三端版本…', line: '三个版本源同时查，约 3 秒', hint: '', tier: undefined, blockers: null });
  startTicker();
  try {
    setView(await api.updateCheck());
  } catch (e) {
    setView({ ...blank(), phase: 'checkfail', tier: 'retry', title: '没查到新版本', line: '调用失败：' + errText(e), hint: '可以重新检查' });
  }
}

/** 顶栏角标：有几端可以更新（查版本中和刚更新完不显示）。 */
export function updateBadge(): number {
  const v = state.view;
  if (!v || !['update', 'blocked'].includes(v.phase)) return 0;
  return (v.products ?? []).filter((p) => p.behind).length;
}

/** 顶栏右侧的「上次检查」文字。 */
export function updateLastText(): string {
  const v = state.view;
  if (!v) return '';
  if (v.phase === 'checking') return '正在查版本';
  if (v.phase === 'running') return '正在更新';
  if (v.checkedAt) return `${v.checkedAt} 查过 · ${v.checkSecs?.toFixed(1) ?? '—'} 秒`;
  return '';
}

// ---------------------------------------------------------------- 状态

function blank(): UpdateView {
  return { phase: 'never', title: '', line: '', hint: '', products: null, steps: null, needsUac: false, elapsed: 0, remaining: 0, percent: 0, estimate: 0, history: null, script: '' };
}

function setView(v: UpdateView) {
  const prev = state.view?.phase;
  state.view = v;
  if (prev !== v.phase) state.pinned = null; // 换了阶段，细节跟着当前步骤走
  if (v.phase !== 'checking' && v.phase !== 'running') stopTicker();
  render();
  onChange();
}

// 查版本时后端不推进度，界面自己每秒刷新一次「已用 N 秒」。
function startTicker() {
  stopTicker();
  state.timer = window.setInterval(() => render(), 1000);
}
function stopTicker() {
  window.clearInterval(state.timer);
  state.timer = 0;
}

// ---------------------------------------------------------------- 格式

const secs = (n: number) => (n >= 90 ? `${Math.round(n / 60)} 分钟` : `${Math.round(n)} 秒`);
const about = (n: number) => '约 ' + secs(n);
const steps = (v: UpdateView): UStep[] => v.steps ?? [];
const started = (p: UPhase) => p === 'running' || p === 'done' || p === 'failed';

type Tone = 'pass' | 'warn' | 'fail' | 'info';

// ---------------------------------------------------------------- 主按钮

interface Btn { label: string; run?: () => void; disabled?: boolean }

function heroOf(v: UpdateView): { tone: Tone; icon: string; spin: boolean; main: Btn; second?: Btn; line: string } {
  const recheck: Btn = { label: '重新检查', run: () => runUpdateCheck() };
  const copyAI: Btn = { label: '复制给 AI 排查', run: () => copyForAI(v) };
  switch (v.phase) {
    case 'never':
      return { tone: 'info', icon: '?', spin: false, main: { label: '现在检查', run: () => runUpdateCheck() }, line: v.line || '打开卫士、体检完成后会自动查一次' };
    case 'checking': {
      const used = state.checkStart ? Math.floor((Date.now() - state.checkStart) / 1000) : 0;
      return { tone: 'info', icon: '✻', spin: true, main: { label: '检查中…', disabled: true }, line: `${v.line}${used ? ' · 已用 ' + used + ' 秒' : ''}` };
    }
    case 'latest':
      return { tone: 'pass', icon: '✓', spin: false, main: recheck, line: v.line };
    case 'update':
      return { tone: 'warn', icon: '!', spin: false, main: { label: '一键更新', run: start }, line: v.line };
    case 'blocked':
      return { tone: 'warn', icon: '!', spin: false, main: { label: '都做好了，重新检查', run: () => runUpdateCheck() }, line: v.line };
    case 'unsafe':
      return { tone: 'fail', icon: '×', spin: false, main: copyAI, second: recheck, line: v.line };
    case 'checkfail':
      return v.tier === 'retry'
        ? { tone: 'warn', icon: '↻', spin: false, main: recheck, line: v.line }
        : { tone: 'fail', icon: '×', spin: false, main: copyAI, second: recheck, line: v.line };
    case 'running':
      return { tone: 'info', icon: '✻', spin: true, main: { label: '更新中…', disabled: true }, line: v.line };
    case 'done':
      return { tone: 'pass', icon: '✓', spin: false, main: recheck, line: v.line };
    case 'failed':
      return v.tier === 'retry'
        ? { tone: 'warn', icon: '↻', spin: false, main: { label: '重试这一步', run: retry }, second: v.runDir ? { label: '打开日志', run: () => open(v.runDir!) } : undefined, line: v.line }
        : { tone: 'fail', icon: '×', spin: false, main: copyAI, second: v.runDir ? { label: '打开日志', run: () => open(v.runDir!) } : recheck, line: v.line };
  }
  return { tone: 'info', icon: '?', spin: false, main: recheck, line: v.line };
}

async function act(fn: () => Promise<UpdateView>) {
  if (state.busy) return;
  state.busy = true;
  render();
  try {
    setView(await fn());
  } catch (e) {
    toast('调用失败：' + errText(e), 'err');
  } finally {
    state.busy = false;
    render();
  }
}

function start() {
  void act(async () => {
    const v = await api.updateStart();
    if (v.phase !== 'running') toast('没有开始：' + (v.title || '状态已变化，先重新检查'), 'err');
    return v;
  });
}

function retry() {
  state.checkStart = Date.now();
  void act(() => api.updateRetry());
}

async function copyForAI(v: UpdateView) {
  const text = v.aiText || `环境卫士「更新」页出错。先别改任何东西，列出可能原因和验证方式。\n原因原文：${v.error ?? v.line}`;
  await api.copy(text);
  toast('已复制：出错步骤、原因原文、记录目录 + 「先别改，列可能原因和验证方式」', 'ok');
}

async function open(p: string) {
  const err = await api.openPath(p);
  if (err) toast('打不开：' + err, 'err');
}

// ---------------------------------------------------------------- 产品卡

function productCard(v: UpdateView, p: UProduct) {
  const ph = v.phase;
  let pill = p.behind ? '可更新' : '最新';
  let tone: Tone | 'na' = p.behind ? 'warn' : 'pass';
  let from = p.installed || '—';
  let showTo = p.behind;
  let cls = '';
  if (p.key === 'plugin' && p.behind && ph === 'blocked') { pill = '要在 VS Code 点更新'; tone = 'info'; }
  if (p.key === 'plugin' && !p.behind && (v.targets ?? []).some((t) => t.state === 'moved')) pill = '已在 VS Code 更新';
  if (ph === 'running' && p.behind) { pill = '更新中'; tone = 'info'; }
  if (ph === 'done' && p.behind) {
    pill = '已更新'; tone = 'pass'; showTo = false; cls = 'pop';
    if (p.installed === p.latest) from = p.latest;
  }
  if (ph === 'failed' && p.behind) {
    const id = p.key === 'cli' ? 'cli' : p.key === 'desktop' ? 'install' : '';
    const ok = steps(v).some((s) => s.id === id && s.state === 'done');
    pill = ok ? '已更新' : '没更新'; tone = ok ? 'pass' : 'fail';
  }
  if (ph === 'checking') { pill = '检查中…'; tone = 'info'; showTo = false; }
  else if (!p.installed) { pill = ph === 'checkfail' ? '没查到' : '还没查'; tone = 'na'; showTo = false; }
  return `<div class="u-prod ${cls} ${cls ? 'ok' : ''}">
    <div class="u-prod-top"><span class="u-prod-name">${esc(p.name)}</span><span class="pill ${tone}">${esc(pill)}</span></div>
    <div class="u-prod-ver mono"><span>${esc(from)}</span>${showTo ? `<span class="arrow">→</span><b>${esc(p.latest)}</b>` : ''}</div>
  </div>`;
}

const PLACEHOLDER: UProduct[] = [
  { key: 'desktop', name: 'Claude Desktop', installed: '', latest: '', behind: false },
  { key: 'cli', name: 'Claude Code CLI', installed: '', latest: '', behind: false },
  { key: 'plugin', name: 'VS Code 插件', installed: '', latest: '', behind: false },
];

// ---------------------------------------------------------------- 横向 5 步

type PState = 'wait' | 'run' | 'done' | 'fail' | 'warn' | 'none';

function phaseStates(v: UpdateView): { s: PState; time: string }[] {
  const all = steps(v);
  const ph = v.phase;
  return PHASES.map((_, i) => {
    if (i === 4) {
      if (ph === 'done') return { s: 'done', time: '共 ' + secs(v.elapsed) };
      if (ph === 'latest') return { s: 'none', time: '' };
      return { s: 'wait', time: '' };
    }
    const xs = all.filter((x) => x.phase === i);
    const est = xs.reduce((a, x) => a + x.est, 0);
    if (i === 0) {
      if (ph === 'never') return { s: 'wait', time: '约 3 秒' };
      if (ph === 'checking') {
        const used = state.checkStart ? Math.floor((Date.now() - state.checkStart) / 1000) : 0;
        return { s: 'run', time: used + ' 秒…' };
      }
      if (ph === 'blocked') return { s: 'warn', time: '要先处理' };
      if (ph === 'unsafe') return { s: 'fail', time: '停在这里' };
      if (ph === 'checkfail') return { s: v.tier === 'retry' ? 'warn' : 'fail', time: v.tier === 'retry' ? '可重试' : '停在这里' };
    }
    if (ph === 'latest') return { s: i === 0 ? 'done' : 'none', time: i === 0 ? secs(xs[0]?.secs ?? v.checkSecs ?? 0) : '这次不用' };
    if (!all.length) return { s: 'wait', time: '' }; // 还没查出计划（没查过、查版本中、查版本失败）
    if (!xs.length) return { s: 'none', time: '这次不用' };
    if (!started(ph)) {
      if (i === 0) return { s: 'done', time: secs(xs.reduce((a, x) => a + x.secs, 0)) };
      return { s: 'wait', time: about(est) };
    }
    const sum = xs.reduce((a, x) => a + x.secs, 0);
    if (xs.some((x) => x.state === 'fail')) return v.tier === 'retry' ? { s: 'warn', time: '可重试' } : { s: 'fail', time: '停在这里' };
    if (xs.some((x) => x.state === 'run')) return { s: 'run', time: secs(sum) + '…' };
    if (xs.every((x) => x.state === 'skip')) return { s: 'wait', time: ph === 'failed' ? '没执行' : '跳过' };
    if (xs.every((x) => x.state === 'done' || x.state === 'skip')) return { s: 'done', time: secs(sum) };
    if (xs.some((x) => x.state === 'done')) return { s: 'run', time: secs(sum) + '…' };
    if (ph === 'failed') return { s: 'wait', time: '没执行' };
    return { s: 'wait', time: about(est) };
  });
}

/** 当前该看哪一步：用户点过的优先，其次是正在跑 / 停住的那一步。 */
function activePhase(v: UpdateView, ps: { s: PState }[]): number {
  if (v.phase === 'done') return 4;
  const i = ps.findIndex((p) => p.s === 'run' || p.s === 'fail' || p.s === 'warn');
  if (i >= 0) return i;
  if (v.phase === 'update') return ps.findIndex((p, k) => k > 0 && p.s === 'wait');
  return 0;
}

/** 进度线画到哪：按 5 个圆点的位置算（当前这一步按已用 / 预计插值），线头和圆点对得上。 */
function trackFill(v: UpdateView, active: number): number {
  if (v.phase === 'done') return 100;
  if (!started(v.phase) || active < 0) return 0;
  const xs = steps(v).filter((s) => s.phase === active);
  const est = xs.reduce((a, s) => a + s.est, 0);
  const used = xs.reduce((a, s) => a + (s.state === 'done' || s.state === 'run' ? Math.min(s.secs, s.est) : 0), 0);
  const within = v.phase === 'running' && est > 0 ? Math.min(0.95, used / est) : 0;
  return Math.min(100, ((active + within) / 4) * 100);
}

function flowHint(v: UpdateView, active: number): string {
  switch (v.phase) {
    case 'running': return `第 ${active + 1} 步 / 共 5 步`;
    case 'done': return '5 步全部完成';
    case 'failed': return `停在第 ${active + 1} 步`;
    case 'latest': return '这次只走了第 1 步';
    case 'blocked': return '第 1 步发现要你先处理的事';
    case 'update': return `更新会走这 5 步 · 预计 ${secs(v.estimate)}`;
  }
  return '更新会走这 5 步';
}

// ---------------------------------------------------------------- 细节

function stepRow(s: UStep, v: UpdateView): string {
  const mark = { done: '✓', run: '✻', wait: '·', fail: '×', skip: '·' }[s.state];
  let t = about(s.est);
  if (s.state === 'done') t = secs(s.secs);
  if (s.state === 'run') t = secs(s.secs) + '…';
  if (s.state === 'fail') t = v.tier === 'retry' ? '中断' : '失败';
  if (s.state === 'skip') t = v.phase === 'failed' ? '没执行' : '跳过';
  if (v.phase === 'checking' && s.phase === 0) t = '…';
  let detail = s.detail ?? '';
  let bar = '';
  if (s.id === 'download' && v.download && (s.state === 'run' || s.state === 'fail')) {
    const d = v.download;
    detail = `${Math.round(d.mb)} / 约 ${Math.round(d.totalMb)} MB` + (s.state === 'fail' ? ' · 网络中断' : d.speed ? ` · ${d.speed.toFixed(1)} MB/s` : '');
    if (s.state === 'run') bar = `<div class="u-bar"><i class="stripe" style="width:${Math.min(100, (d.mb / Math.max(d.totalMb, 1)) * 100).toFixed(1)}%"></i></div>`;
  }
  return `<div class="u-row s-${s.state}">
    <span class="u-mark ${s.state === 'run' ? 'spin' : ''}">${mark}</span>
    <span class="u-name">${esc(s.name)}</span>${s.uac ? '<span class="tag uac">UAC 内</span>' : ''}
    <span class="u-det" title="${esc(detail)}">${esc(detail)}</span>
    <span class="u-secs mono">${esc(t)}</span>
  </div>${bar}`;
}

function plainRow(text: string, st: 'done' | 'wait' = 'wait') {
  return `<div class="u-row s-${st}"><span class="u-mark">${st === 'done' ? '✓' : '·'}</span><span class="u-name wrap">${esc(text)}</span></div>`;
}

function blockerRow(b: UBlocker) {
  const link = b.link ? '<button class="link" data-plugin>打开插件页</button>' : '';
  return `<div class="u-blocker"><span class="u-bang">!</span>
    <div class="u-bl-main"><span>${esc(b.title)}</span><span class="u-sub">${esc(b.detail)}</span></div>${link}</div>`;
}

function tiersHTML(v: UpdateView) {
  const t = (on: boolean, title: string, text: string) => `<div class="u-tier ${on ? 'on' : ''}"><b>${title}</b><span>${text}</span></div>`;
  return `<div class="u-tiers">
    ${t(v.tier === 'retry', '界面自己重试', '网络断、下载不完整、超时 → 「重试这一步」')}
    ${t(v.tier === 'user', '要你动手', 'Claude 在运行、插件要手点、UAC 点了否 → 待办')}
    ${t(v.tier === 'ai', '交给 AI 看', '签名不对、防护核验不过、没见过的错 → 「复制给 AI」')}
  </div><div class="u-err" title="${esc(v.error ?? '')}">原因原文：${esc(v.error ?? v.line)}</div>`;
}

function targetsRow(v: UpdateView) {
  const ts = v.targets ?? [];
  if (!ts.length) return '';
  const changed = ts.filter((t) => t.state !== 'unchanged');
  const word: Record<string, string> = { changed_signed: '变了（有 Anthropic 签名）', changed_unsigned: '变了但没有有效签名', moved: '插件换了目录', missing: '不存在' };
  const text = changed.length ? changed.map((t) => `${t.name}：${word[t.state] ?? t.state}`).join('；') : '4 个都没变';
  const bad = changed.some((t) => t.state === 'changed_unsigned' || t.state === 'missing');
  return `<div class="u-row s-${bad ? 'fail' : 'done'}"><span class="u-mark">${bad ? '×' : '✓'}</span><span class="u-name">受保护程序指纹</span><span class="u-det" title="${esc(text)}">${esc(text)}</span><span class="u-secs mono"></span></div>`;
}

function detailHTML(v: UpdateView, shown: number, ps: { s: PState }[]): { title: string; hint: string; body: string } {
  const ph = v.phase;
  const xs = steps(v).filter((s) => s.phase === shown);
  const failPh = ps.findIndex((p) => p.s === 'fail' || p.s === 'warn');
  let title = `第 ${shown + 1} 步 ${PHASES[shown]}`;
  let hint = '点上面任意一步看细节';
  let body = '';
  if (shown === 4) {
    title = '完成';
    // 后端的结论行是「三端回读等于最新，防护核验 …」或「防护不用动：…」，按逗号拆成一项一行
    body = ph === 'done'
      ? v.line.split('，').map((x) => plainRow(x, 'done')).join('') + plainRow(`本次用时 ${v.elapsed.toFixed(1)} 秒，已记进「最近几次」，下次按它估剩余时间`, 'done')
      : plainRow(ph === 'latest' ? '三端都是最新，这次不用更新' : '还没走到这一步');
    return { title, hint, body };
  }
  if (ph === 'blocked' && shown === 0) {
    const bs = v.blockers ?? [];
    return { title: `第 1 步 检查：发现 ${bs.length} 件事要你先做`, hint: '做完点「都做好了，重新检查」', body: bs.map(blockerRow).join('') };
  }
  if ((ph === 'failed' || ph === 'checkfail' || ph === 'unsafe') && shown === Math.max(failPh, 0)) {
    title += ' · 出错时谁来处理';
    hint = v.failedStep ? '停在：' + v.failedStep : '';
    body = tiersHTML(v);
    if (ph === 'failed') body += xs.map((s) => stepRow(s, v)).join('');
    return { title, hint, body };
  }
  if (ph === 'never') return { title, hint, body: plainRow('还没查过；点「现在检查」，约 3 秒') };
  if (ph === 'latest' && shown > 0) return { title, hint, body: plainRow('三端都是最新，这一步这次不用跑') };
  if (!xs.length) {
    const why = ['', '这次 CLI 和 Desktop 都不用装，只登记指纹', '这次不用动防护，不弹 UAC', '这次不用动防护，不进 UAC'][shown];
    return { title, hint, body: plainRow(ph === 'checking' ? '查完版本才知道这一步要不要跑' : why) };
  }
  body = xs.map((s) => stepRow(s, v)).join('');
  if (shown === 0 && !started(ph)) body += targetsRow(v);
  if (shown === 2 && ph === 'update') hint = '整次只弹这一次 UAC，那就是确认';
  return { title, hint, body };
}

// ---------------------------------------------------------------- 渲染

function render() {
  if (!root) return;
  const v = state.view ?? blank();
  const hero = heroOf(v);
  const ps = phaseStates(v);
  const active = activePhase(v, ps);
  const shown = state.pinned ?? (active >= 0 ? active : 1);
  const d = detailHTML(v, shown, ps);
  const products = v.products?.length ? v.products : PLACEHOLDER;
  const fill = trackFill(v, active);
  const pct = v.phase === 'running' ? Math.round(v.percent) + '%' : v.phase === 'done' ? '100%' : '';
  const trackTone = v.phase === 'failed' ? (v.tier === 'retry' ? 'warn' : 'fail') : 'pass';
  const disabled = hero.main.disabled || state.busy;
  const hint = v.phase === 'checking' ? '' : v.hint;
  const history = v.history ?? [];

  root.innerHTML = `
  <div class="u-hero t-${hero.tone}">
    <div class="u-hero-l">
      <span class="u-icon ${hero.spin ? 'spin' : ''}">${hero.icon}</span>
      <div class="u-hero-text"><span class="u-title">${esc(v.title || '还没查过新版本')}</span><span class="u-line" title="${esc(hero.line)}">${esc(hero.line)}</span></div>
    </div>
    <div class="u-hero-r">
      <div class="u-btns">
        ${hero.second ? `<button class="btn big ghost" id="u-second" ${state.busy ? 'disabled' : ''}>${esc(hero.second.label)}</button>` : ''}
        <button class="btn big primary" id="u-main" ${disabled ? 'disabled' : ''}>${esc(state.busy && !hero.main.disabled ? '请稍候…' : hero.main.label)}</button>
      </div>
      <span class="u-hint">${esc(hint)}</span>
    </div>
  </div>

  <div class="u-prods">${products.map((p) => productCard(v, p)).join('')}</div>

  <div class="u-flow">
    <div class="u-flow-top"><span>${esc(flowHint(v, active))}</span><span class="mono">${pct}</span></div>
    <div class="u-track-wrap">
      <div class="u-track"></div>
      <div class="u-track-fill ${trackTone}" style="width: calc((100% - 88px) * ${(fill / 100).toFixed(3)})"></div>
      <div class="u-phases">
        ${ps.map((p, i) => `<button class="u-ph p-${p.s} ${i === shown ? 'sel' : ''}" data-ph="${i}" aria-pressed="${i === shown}">
          <span class="u-dot ${p.s === 'run' ? 'pulse' : ''}">${{ done: '✓', fail: '×', warn: '!' }[p.s as string] ?? i + 1}</span>
          <span class="u-ph-name">${PHASES[i]}</span>
          <span class="u-ph-time mono">${esc(p.time)}</span>
        </button>`).join('')}
      </div>
    </div>
  </div>

  <div class="u-bottom">
    <section class="u-detail">
      <div class="u-sec-head"><b>${esc(d.title)}</b><span>${esc(d.hint)}</span></div>
      <div class="u-rows">${d.body}</div>
    </section>
    <section class="u-history">
      <div class="u-sec-head"><b>最近几次</b><span>${history.length ? '点一行打开记录目录' : ''}</span></div>
      ${history.length ? history.map((h) => `<button class="u-hrow" data-dir="${esc(h.dir)}" title="${esc(h.dir)}">
          <span class="mono">${esc(h.at)}</span>
          <span class="u-hwhat"><i class="dot ${h.tone}"></i><span>${esc(h.what)}</span></span>
          <span class="mono">${h.seconds ? h.seconds.toFixed(1) + ' 秒' : ''}</span></button>`).join('')
        : '<div class="u-empty">还没有更新记录。桌面「Claude-三端更新」和这里的记录放在同一个目录。</div>'}
    </section>
  </div>`;

  $('#u-main', root)?.addEventListener('click', () => hero.main.run?.());
  $('#u-second', root)?.addEventListener('click', () => hero.second?.run?.());
  $$('.u-ph', root).forEach((b) => b.addEventListener('click', () => {
    const i = Number(b.dataset.ph);
    state.pinned = state.pinned === i ? null : i;
    render();
  }));
  $$('.u-hrow', root).forEach((b) => b.addEventListener('click', () => open(b.dataset.dir!)));
  $('[data-plugin]', root)?.addEventListener('click', async () => {
    const err = await api.updateOpenPlugin();
    toast(err ? '打不开插件页：' + err : '已在 VS Code 打开插件页；点完更新退出 VS Code，再回来点「都做好了，重新检查」', err ? 'err' : 'ok');
  });
}
