// 「三入口 → 出口」线路图（照原型的紧凑版）：左边三个入口，右边按出口 IP 合并的出口，连线颜色表示通不通；检查中连线流动。
import type { Exit, Report } from './types';
import { esc, entryLabel } from './util';

const ORDER: Exit['entry'][] = ['browser', 'cli', 'desktop'];
const ENTRY_Y = [14, 60, 106];

interface ExitGroup { key: string; exits: Exit[] }

type LineState = 'ok' | 'down' | 'err' | 'wait' | 'idle' | 'bad';

function lineState(ex: Exit, rep: Report | null, checking: boolean): LineState {
  if (!rep) return 'wait';
  if (rep.offline && !checking) return 'idle';
  if (ex.ip) return 'ok';
  if (!rep.final || checking) return 'wait';
  return ex.listening ? 'err' : 'down';
}

function exitYs(n: number): number[] {
  if (n <= 1) return [60];
  if (n === 2) return [28, 92];
  return ENTRY_Y;
}

export interface RouteView { html: string; note: string; split: boolean }

export function renderRoute(rep: Report | null, ports: Record<string, number>, checking: boolean): RouteView {
  const exits = ORDER.map((e) => rep?.exits?.find((x) => x.entry === e) ?? ({ entry: e, port: ports[e], listening: false } as Exit));
  const final = !!rep?.final && !checking;
  const offline = !!rep?.offline && !checking;

  const groups: ExitGroup[] = [];
  for (const ex of exits) {
    if (!ex.ip) continue;
    const g = groups.find((x) => x.key === ex.ip);
    if (g) g.exits.push(ex);
    else groups.push({ key: ex.ip, exits: [ex] });
  }
  const ys = exitYs(groups.length);
  const split = groups.length > 1;
  // 出口不一致时以专用浏览器的出口为准（网页版 Claude 看到的就是它），没有就取入口最多的那个；其余标成警告色
  const refIdx = (() => {
    const b = groups.findIndex((g) => g.exits.some((x) => x.entry === 'browser'));
    if (b >= 0) return b;
    let best = 0;
    groups.forEach((g, i) => { if (g.exits.length > groups[best].exits.length) best = i; });
    return best;
  })();

  const states = exits.map((ex) => lineState(ex, rep, checking));
  const entries = exits.map((ex, i) => {
    const s = states[i];
    return `<div class="r-entry ${s}"><span class="r-name">${esc(entryLabel[ex.entry])}</span>
      <span class="r-port mono">:${esc(ex.port || ports[ex.entry])}${s === 'down' ? ' · 没监听' : ''}</span></div>`;
  }).join('');

  const wires = exits.map((ex, i) => {
    const s = states[i];
    const y1 = ENTRY_Y[i];
    if (s === 'down') return `<path d="M0 ${y1} L60 ${y1}" class="r-wire down"/>`;
    const gi = groups.findIndex((g) => g.key === ex.ip);
    const y2 = gi >= 0 ? ys[gi] : 60;
    const odd = split && gi >= 0 && gi !== refIdx;
    const cls = s === 'ok' && odd ? 'bad' : s;
    return `<path d="M0 ${y1} C100 ${y1}, 100 ${y2}, 200 ${y2}" class="r-wire ${cls}"/>`;
  }).join('');

  let right = '';
  if (offline) {
    right = '<div class="r-exit empty"><span class="r-name">离线模式</span><span class="r-note">没有测出口</span></div>';
  } else if (!groups.length) {
    const waiting = checking || !final;
    right = waiting
      ? '<div class="r-exit empty wait"><span class="r-name">测出口中…</span><span class="r-note">经三个端口分别探测</span></div>'
      : '<div class="r-exit empty"><span class="r-name">没测到出口</span><span class="r-note">检查 Clash 是否开着</span></div>';
  } else {
    right = groups.map((g, gi) => {
      const ex = g.exits.find((x) => x.country) ?? g.exits[0];
      const place = `${ex.country || '未知地区'}${ex.city ? ' · ' + ex.city : ''}`;
      let note = '';
      if (split) note = g.exits.map((x) => entryLabel[x.entry]).join('、');
      else if (ex.trustScore != null) note = `评分 ${ex.trustScore}${ex.residential === true ? ' · 住宅' : ex.residential === false ? ' · 非住宅' : ''}`;
      const odd = split && gi !== refIdx;
      return `<div class="r-exit ${odd ? 'bad' : 'ok'}"><span class="r-name">${esc(place)}</span>
        <span class="r-ip mono">${esc(g.key)}</span>${note ? `<span class="r-note">${esc(note)}</span>` : ''}</div>`;
    }).join('');
  }

  let note = '';
  if (checking) note = rep ? '测线路…' : '';
  else if (offline) note = '离线模式';
  else if (split) note = '三个入口没从同一个出口出去';
  else if (states.some((s) => s === 'down')) note = '有入口没监听';
  else if (groups.length === 1 && groups[0].exits.length === 3) note = '三入口 · 一出口';

  const html = `<div class="r-grid">
    <div class="r-entries">${entries}</div>
    <svg viewBox="0 0 200 120" preserveAspectRatio="none" aria-hidden="true">${wires}</svg>
    <div class="r-exits">${right}</div>
  </div>`;
  return { html, note, split };
}
