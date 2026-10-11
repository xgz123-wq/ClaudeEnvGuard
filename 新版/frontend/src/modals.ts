// 弹窗：一键修复、清理、还原、设置。每个弹窗都先展示「会改什么」，用户确认后才执行。
import { api, errText } from './api';
import type { ApplyResult, CleanPlan, FixAction, InitState, Settings } from './types';
import { $, $$, esc, h, mb, toast } from './util';

interface ModalOpts { title: string; sub?: string; body: string; wide?: boolean }

let current: HTMLElement | null = null;
let busy = false;

export function closeModal() {
  if (busy) return;
  current?.remove();
  current = null;
}

function openModal(o: ModalOpts): HTMLElement {
  closeModal();
  const el = h(`<div class="modal-back" role="dialog" aria-modal="true">
    <div class="modal ${o.wide ? 'wide' : ''}">
      <header><div><h2>${esc(o.title)}</h2>${o.sub ? `<p>${esc(o.sub)}</p>` : ''}</div>
        <button class="icon-btn close" title="关闭（Esc）" aria-label="关闭">✕</button></header>
      <div class="modal-body">${o.body}</div>
      <footer></footer>
    </div></div>`);
  el.addEventListener('mousedown', (e) => { if (e.target === el) closeModal(); });
  $('.close', el)!.addEventListener('click', closeModal);
  document.body.appendChild(el);
  current = el;
  return el;
}

function setFooter(m: HTMLElement, html: string) {
  $('footer', m)!.innerHTML = html;
}

function setBusy(m: HTMLElement, on: boolean, text = '') {
  busy = on;
  m.classList.toggle('busy', on);
  const close = $<HTMLButtonElement>('.close', m);
  if (close) close.disabled = on;
  if (on) setFooter(m, `<div class="working"><span class="spinner"></span>${esc(text)}</div>`);
}

function outcomesHTML(r: ApplyResult) {
  const rows = (r.outcomes ?? [])
    .map((o) => `<li class="${o.ok ? 'ok' : 'bad'}"><span class="dot"></span>${esc(o.message)}</li>`)
    .join('');
  return `<ul class="outcomes">${rows}</ul><p class="result-msg ${r.ok ? 'good' : 'bad'}">${esc(r.message)}</p>`;
}

// ---------------------------------------------------------------- 一键修复

export async function openFix(onDone: () => void) {
  const m = openModal({ title: '一键修复', sub: '先看清楚会改什么，再勾选执行。每次修复都会先备份，可在「还原」里撤销。', body: '<div class="loading">正在生成修复计划…</div>', wide: true });
  let alias = '';
  const plan = await api.plan(alias).catch((e) => ({ actions: [], notes: ['生成计划失败：' + errText(e)] }) as never);
  const actions: FixAction[] = plan.actions ?? [];
  const notes = (plan.notes ?? []).map((n: string) => `<div class="note">${esc(n)}</div>`).join('');
  if (!actions.length) {
    $('.modal-body', m)!.innerHTML = `${notes}<div class="empty-state"><div class="big">没有需要一键修复的项</div><p>剩下的问题需要手动处理，展开检查项能看到具体做法。</p></div>`;
    setFooter(m, '<button class="btn" data-x="close">知道了</button>');
    $('[data-x=close]', m)!.addEventListener('click', closeModal);
    return;
  }
  const hasCreate = actions.some((a) => a.id === 'browser.create');
  $('.modal-body', m)!.innerHTML = `${notes}
    ${hasCreate ? `<label class="field inline"><span>新专用浏览器的名字</span>
      <input id="alias" maxlength="40" placeholder="例如 账号1（只能用字母、数字、_ 和 -）"></label>` : ''}
    <div class="actions">${actions.map(actionCard).join('')}</div>`;
  const update = () => {
    const sel = $$<HTMLInputElement>('.act-check', m).filter((c) => c.checked);
    const admin = sel.some((c) => c.dataset.admin === '1');
    setFooter(m, `<div class="foot-note">${admin ? '⚠ 选中的项里有需要管理员的，会弹出一次 Windows 授权窗口，请点「是」。' : '所选项都不需要管理员权限。'}</div>
      <button class="btn ghost" data-x="cancel">取消</button>
      <button class="btn primary" data-x="go" ${sel.length ? '' : 'disabled'}>执行所选（${sel.length}）</button>`);
    $('[data-x=cancel]', m)!.addEventListener('click', closeModal);
    $('[data-x=go]', m)!.addEventListener('click', run);
  };
  $$('.act-check', m).forEach((c) => c.addEventListener('change', () => {
    c.closest('.act')!.classList.toggle('off', !(c as HTMLInputElement).checked);
    update();
  }));
  $('#alias', m)?.addEventListener('input', (e) => { alias = (e.target as HTMLInputElement).value.trim(); });
  update();

  async function run() {
    const ids = $$<HTMLInputElement>('.act-check', m).filter((c) => c.checked).map((c) => c.value);
    if (hasCreate && alias && !/^[\p{L}\p{N}_-]{1,40}$/u.test(alias)) {
      toast('浏览器名字只能用字母、数字、下划线和短横线', 'err');
      return;
    }
    setBusy(m, true, '正在修复…如果弹出 Windows 授权窗口，请点「是」');
    const r = await api.apply(ids, alias);
    setBusy(m, false);
    $('.modal-body', m)!.innerHTML = outcomesHTML(r);
    setFooter(m, `${r.backupId ? '<button class="btn ghost" data-x="restore">查看备份与还原</button>' : ''}
      <button class="btn primary" data-x="done">完成并重新检查</button>`);
    $('[data-x=done]', m)!.addEventListener('click', () => { closeModal(); onDone(); });
    $('[data-x=restore]', m)?.addEventListener('click', () => openRestore(onDone));
  }
}

function actionCard(a: FixAction) {
  const changes = (a.changes ?? [])
    .map((c) => `<li><span class="what">${esc(c.what)}</span><span class="from mono">${esc(c.from)}</span><span class="arrow">→</span><span class="to mono">${esc(c.to)}</span></li>`)
    .join('');
  return `<label class="act ${a.defaultOn ? '' : 'off'}">
    <input type="checkbox" class="act-check" value="${esc(a.id)}" data-admin="${a.needsAdmin ? 1 : 0}" ${a.defaultOn ? 'checked' : ''}>
    <div class="act-main">
      <div class="act-title">${esc(a.title)}${a.needsAdmin ? '<span class="chip warn">需要管理员</span>' : ''}${a.defaultOn ? '' : '<span class="chip">可选</span>'}</div>
      <ul class="changes">${changes}</ul>
      ${a.note ? `<div class="act-note">${esc(a.note)}</div>` : ''}
      <div class="act-ids">对应检查：${esc((a.checkIds ?? []).join('、'))}</div>
    </div></label>`;
}

// ---------------------------------------------------------------- 清理

const cleanNames: Record<string, string> = { code: 'Claude Code', desktop: 'Claude Desktop', browser: '专用浏览器' };

export async function openClean(kind: string, onDone: () => void) {
  const m = openModal({ title: '清理' + cleanNames[kind] + '登录', body: '<div class="loading">正在列出要删除的内容…</div>', wide: true });
  let plan: CleanPlan;
  try {
    plan = await api.cleanPlan(kind);
  } catch (e) {
    $('.modal-body', m)!.innerHTML = `<div class="note bad">列清单失败：${esc(errText(e))}</div>`;
    return;
  }
  const items = plan.items ?? [];
  if (plan.title) $('h2', m)!.textContent = plan.title;
  const rows = items
    .map((it, i) => `<label class="clean-row"><input type="checkbox" class="cl-check" value="${i}" checked>
      <span class="mono path" title="${esc(it.path)}">${esc(it.path)}</span>
      <span class="meta">${it.action === 'edit' ? '改' : '删'} · ${it.isDir ? '目录' : '文件'} · ${mb(it.bytes)} · ${it.files} 个文件</span>
      ${it.note ? `<span class="meta note-inline">${esc(it.note)}</span>` : ''}</label>`)
    .join('');
  $('.modal-body', m)!.innerHTML = `
    ${plan.blocked ? `<div class="note bad">现在不能清理：${esc(plan.blocked)}</div>` : ''}
    ${items.length ? `<div class="clean-list">${rows}</div><div class="clean-total">合计 ${plan.totalMb.toFixed(1)} MB</div>` : '<div class="empty-state"><div class="big">没有需要清理的内容</div></div>'}
    ${plan.warning ? `<div class="note warn">${esc(plan.warning)}</div>` : ''}
    ${items.length && !plan.blocked ? '<label class="confirm"><input type="checkbox" id="cl-ok"> 我已核对上面的清单，知道删除后不能恢复</label>' : ''}`;
  const update = () => {
    const ok = ($('#cl-ok', m) as HTMLInputElement | null)?.checked;
    const n = $$<HTMLInputElement>('.cl-check', m).filter((c) => c.checked).length;
    setFooter(m, `<button class="btn ghost" data-x="cancel">${items.length && !plan.blocked ? '取消' : '关闭'}</button>
      ${items.length && !plan.blocked ? `<button class="btn danger" data-x="go" ${ok && n ? '' : 'disabled'}>删除所选（${n}）</button>` : ''}`);
    $('[data-x=cancel]', m)!.addEventListener('click', closeModal);
    $('[data-x=go]', m)?.addEventListener('click', run);
  };
  $$('.cl-check', m).forEach((c) => c.addEventListener('change', update));
  $('#cl-ok', m)?.addEventListener('change', update);
  update();

  async function run() {
    const paths = $$<HTMLInputElement>('.cl-check', m).filter((c) => c.checked).map((c) => items[Number(c.value)].path);
    setBusy(m, true, '正在清理…');
    const r = await api.clean(kind, paths);
    setBusy(m, false);
    $('.modal-body', m)!.innerHTML = `<ul class="outcomes">
      ${(r.removed ?? []).map((p) => `<li class="ok"><span class="dot"></span><span class="mono">${esc(p)}</span></li>`).join('')}
      ${(r.errors ?? []).map((p) => `<li class="bad"><span class="dot"></span>${esc(p)}</li>`).join('')}</ul>
      <p class="result-msg ${r.ok ? 'good' : 'bad'}">${esc(r.message)}</p>`;
    setFooter(m, '<button class="btn primary" data-x="done">完成</button>');
    $('[data-x=done]', m)!.addEventListener('click', () => { closeModal(); onDone(); });
  }
}

// ---------------------------------------------------------------- 还原

export async function openRestore(onDone: () => void) {
  const m = openModal({ title: '还原修复', sub: '每次一键修复都会备份改动前的状态，可以整次撤销。', body: '<div class="loading">读取备份…</div>', wide: true });
  const list = (await api.backups().catch(() => [])) ?? [];
  if (!list.length) {
    $('.modal-body', m)!.innerHTML = '<div class="empty-state"><div class="big">还没有修复记录</div><p>做过一键修复后，这里会出现可还原的记录。</p></div>';
    setFooter(m, '<button class="btn" data-x="close">关闭</button>');
    $('[data-x=close]', m)!.addEventListener('click', closeModal);
    return;
  }
  $('.modal-body', m)!.innerHTML = `<div class="backups">${list
    .map((b) => `<div class="backup ${b.restored ? 'done' : ''}">
      <div class="b-main"><div class="b-time mono">${esc(b.createdAt || b.id)}</div>
        <div class="b-acts">${esc((b.actions ?? []).join('；'))}</div></div>
      <div class="b-ops"><button class="btn ghost small" data-open="${esc(b.dir)}">打开目录</button>
        ${b.restored ? '<span class="chip">已还原</span>' : `<button class="btn small" data-restore="${esc(b.id)}">还原这次</button>`}</div>
    </div>`)
    .join('')}</div>`;
  setFooter(m, '<button class="btn ghost" data-x="close">关闭</button>');
  $('[data-x=close]', m)!.addEventListener('click', closeModal);
  $$('[data-open]', m).forEach((b) => b.addEventListener('click', async () => {
    const err = await api.openPath(b.dataset.open!);
    if (err) toast(err, 'err');
  }));
  $$('[data-restore]', m).forEach((b) => b.addEventListener('click', async () => {
    setBusy(m, true, '正在还原…如果弹出 Windows 授权窗口，请点「是」');
    const r = await api.restore(b.dataset.restore!);
    setBusy(m, false);
    $('.modal-body', m)!.innerHTML = outcomesHTML(r);
    setFooter(m, '<button class="btn primary" data-x="done">完成并重新检查</button>');
    $('[data-x=done]', m)!.addEventListener('click', () => { closeModal(); onDone(); });
  }));
}

// ---------------------------------------------------------------- 设置

export function openSettings(init: InitState, onSaved: (s: Settings) => void) {
  const s = init.settings;
  const regionOpts = [`<option value="">跟随出口 IP（推荐）</option>`]
    .concat(init.regions.map((r) => `<option value="${esc(r.id)}" ${s.regionOverride === r.id ? 'selected' : ''}>${esc(r.label)} · ${esc(r.iana)}</option>`))
    .join('');
  const m = openModal({
    title: '设置', sub: '默认值和分享包一致；只有你改过 Clash 端口时才需要动。',
    body: `<div class="form">
      <label class="field"><span>目标地区</span><select id="st-region">${regionOpts}</select>
        <em>「随 IP」的项（时区、语言、区域）按这里判断。一般跟随出口即可；节点在美国东部但想按西部配置时再手选。</em></label>
      <div class="field-row">
        <label class="field"><span>CLI 端口（HTTP）</span><input id="st-cli" type="number" min="1" max="65535" value="${s.portCli}"></label>
        <label class="field"><span>浏览器端口（SOCKS5）</span><input id="st-br" type="number" min="1" max="65535" value="${s.portBrowser}"></label>
        <label class="field"><span>Desktop 端口（HTTP）</span><input id="st-dt" type="number" min="1" max="65535" value="${s.portDesktop}"></label>
      </div>
      <label class="field"><span>新建专用浏览器的首页</span><input id="st-url" value="${esc(s.startUrl)}"></label>
      <label class="field"><span>分享包位置（可选）</span><input id="st-root" value="${esc(s.sharePackageRoot)}" placeholder="留空自动在桌面上找">
        <em>用于读取「直连防护」的状态文件。</em></label>
    </div>`,
  });
  setFooter(m, `<button class="btn ghost" data-x="reset">恢复默认端口</button><span class="spacer"></span>
    <button class="btn ghost" data-x="cancel">取消</button><button class="btn primary" data-x="save">保存并重新检查</button>`);
  $('[data-x=cancel]', m)!.addEventListener('click', closeModal);
  $('[data-x=reset]', m)!.addEventListener('click', () => {
    ($('#st-cli', m) as HTMLInputElement).value = String(init.defaults.portCli);
    ($('#st-br', m) as HTMLInputElement).value = String(init.defaults.portBrowser);
    ($('#st-dt', m) as HTMLInputElement).value = String(init.defaults.portDesktop);
    ($('#st-url', m) as HTMLInputElement).value = init.defaults.startUrl;
  });
  $('[data-x=save]', m)!.addEventListener('click', async () => {
    const next: Settings = {
      ...s,
      regionOverride: ($('#st-region', m) as HTMLSelectElement).value,
      portCli: Number(($('#st-cli', m) as HTMLInputElement).value),
      portBrowser: Number(($('#st-br', m) as HTMLInputElement).value),
      portDesktop: Number(($('#st-dt', m) as HTMLInputElement).value),
      startUrl: ($('#st-url', m) as HTMLInputElement).value.trim(),
      sharePackageRoot: ($('#st-root', m) as HTMLInputElement).value.trim(),
    };
    const err = await api.saveSettings(next);
    if (err) {
      toast(err, 'err');
      return;
    }
    closeModal();
    onSaved(next);
  });
}

export function modalOpen() {
  return current !== null;
}
