// 记录页（照原型）：左边按时间列出修复、还原、更新、清理（可按类型筛），右边「改了什么 / 改之前 → 改之后」。
// 修复记录每一项有勾选框，「还原勾选的 N 项」只还原勾上的，其余保持现状；更新写明不能回退，清理写明不可恢复。
import { api } from './api';
import { ctx } from './ctx';
import type { LogDiff, LogKind, LogRecord } from './types';
import { $, esc, toast } from './util';

type Filter = 'all' | 'fix' | 'update' | 'clean';

const KIND: Record<LogKind, string> = { fix: '修复', restore: '还原', update: '更新', clean: '清理' };
const FILTERS: { id: Filter; label: string }[] = [
  { id: 'all', label: '全部' }, { id: 'fix', label: '修复与还原' }, { id: 'update', label: '更新' }, { id: 'clean', label: '清理' },
];

const rs = {
  list: [] as LogRecord[],
  loaded: false,
  loading: false,
  filter: 'all' as Filter,
  sel: '',
  unpicked: new Map<string, Set<string>>(), // 记录 id → 用户取消勾选的动作 id；默认全勾
  busy: false,
  seen: -1, // 上次看记录页时有几条；角标 = 之后新增的
};

let root: HTMLElement;

export function mountRecords(el: HTMLElement) {
  root = el;
  root.innerHTML = `<div class="rec-top"><div class="rec-filters" id="rec-filters"></div><span class="rec-tip">卫士对这台电脑做过的每一次改动，都按时间记在这里</span></div>
    <div class="rec-body"><div class="rec-list" id="rec-list"></div><section class="rec-detail" id="rec-detail"></section></div>`;
  root.addEventListener('click', onClick);
  root.addEventListener('change', onChange);
  ctx.refreshRecords = () => { load(); };
  load();
}

export async function load() {
  if (rs.loading) return;
  rs.loading = true;
  try {
    rs.list = await api.records();
  } catch (e) {
    api.log('读记录失败：' + String(e));
  }
  rs.loading = false;
  rs.loaded = true;
  if (rs.seen < 0 || ctx.page === 'records') rs.seen = rs.list.length;
  render();
  ctx.renderTabs();
}

export function recordsShown() {
  rs.seen = rs.list.length;
  load();
}

export function recordsBadge(): number {
  return rs.seen < 0 ? 0 : Math.max(0, rs.list.length - rs.seen);
}

export function recordsLastText(): string {
  return rs.loaded ? `共 ${rs.list.length} 条` : '';
}

function match(r: LogRecord, f: Filter) {
  return f === 'all' || r.kind === f || (f === 'fix' && r.kind === 'restore');
}

function pill(r: LogRecord): [string, string] {
  switch (r.state) {
    case 'restorable': return ['可还原', 'warn'];
    case 'partial': return ['部分还原', 'info'];
    case 'restored': return ['已还原', 'na'];
    case 'irreversible': return ['不可恢复', 'fail'];
    case 'failed': return ['没成功', 'fail'];
  }
  return ['完成', 'pass'];
}

function render() {
  if (!root) return;
  const list = rs.list.filter((r) => match(r, rs.filter));
  $('#rec-filters', root)!.innerHTML = FILTERS.map((f) => {
    const n = rs.list.filter((r) => match(r, f.id)).length;
    return `<button class="chip-btn ${rs.filter === f.id ? 'on' : ''}" data-filter="${f.id}">${f.label} ${n}</button>`;
  }).join('');
  const sel = list.find((r) => r.id === rs.sel) ?? list[0];
  $('#rec-list', root)!.innerHTML = list.length
    ? list.map((r, i) => {
      const [p, tone] = pill(r);
      return `<button class="rec-item k-${r.kind} ${sel && r.id === sel.id ? 'sel' : ''} in" style="animation-delay:${Math.min(i, 10) * 25}ms" data-rec="${esc(r.id)}">
        <span class="rec-dot"></span>
        <span class="rec-item-main"><span class="rec-item-title"><span class="kind-tag k-${r.kind}">${KIND[r.kind]}</span><span class="rec-title-text">${esc(r.title)}</span></span>
          <span class="rec-at mono">${esc(r.at)}</span></span>
        <span class="pill ${tone}">${p}</span></button>`;
    }).join('')
    : `<div class="rec-none">${rs.loaded ? '还没有记录。一键修复、还原、清理登录、三端更新之后都会记在这里。' : '读取中…'}</div>`;
  renderDetail(sel);
}

function picked(r: LogRecord, itemId: string) {
  return !rs.unpicked.get(r.id)?.has(itemId);
}

function pickedItems(r: LogRecord): string[] {
  const ids: string[] = [];
  for (const d of r.diffs ?? []) {
    if (d.restorable && d.itemId && !ids.includes(d.itemId) && picked(r, d.itemId)) ids.push(d.itemId);
  }
  return ids;
}

function diffRow(r: LogRecord, d: LogDiff, first: boolean, restorable: boolean) {
  let pick = '';
  if (restorable && first && d.itemId) {
    if (d.restorable) pick = `<input type="checkbox" data-pick="${esc(d.itemId)}" ${picked(r, d.itemId) ? 'checked' : ''} ${rs.busy ? 'disabled' : ''} aria-label="还原这一项">`;
    else if (d.restoredAt) pick = '<span class="tag done">已还原</span>';
  }
  const done = r.kind === 'fix' && !!d.restoredAt;
  return `<div class="rec-diff ${done ? 'done' : ''}"><span class="rec-pick">${pick}</span><span>${esc(d.what)}</span>
    <span class="mono rec-from">${esc(d.from)}</span><span class="rec-arrow">→</span><span class="mono rec-to">${esc(d.to)}</span></div>`;
}

function renderDetail(r: LogRecord | undefined) {
  const el = $('#rec-detail', root)!;
  if (!r) {
    el.innerHTML = `<div class="rec-head"><span class="rec-detail-title">还没有记录</span></div>`;
    return;
  }
  const restorable = r.kind === 'fix' && (r.state === 'restorable' || r.state === 'partial');
  const diffs = r.diffs ?? [];
  const items = [...new Set(diffs.map((d) => d.itemId).filter(Boolean))];
  let rows = '';
  const seen = new Set<string>();
  for (const d of diffs) {
    const first = !!d.itemId && !seen.has(d.itemId);
    if (d.itemId) seen.add(d.itemId);
    // 一次修复改了好几项时，每项先列一行标题，勾选框放在标题上
    if (first && items.length > 1) {
      rows += `<label class="rec-group">${restorable && d.restorable ? `<input type="checkbox" data-pick="${esc(d.itemId!)}" ${picked(r, d.itemId!) ? 'checked' : ''} ${rs.busy ? 'disabled' : ''} aria-label="还原这一项">` : d.restoredAt ? '<span class="tag done">已还原</span>' : ''}<span>${esc(d.item || d.itemId)}</span></label>`;
      rows += diffRow(r, d, false, restorable);
    } else {
      rows += diffRow(r, d, first, restorable && items.length <= 1);
    }
  }
  let note = '', tone = 'info';
  const btns: string[] = [];
  if (restorable) {
    note = '勾哪几项就只还原哪几项，没勾的保持现状；还原后自动重新检查。比如只想把时区改回去，就只勾「系统时区」。';
    const n = pickedItems(r).length;
    btns.push(`<button class="btn primary big" data-act="restore" ${n && !rs.busy ? '' : 'disabled'}>${rs.busy ? '正在还原…' : n ? `还原勾选的 ${n} 项` : '先勾要还原的项'}</button>`);
  } else if (r.kind === 'fix' && r.state === 'restored') note = '这次修复已经还原，上面的改动现在不生效。';
  else if (r.kind === 'fix' && r.state === 'failed') { note = '这次修复没有改成，当时已自动撤回，不用还原。'; tone = 'warn'; }
  else if (r.kind === 'restore') note = '按当时的备份写回了改之前的值。';
  else if (r.kind === 'update') { note = '更新不能回退：备份里没有旧安装包。要看每步耗时和原始日志，打开记录目录里的 记录.json。'; tone = 'warn'; }
  else if (r.kind === 'clean') { note = '登录凭据删了不留备份，所以这条只能看、不能还原。'; tone = 'fail'; }
  if (r.dir) btns.push(`<button class="btn ghost big" data-open="${esc(r.dir)}">${r.kind === 'fix' || r.kind === 'restore' ? '打开备份目录' : '打开记录目录'}</button>`);
  if (rs.busy) note = '正在还原…如果弹出 Windows 授权窗口，请点「是」';
  el.innerHTML = `<div class="rec-head"><span class="rec-head-title"><span class="kind-tag k-${r.kind}">${KIND[r.kind]}</span><span class="rec-detail-title">${esc(r.title)}</span></span>
      <span class="rec-meta">${esc(r.at)}${r.meta ? ' · ' + esc(r.meta) : ''}</span></div>
    <div class="rec-table"><div class="rec-diff head"><span>${restorable ? '还原' : ''}</span><span>改了什么</span><span>改之前</span><span></span><span>改之后</span></div>
      <div class="rec-rows">${rows || '<div class="rec-diff"><span></span><span>没有记下具体改动</span></div>'}</div></div>
    ${note ? `<div class="rec-note ${tone}">${esc(note)}</div>` : ''}
    <div class="rec-btns">${btns.join('')}</div>`;
}

async function onClick(e: MouseEvent) {
  const t = e.target as HTMLElement;
  const f = t.closest<HTMLElement>('[data-filter]');
  if (f) {
    rs.filter = f.dataset.filter as Filter;
    rs.sel = '';
    return render();
  }
  const it = t.closest<HTMLElement>('[data-rec]');
  if (it) {
    rs.sel = it.dataset.rec!;
    return render();
  }
  const o = t.closest<HTMLElement>('[data-open]');
  if (o) {
    const err = await api.openPath(o.dataset.open!);
    if (err) toast('打不开目录：' + err, 'err');
    return;
  }
  const b = t.closest<HTMLElement>('[data-act="restore"]');
  if (b && !(b as HTMLButtonElement).disabled) restore();
}

function onChange(e: Event) {
  const t = e.target as HTMLInputElement;
  if (!t.dataset.pick) return;
  const r = current();
  if (!r) return;
  const set = rs.unpicked.get(r.id) ?? new Set<string>();
  if (t.checked) set.delete(t.dataset.pick);
  else set.add(t.dataset.pick);
  rs.unpicked.set(r.id, set);
  renderDetail(r);
}

function current(): LogRecord | undefined {
  const list = rs.list.filter((r) => match(r, rs.filter));
  return list.find((r) => r.id === rs.sel) ?? list[0];
}

async function restore() {
  const r = current();
  if (!r?.backupId) return;
  const ids = pickedItems(r);
  if (!ids.length) return;
  rs.busy = true;
  rs.sel = r.id; // 还原完停在这条修复上，看得到哪几项已还原
  renderDetail(r);
  const res = await api.restoreItems(r.backupId, ids);
  rs.busy = false;
  rs.unpicked.delete(r.id);
  api.log(`按项还原 ${r.backupId} ${ids.join(',')}：${res.message}`);
  const total = new Set((r.diffs ?? []).filter((d) => d.restorable).map((d) => d.itemId)).size;
  toast(res.ok ? (ids.length < total ? `已还原 ${ids.length} 项，其余保持；正在重新检查` : '已全部还原；正在重新检查') : res.message, res.ok ? 'ok' : 'err');
  await load();
  ctx.runCheck();
}
