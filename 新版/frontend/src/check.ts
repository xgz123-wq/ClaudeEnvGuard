// 体检页（照原型）：结论区（一句结论 + 唯一主按钮）＋ 线路图 ＋ 6 张卡片 ＋ 选中卡片的明细。
// 一键修复的计划、清理登录的清单都直接在明细区展开，不再弹窗；还原挪到「记录」页按项做。
import { api, errText } from './api';
import { ctx, startUrl } from './ctx';
import { renderRoute } from './route';
import type { CleanPlan, FixAction, FixPlan, Report, Result, Status } from './types';
import { $, esc, mb, statusLabel, toast } from './util';

type Card = 'todo' | 'route' | 'browser' | 'cli' | 'desktop' | 'system';
type Phase = 'ready' | 'plan' | 'fixing';

const CARDS: { id: Card; name: string }[] = [
  { id: 'todo', name: '需要处理' },
  { id: 'route', name: '线路' },
  { id: 'browser', name: '专用浏览器' },
  { id: 'cli', name: 'Claude Code' },
  { id: 'desktop', name: 'Claude Desktop' },
  { id: 'system', name: '系统' },
];

const PRIORITY = ['P1', 'P2', 'P4', 'B1', 'B2', 'B3', 'B4', 'B5', 'B7', 'D2', 'C2', 'C3', 'P3', 'B6', 'S1', 'S4', 'C4', 'B8', 'C6', 'D4', 'P5', 'C5', 'D3', 'S2', 'S3', 'S7', 'S6', 'S5'];
const rank = (s: Status) => ({ fail: 4, unknown: 3, warn: 2, pass: 1, na: 0 }[s] ?? 0);
const cleanNames: Record<string, string> = { code: 'Claude Code', desktop: 'Claude Desktop', browser: '这个专用浏览器' };

const st = {
  report: null as Report | null,
  checking: false,
  offline: false,
  phase: 'ready' as Phase,
  card: 'todo' as Card,
  showPass: false,
  expanded: new Set<string>(),
  plan: null as FixPlan | null,
  planOff: new Set<string>(), // 默认勾选、被用户取消的
  planOn: new Set<string>(), // 默认不勾、被用户勾上的可选项
  alias: '',
  outcome: new Map<string, boolean>(), // 修完每项成没成
  fixed: null as null | { ok: number; fail: number; backupId: string; until: number },
  clean: null as null | { kind: string; plan: CleanPlan | null; ok: boolean; busy: boolean; err: string },
  firstCheck: true,
  onFirstCheck: () => {},
};

let root: HTMLElement;
let undoTimer = 0;

// ---------------------------------------------------------------- 对外

export function mountCheck(el: HTMLElement, onFirstCheck: () => void) {
  root = el;
  st.onFirstCheck = onFirstCheck;
  root.innerHTML = `
    <div class="g-top">
      <section class="g-hero" id="g-hero"></section>
      <button class="g-route" id="g-route" aria-label="线路：点开看 P1–P5"></button>
    </div>
    <nav class="g-cards" id="g-cards" role="tablist"></nav>
    <section class="g-detail" id="g-detail" role="tabpanel"></section>`;
  root.addEventListener('click', onClick);
  root.addEventListener('change', onChange);
  root.addEventListener('input', (e) => {
    const t = e.target as HTMLInputElement;
    if (t.id === 'g-alias') st.alias = t.value.trim();
  });
  api.onReport((partial) => {
    if (st.checking) {
      st.report = partial;
      render();
    }
  });
  ctx.runCheck = (offline = false) => { runCheck(offline); };
  render();
}

export function checkBadge(): { n: number; fail: boolean } {
  const rep = st.report;
  if (!rep?.final || st.checking) return { n: 0, fail: false };
  const m = must(rep);
  return { n: m.length, fail: m.some((r) => r.status === 'fail') };
}

export function checkLastText(): string {
  if (st.checking) return '正在检查';
  const rep = st.report;
  if (!rep?.final) return '';
  const at = new Date(rep.startedAt);
  const hm = isNaN(at.getTime()) ? '' : at.toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' });
  return `${hm} 检查 · ${(rep.durationMs / 1000).toFixed(1)} 秒${rep.offline ? ' · 离线' : ''}`;
}

export function lastReport(): Report | null {
  return st.report;
}

export function isChecking() {
  return st.checking;
}

export async function runCheck(offline = false) {
  if (st.checking) return;
  st.checking = true;
  st.offline = offline;
  st.report = null;
  st.phase = 'ready';
  st.plan = null;
  st.clean = null;
  render();
  try {
    const rep = await api.check(offline, ctx.init?.settings.selectedBrowser ?? '');
    st.report = rep;
    for (const r of rep.results ?? []) if (r.status !== 'fail') st.expanded.delete(r.id);
    api.log(`结果已显示：${rep.summary?.headline ?? ''}（${rep.results?.length ?? 0} 项）`);
  } catch (e) {
    toast('检查失败：' + errText(e), 'err');
  } finally {
    st.checking = false;
    render();
    if (st.firstCheck) {
      st.firstCheck = false;
      st.onFirstCheck();
    }
  }
}

// ---------------------------------------------------------------- 数据

function results(rep: Report | null): Result[] {
  return rep?.results ?? [];
}

function bad(r: Result) {
  return !r.pending && (r.status === 'fail' || r.status === 'warn' || r.status === 'unknown');
}

function must(rep: Report | null): Result[] {
  return results(rep)
    .filter((r) => !r.advanced && bad(r))
    .sort((a, b) => rank(b.status) - rank(a.status) || PRIORITY.indexOf(a.id) - PRIORITY.indexOf(b.id));
}

function optional(rep: Report | null): Result[] {
  return results(rep).filter((r) => r.advanced && !r.pending && (r.status === 'warn' || r.status === 'fail'));
}

function groupOf(card: Card): Result[] {
  const g = card === 'route' ? 'overview' : card;
  return results(st.report).filter((r) => r.group === g);
}

function isOn(a: FixAction) {
  return a.defaultOn ? !st.planOff.has(a.id) : st.planOn.has(a.id);
}

function onActions(): FixAction[] {
  return (st.plan?.actions ?? []).filter(isOn);
}

function ports(): Record<string, number> {
  const s = ctx.init?.settings;
  return { browser: s?.portBrowser ?? 43866, cli: s?.portCli ?? 43865, desktop: s?.portDesktop ?? 43867 };
}

function targetText(rep: Report | null): string {
  const t = rep?.target;
  if (!t) return st.checking ? '等出口测完…' : '还不知道';
  if (t.source === 'none') return t.sourceText;
  return `${t.country} · ${t.iana} · ${t.lang}（${t.source === 'manual' ? '手选' : '跟随出口'}）`;
}

// ---------------------------------------------------------------- 渲染

function render() {
  renderHero();
  renderRouteCard();
  renderCards();
  renderDetail();
  ctx.renderTabs();
}

interface Hero { tone: 'pass' | 'warn' | 'fail' | 'info'; icon: string; spin: boolean; title: string; line: string;
  btn: string; act: string; disabled: boolean; second?: string; secondAct?: string; hint: string }

function heroState(): Hero {
  const rep = st.report;
  if (st.checking) {
    const local = !!rep;
    return { tone: 'info', icon: '✻', spin: true, title: st.offline ? '正在离线检查…' : '正在检查…',
      line: (local ? '本机配置 ✓' : '读本机配置…') + (st.offline ? '' : ' · ' + (local ? '测三条线路…' : '等待联网')),
      btn: '检查中…', act: '', disabled: true, hint: st.offline ? '' : '约 4 秒' };
  }
  if (st.phase === 'plan') {
    const on = onActions();
    const admin = on.some((a) => a.needsAdmin);
    return { tone: 'warn', icon: '!', spin: false, title: '确认要改的地方', line: '每一处都会先备份，修完能在「记录」页按项还原',
      btn: `修这 ${on.length} 项`, act: 'apply', disabled: on.length === 0, second: '先不修', secondAct: 'cancel-plan',
      hint: admin ? '含要管理员的项，会弹一次 UAC' : '不需要管理员' };
  }
  if (st.phase === 'fixing') {
    return { tone: 'info', icon: '✻', spin: true, title: '正在修复…', line: '改完会自动重新检查；弹出 Windows 授权窗口请点「是」',
      btn: '修复中…', act: '', disabled: true, hint: '' };
  }
  if (!rep) {
    return { tone: 'info', icon: '?', spin: false, title: '还没检查', line: '点「重新检查」看看这台电脑配得怎么样', btn: '重新检查', act: 'check', disabled: false, hint: '' };
  }
  const m = must(rep);
  const opt = optional(rep);
  const fixable = m.filter((r) => r.fixId);
  const fx = st.fixed;
  let h: Hero;
  if (!m.length) {
    h = { tone: 'pass', icon: '✓', spin: false,
      title: fx ? `修好 ${fx.ok} 项，重新检查全部通过` : rep.offline ? '本机配置全部通过' : '全部通过',
      line: rep.offline ? '离线检查没测出口；点「重新检查」联网再查一次' : opt.length ? `另有 ${opt.length} 个可选优化，不影响使用` : '没有需要你动手的地方',
      btn: '重新检查', act: 'check', disabled: false, hint: '' };
    if (!rep.offline && (rep.browsers ?? []).length) {
      h.second = '用专用浏览器肉眼复核';
      h.secondAct = 'eyeball';
      h.hint = '程序查的是配置，网页上看到的才算最终结果';
    }
  } else if (fixable.length) {
    h = { tone: fx ? 'warn' : 'fail', icon: '!', spin: false,
      title: fx ? `修好 ${fx.ok} 项${fx.fail ? `，${fx.fail} 项没修成` : ''}，还剩 ${m.length} 项` : `有 ${m.length} 项没配好`,
      line: fx ? '剩下的是没勾的或没修成的项，想修再点一次' : `其中 ${fixable.length} 项能一键修；先看清要改什么再动手`,
      btn: '一键修复', act: 'plan', disabled: false, hint: '' };
  } else {
    h = { tone: m.some((r) => r.status === 'fail') ? 'fail' : 'warn', icon: '!', spin: false,
      title: `有 ${m.length} 项要你手动处理`,
      line: rep.summary.nextStep || '这类问题卫士修不了，展开每一项能看到做法',
      btn: '处理完，重新检查', act: 'check', disabled: false, second: '复制给 AI 排查', secondAct: 'copy-ai', hint: '' };
  }
  if (fx && fx.until > Date.now() && fx.backupId) {
    h.second = `撤销这次修复（${Math.ceil((fx.until - Date.now()) / 1000)} 秒）`;
    h.secondAct = 'undo';
    h.hint = '过了这几秒也能在「记录」页按项还原，记录一直在';
  }
  return h;
}

function renderHero() {
  const h = heroState();
  const el = $('#g-hero', root)!;
  el.className = 'g-hero k-' + h.tone;
  el.innerHTML = `
    <div class="g-hero-main">
      <span class="g-icon ${h.spin ? 'spin' : ''}">${h.icon}</span>
      <div class="g-hero-text">
        <span class="g-title">${esc(h.title)}</span>
        <span class="g-line">${esc(h.line)}</span>
        <span class="g-target">随 IP 项按：<span class="mono">${esc(targetText(st.report))}</span><button class="link" data-act="settings">改</button></span>
      </div>
    </div>
    <div class="g-hero-actions">
      <button class="btn primary big" data-act="${h.act}" ${h.disabled ? 'disabled' : ''}>${esc(h.btn)}</button>
      ${h.second ? `<button class="btn big" data-act="${h.secondAct}">${esc(h.second)}</button>` : ''}
      <span class="g-hint">${esc(h.hint)}</span>
    </div>`;
}

function renderRouteCard() {
  const rv = renderRoute(st.report, ports(), st.checking);
  const el = $('#g-route', root)!;
  el.classList.toggle('sel', st.card === 'route' && st.phase === 'ready' && !st.clean);
  el.classList.toggle('checking', st.checking);
  el.innerHTML = `<span class="g-route-head"><span>线路 · 三个入口各走自己的端口</span><span class="${rv.split ? 'bad' : ''}">${esc(rv.note)}</span></span>${rv.html}`;
}

function renderCards() {
  const rep = st.report;
  const sel = st.phase === 'ready' && !st.clean ? st.card : st.phase !== 'ready' ? 'todo' : st.card;
  $('#g-cards', root)!.innerHTML = CARDS.map((c) => {
    const list = c.id === 'todo' ? must(rep) : groupOf(c.id).filter((r) => !r.advanced);
    const b = c.id === 'todo' ? list : list.filter(bad);
    const worst = b.some((r) => r.status === 'fail') ? 'fail' : b.length ? 'warn' : 'pass';
    let mark = '✓', tone: string = 'pass';
    let sub = c.id === 'todo' ? '没有要处理的' : `${list.length} 项都通过`;
    if (b.length) {
      mark = String(b.length);
      tone = worst;
      sub = c.id === 'todo' ? `${b.length} 项要处理` : `${b.length} 项要看 · 共 ${list.length} 项`;
    }
    if (c.id === 'browser' && rep?.selectedBrowser) sub = rep.selectedBrowser + ' · ' + sub;
    const pending = st.checking && (!rep || c.id === 'todo' || groupOf(c.id).some((r) => r.pending) || !groupOf(c.id).length);
    if (pending) {
      mark = '…';
      tone = 'wait';
      sub = '检查中';
    } else if (!rep) {
      mark = '·';
      tone = 'wait';
      sub = '还没检查';
    }
    return `<button class="g-card ${sel === c.id ? 'sel' : ''}" role="tab" aria-selected="${sel === c.id}" data-card="${c.id}">
      <span class="g-card-top"><span class="g-card-name">${c.name}</span><span class="g-mark ${tone}">${mark}</span></span>
      <span class="g-card-sub">${esc(sub)}</span></button>`;
  }).join('');
}

function rowHTML(r: Result, i: number) {
  const pending = !!r.pending;
  const s = pending ? 'pending' : r.status;
  const mark = pending ? '·' : ({ pass: '✓', warn: '!', fail: '×', unknown: '?', na: '–' } as Record<string, string>)[r.status];
  const open = st.expanded.has(r.id);
  const why = !pending && r.detail && r.status !== 'pass' && r.status !== 'na' ? `<span class="g-why">${esc(r.detail)}</span>` : '';
  const fixable = !pending && !!r.fixId && (r.status === 'warn' || r.status === 'fail');
  const more = [
    r.target ? `<div class="kv"><i>目标</i><span>${esc(r.target)}</span></div>` : '',
    r.detail ? `<div class="kv"><i>${r.status === 'pass' ? '说明' : '原因'}</i><span>${esc(r.detail)}</span></div>` : '',
    r.advice ? `<div class="kv"><i>怎么修</i><span>${esc(r.advice)}</span></div>` : '',
  ].join('');
  return `<div class="g-row s-${s} ${open ? 'open' : ''} ${pending ? 'shimmer' : 'in'}" style="animation-delay:${Math.min(i, 12) * 30}ms" data-id="${esc(r.id)}">
    <button class="g-row-head" data-row="${esc(r.id)}" aria-expanded="${open}">
      <span class="g-dot">${mark}</span>
      <span class="g-row-title"><span class="g-row-name">${esc(r.title)}<span class="rid">${esc(r.id)}</span>${r.advanced ? '<span class="tag opt">可选</span>' : ''}</span>${why}</span>
      <span class="g-cur mono" title="${esc(r.current ?? '')}">${pending ? '' : esc(r.current ?? '')}</span>
      ${fixable ? '<span class="tag fix">能一键修</span>' : ''}
      <span class="pill ${s}">${pending ? '检测中' : statusLabel[r.status]}</span>
    </button>
    ${open ? `<div class="g-more">${more || '<div class="kv"><i>说明</i><span>没有更多信息。</span></div>'}</div>` : ''}
  </div>`;
}

function toolsHTML(): string {
  const rep = st.report;
  if (st.checking || !rep) return '';
  const tool = (note: string, btns: string) => `<div class="g-tools"><span class="g-tool-note">${note}</span>${btns}</div>`;
  const cleanBtn = (k: string) => `<button class="btn small ghost danger-text" data-clean="${k}">清理登录…</button>`;
  switch (st.card) {
    case 'browser': {
      const list = rep.browsers ?? [];
      if (!list.length) return tool('还没有专用浏览器；「需要处理」里一键修复可以新建一个', '');
      const opts = list.map((b) => `<option value="${esc(b.name)}" ${b.name === rep.selectedBrowser ? 'selected' : ''}>${esc(b.name)}${b.lastUsed ? '（最近使用 ' + esc(b.lastUsed) + '）' : ''}${b.standard ? '' : '（非标准目录）'}</option>`).join('');
      return tool(`检查哪个专用浏览器：<select id="g-browser">${opts}</select>`,
        `<button class="btn small ghost" data-act="open-browser" title="按桌面快捷方式里的完整防护参数打开">打开这个浏览器</button>
         <button class="btn small ghost" data-act="eyeball" title="在这个专用浏览器里打开起始网址人工复核">在浏览器里复核 IP</button>${cleanBtn('browser')}`);
    }
    case 'cli':
      return tool('Claude Code 读 ~/.claude/settings.json，改完要重启 Claude Code', cleanBtn('code'));
    case 'desktop':
      return tool('Desktop 的代理来自 HKLM 策略，改完要完全退出（含托盘）再打开', cleanBtn('desktop'));
    case 'system':
      return tool('时区和区域格式「随 IP」：换了出口地区，这里的目标会跟着变', '');
  }
  return '';
}

function planHTML(): string {
  const p = st.plan;
  if (!p) return '<div class="g-loading">正在生成修复计划…</div>';
  const notes = (p.notes ?? []).map((n) => `<div class="note">${esc(n)}</div>`).join('');
  const fixing = st.phase === 'fixing';
  const hasCreate = (p.actions ?? []).some((a) => a.id === 'browser.create');
  const rows = (p.actions ?? []).map((a) => {
    const on = isOn(a);
    let mark = '', cls = '';
    if (fixing && on) {
      const o = st.outcome.get(a.id);
      if (o === undefined) { mark = '✻'; cls = 'spin run'; } else { mark = o ? '✓' : '×'; cls = o ? 'ok' : 'bad'; }
    }
    const change = (a.changes ?? []).map((c) => `${c.what}：${c.from} → ${c.to}`).join('；');
    return `<label class="g-plan ${on ? 'on' : ''}">
      <input type="checkbox" data-plan="${esc(a.id)}" ${on ? 'checked' : ''} ${fixing ? 'disabled' : ''}>
      <span class="g-plan-mark ${cls}">${mark}</span>
      <span class="g-plan-text"><span>${esc(a.title)}</span><span class="mono g-plan-change" title="${esc(change)}">${esc(change)}</span>${a.note ? `<span class="g-plan-note">${esc(a.note)}</span>` : ''}</span>
      ${a.needsAdmin ? '<span class="tag admin">要管理员</span>' : ''}${a.defaultOn ? '' : '<span class="tag opt">可选</span>'}
    </label>`;
  }).join('');
  const alias = hasCreate ? `<label class="g-alias"><span>新专用浏览器的名字</span><input id="g-alias" maxlength="40" value="${esc(st.alias)}" placeholder="例如 账号1（字母、数字、_ 和 -）" ${fixing ? 'disabled' : ''}></label>` : '';
  return notes + alias + rows;
}

function cleanHTML(): string {
  const c = st.clean!;
  if (c.err) return `<div class="note bad">列清单失败：${esc(c.err)}</div>`;
  if (!c.plan) return '<div class="g-loading">正在列出要删除的内容…</div>';
  const p = c.plan;
  const items = p.items ?? [];
  const app = cleanNames[c.kind];
  const rows = items.map((it) => `<div class="g-clean-row">
      <span class="tag del">${it.action === 'edit' ? '改' : '删'}</span>
      <span class="mono g-clean-path" title="${esc(it.path)}">${esc(it.path)}</span>
      <span class="g-clean-meta">${it.note ? esc(it.note) + ' · ' : ''}${mb(it.bytes)}</span></div>`).join('');
  return `<div class="note bad">登录凭据删了不留备份（留备份等于没清）；${esc(app)}要先完全退出，在运行时会拒绝。${p.warning ? esc(p.warning) : ''}</div>
    ${p.blocked ? `<div class="note warn">现在不能清理：${esc(p.blocked)}</div>` : ''}
    ${items.length ? rows + `<div class="g-clean-total">合计 ${p.totalMb.toFixed(1)} MB</div>` : '<div class="g-empty-line">没有需要清理的内容</div>'}
    ${items.length && !p.blocked ? `<div class="g-clean-go">
      <label class="g-clean-ok"><input type="checkbox" id="g-clean-ok" ${c.ok ? 'checked' : ''} ${c.busy ? 'disabled' : ''}><span>我核对过上面的清单，知道删了不能恢复</span></label>
      <button class="btn danger" data-act="do-clean" ${c.ok && !c.busy ? '' : 'disabled'}>${c.busy ? '正在删除…' : `删除这 ${items.length} 项`}</button></div>` : ''}`;
}

function renderDetail() {
  const el = $('#g-detail', root)!;
  const rep = st.report;
  let title = '', hint = '', body = '', back = false;
  if (st.phase === 'plan' || st.phase === 'fixing') {
    title = st.phase === 'fixing' ? '正在修复' : '一键修复：勾选要改的';
    hint = '默认只勾必修项；可选项要你自己勾';
    body = planHTML();
  } else if (st.clean) {
    title = st.clean.plan?.title || '清理' + cleanNames[st.clean.kind] + '登录';
    hint = '换账号、重新登录时用；先列清单，核对后才删';
    back = true;
    body = cleanHTML();
  } else if (st.checking && !rep) {
    title = '检查中';
    hint = '本机项先出，联网项随后到';
    body = '<div class="skeleton">' + '<div class="sk-row"></div>'.repeat(6) + '</div>';
  } else if (st.card === 'todo') {
    const m = must(rep);
    title = m.length ? `需要处理 ${m.length} 项` : '需要处理';
    hint = m.length ? '按严重程度排好了；点一行看原因和做法' : '';
    if (st.checking) hint = '本机项先出，联网项随后到';
    if (m.length) body = m.map(rowHTML).join('');
    else if (rep?.final) {
      const opt = optional(rep);
      body = `<div class="g-allgood in"><span class="g-allgood-icon">✓</span><span><b>必检项都没问题</b><span>换节点、更新 Claude 之后再来查一次。${opt.length ? '下面是不影响使用的可选项。' : ''}</span></span></div>`
        + opt.map(rowHTML).join('');
    } else {
      const pend = results(rep).filter((r) => r.pending);
      body = pend.map(rowHTML).join('');
    }
  } else {
    const name = CARDS.find((c) => c.id === st.card)!.name;
    const list = groupOf(st.card);
    const b = list.filter((r) => bad(r) || r.pending).sort((x, y) => rank(y.status) - rank(x.status));
    const rest = list.filter((r) => !bad(r) && !r.pending);
    title = `${name} · ${list.length} 项`;
    hint = '有问题的排前面，通过的收起来';
    body = toolsHTML() + b.map(rowHTML).join('');
    if (st.showPass || st.card === 'route') body += rest.map((r, i) => rowHTML(r, b.length + i)).join('');
    if (!b.length && !st.showPass && st.card !== 'route' && rest.length) body += `<div class="g-empty-line">这一类都通过了</div>`;
    if (rest.length && st.card !== 'route') body += `<button class="g-pass-toggle" data-act="toggle-pass">${st.showPass ? '收起通过的项' : `另外 ${rest.length} 项通过 · 展开看`}</button>`;
  }
  el.innerHTML = `<div class="g-detail-head"><span class="g-detail-title">${esc(title)}</span>
      <span class="g-detail-hint">${esc(hint)}${back ? '<button class="btn small ghost" data-act="back">返回</button>' : ''}</span></div>
    <div class="g-detail-body">${body}</div>`;
}

// ---------------------------------------------------------------- 交互

async function onClick(e: MouseEvent) {
  const t = e.target as HTMLElement;
  const card = t.closest<HTMLElement>('[data-card]');
  if (card) {
    st.card = card.dataset.card as Card;
    st.showPass = false;
    st.clean = null;
    if (st.phase === 'plan') st.phase = 'ready';
    render();
    return;
  }
  if (t.closest('#g-route')) {
    if (st.phase === 'fixing') return;
    st.card = 'route';
    st.clean = null;
    if (st.phase === 'plan') st.phase = 'ready';
    render();
    return;
  }
  const row = t.closest<HTMLElement>('[data-row]');
  if (row) {
    const id = row.dataset.row!;
    if (st.expanded.has(id)) st.expanded.delete(id);
    else st.expanded.add(id);
    renderDetail();
    return;
  }
  const cl = t.closest<HTMLElement>('[data-clean]');
  if (cl) return openClean(cl.dataset.clean!);
  const b = t.closest<HTMLElement>('[data-act]');
  if (!b || (b as HTMLButtonElement).disabled) return;
  switch (b.dataset.act) {
    case 'check': return runCheck();
    case 'settings': return ctx.showPage('settings');
    case 'plan': return openPlan();
    case 'cancel-plan': st.phase = 'ready'; return render();
    case 'apply': return applyPlan();
    case 'undo': return undo();
    case 'eyeball': return eyeball();
    case 'open-browser': return openBrowser();
    case 'copy-ai': return copyForAI();
    case 'toggle-pass': st.showPass = !st.showPass; return renderDetail();
    case 'back': st.clean = null; return render();
    case 'do-clean': return doClean();
  }
}

async function onChange(e: Event) {
  const t = e.target as HTMLInputElement;
  if (t.dataset.plan) {
    const a = st.plan?.actions?.find((x) => x.id === t.dataset.plan);
    if (!a) return;
    if (a.defaultOn) { if (t.checked) st.planOff.delete(a.id); else st.planOff.add(a.id); }
    else if (t.checked) st.planOn.add(a.id); else st.planOn.delete(a.id);
    t.closest('.g-plan')!.classList.toggle('on', t.checked);
    renderHero();
    return;
  }
  if (t.id === 'g-clean-ok' && st.clean) {
    st.clean.ok = t.checked;
    renderDetail();
    return;
  }
  if (t.id === 'g-browser') {
    const name = (t as unknown as HTMLSelectElement).value;
    const err = await api.selectBrowser(name);
    if (err) return toast(err, 'err');
    if (ctx.init) ctx.init.settings.selectedBrowser = name;
    runCheck();
  }
}

async function openPlan() {
  st.phase = 'plan';
  st.card = 'todo';
  st.clean = null;
  st.plan = null;
  st.planOff.clear();
  st.planOn.clear();
  st.outcome.clear();
  render();
  try {
    st.plan = await api.plan('');
  } catch (e) {
    st.plan = { target: st.report!.target, actions: [], notes: ['生成计划失败：' + errText(e)] };
  }
  if (!(st.plan.actions ?? []).length) {
    toast((st.plan.notes ?? [])[0] || '没有能一键修复的项，剩下的要手动处理', 'info');
    st.phase = 'ready';
  }
  render();
}

async function applyPlan() {
  const ids = onActions().map((a) => a.id);
  if (!ids.length) return;
  if (ids.includes('browser.create') && st.alias && !/^[\p{L}\p{N}_-]{1,40}$/u.test(st.alias)) {
    toast('浏览器名字只能用字母、数字、下划线和短横线', 'err');
    return;
  }
  st.phase = 'fixing';
  st.outcome.clear();
  render();
  const r = await api.apply(ids, st.alias);
  for (const o of r.outcomes ?? []) if (o.actionId) st.outcome.set(o.actionId, o.ok);
  for (const id of ids) if (!st.outcome.has(id)) st.outcome.set(id, r.ok);
  renderDetail();
  const ok = [...st.outcome.values()].filter(Boolean).length;
  const fail = ids.length - ok;
  api.log(`一键修复：${r.message}`);
  if (!r.ok) toast(r.message, 'err');
  st.fixed = { ok, fail, backupId: r.backupId ?? '', until: Date.now() + 10_000 };
  ctx.refreshRecords();
  await new Promise((res) => setTimeout(res, 600)); // 让人看清每项的结果再重查
  st.phase = 'ready';
  st.plan = null;
  startUndoTimer();
  await runCheck();
}

function startUndoTimer() {
  window.clearInterval(undoTimer);
  undoTimer = window.setInterval(() => {
    if (!st.fixed || st.fixed.until <= Date.now()) window.clearInterval(undoTimer);
    if (!st.checking) renderHero();
  }, 1000);
}

async function undo() {
  const fx = st.fixed;
  if (!fx?.backupId) return;
  window.clearInterval(undoTimer);
  st.fixed = null;
  renderHero();
  const r = await api.restore(fx.backupId);
  toast(r.ok ? `已撤销，按备份还原了 ${fx.ok} 项；记录留在「记录」页` : r.message, r.ok ? 'ok' : 'err');
  ctx.refreshRecords();
  runCheck();
}

async function eyeball() {
  const name = st.report?.selectedBrowser ?? '';
  if (!name) return toast('还没有专用浏览器', 'err');
  const err = await api.launchBrowser(name, startUrl());
  toast(err || `已用「${name}」打开 ${startUrl().replace(/^https?:\/\//, '')}：WebRTC、浏览器时区这些只能在网页上亲眼看`, err ? 'err' : 'ok');
}

async function openBrowser() {
  const name = st.report?.selectedBrowser ?? '';
  const err = await api.launchBrowser(name, '');
  toast(err || `已按快捷方式参数打开「${name}」`, err ? 'err' : 'ok');
}

async function copyForAI() {
  const m = must(st.report);
  const lines = m.map((r) => `- ${r.id} ${r.title}【${statusLabel[r.status]}】\n  现在：${r.current ?? ''}\n  目标：${r.target ?? ''}\n  原因：${r.detail ?? ''}`);
  const text = `Claude 环境卫士 v${ctx.init?.version ?? ''} 体检有 ${m.length} 项要手动处理：\n${lines.join('\n')}\n\n先别改，列可能原因和验证方式。`;
  await api.copy(text);
  toast(`已复制：${m.length} 项的现象、当前值、目标值 + 一句「先别改，列可能原因和验证方式」`, 'ok');
}

async function openClean(kind: string) {
  st.clean = { kind, plan: null, ok: false, busy: false, err: '' };
  render();
  try {
    st.clean.plan = await api.cleanPlan(kind);
  } catch (e) {
    st.clean.err = errText(e);
  }
  renderDetail();
}

async function doClean() {
  const c = st.clean;
  if (!c?.plan || !c.ok) return;
  c.busy = true;
  renderDetail();
  const r = await api.clean(c.kind, (c.plan.items ?? []).map((it) => it.path));
  const app = cleanNames[c.kind];
  toast(r.ok ? `已删除 ${(r.removed ?? []).length} 项，下次打开${app}要重新登录；已记进「记录」页` : r.message + (r.errors?.length ? '：' + r.errors[0] : ''), r.ok ? 'ok' : 'err');
  st.clean = null;
  ctx.refreshRecords();
  runCheck();
}
