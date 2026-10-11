// 「三入口 → 出口」线路图：左边三个入口，右边按出口 IP 合并的出口节点，连线颜色表示通不通。
import type { Exit, Report } from './types';
import { esc, entryLabel } from './util';

const ORDER: Exit['entry'][] = ['browser', 'cli', 'desktop'];

interface ExitGroup { key: string; exits: Exit[] }

function lineState(ex: Exit, final: boolean, offline: boolean): string {
  if (offline) return 'idle';
  if (!ex.listening) return 'down';
  if (ex.ip) return 'ok';
  return final ? 'err' : 'wait';
}

export function renderRoute(rep: Report | null, ports: Record<string, number>): string {
  const exits = ORDER.map((e) => rep?.exits?.find((x) => x.entry === e) ?? ({ entry: e, port: ports[e], listening: false } as Exit));
  const final = !!rep?.final;
  const offline = !!rep?.offline;

  const groups: ExitGroup[] = [];
  for (const ex of exits) {
    const key = ex.ip || '';
    if (!key) continue;
    const g = groups.find((x) => x.key === key);
    if (g) g.exits.push(ex);
    else groups.push({ key, exits: [ex] });
  }

  const W = 560, H = 172, rowH = 56, top = 4;
  const entryY = (i: number) => top + i * rowH + 24;
  const exitBoxH = groups.length <= 1 ? 128 : Math.min(60, (H - 8) / groups.length - 6);
  const exitY = (gi: number) => {
    if (groups.length <= 1) return H / 2;
    const span = (H - 8) / groups.length;
    return 4 + gi * span + span / 2;
  };
  const x1 = 172, x2 = W - 214;

  const paths = exits
    .map((ex, i) => {
      const st = lineState(ex, final, offline);
      const gi = groups.findIndex((g) => g.key === (ex.ip || ''));
      const y1 = entryY(i);
      const y2 = gi >= 0 ? exitY(gi) : y1;
      const xe = gi >= 0 ? x2 : x1 + 70;
      const mid = (x1 + xe) / 2;
      const d = `M ${x1} ${y1} C ${mid} ${y1}, ${mid} ${y2}, ${xe} ${y2}`;
      const cap = gi >= 0 ? '' : `<circle cx="${xe}" cy="${y2}" r="4" class="cap ${st}"/>`;
      return `<path d="${d}" class="wire ${st}"/>${cap}`;
    })
    .join('');

  const entries = exits
    .map((ex, i) => {
      const st = lineState(ex, final, offline);
      const note = !ex.listening && !offline ? '端口未监听' : '';
      return `<div class="node entry ${st}" style="top:${entryY(i) - 22}px">
        <div class="node-name">${esc(entryLabel[ex.entry])}</div>
        <div class="node-sub mono">:${esc(ex.port || ports[ex.entry])}${note ? ` · <span class="bad">${note}</span>` : ''}</div>
      </div>`;
    })
    .join('');

  let right = '';
  if (offline) {
    right = `<div class="node exit empty" style="top:${H / 2 - 40}px"><div class="node-name">离线模式</div><div class="node-sub">没有测出口</div></div>`;
  } else if (!groups.length) {
    const waiting = !final && exits.some((e) => e.listening);
    right = `<div class="node exit empty ${waiting ? 'wait' : ''}" style="top:${H / 2 - 40}px">
      <div class="node-name">${waiting ? '正在测出口…' : '没测到出口'}</div>
      <div class="node-sub">${waiting ? '经三个端口分别探测' : '检查代理客户端是否开着'}</div></div>`;
  } else {
    right = groups
      .map((g, gi) => {
        const ex = g.exits.find((x) => x.country) ?? g.exits[0];
        const y = exitY(gi);
        const compact = groups.length > 1;
        const trust = ex.trustScore != null
          ? `<span class="chip ${ex.trustScore >= 80 ? 'pass' : ex.trustScore >= 50 ? 'warn' : 'fail'}">评分 ${ex.trustScore}${ex.residential === true ? ' · 住宅' : ex.residential === false ? ' · 非住宅' : ''}</span>`
          : '';
        return `<div class="node exit ${compact ? 'compact' : ''}" style="top:${y - exitBoxH / 2}px;height:${exitBoxH}px">
          <div class="exit-head"><span class="cc">${esc(ex.countryCode || '??')}</span>
            <span class="node-name">${esc(ex.country || '未知地区')}${ex.city ? ' · ' + esc(ex.city) : ''}</span></div>
          <div class="mono ip">${esc(g.key)}</div>
          ${compact ? '' : `<div class="node-sub">${esc(ex.timeZone || '')}${ex.org ? ' · ' + esc(ex.org) : ''}</div><div class="exit-chips">${trust}</div>`}
        </div>`;
      })
      .join('');
  }

  const caption = groups.length > 1
    ? '<div class="route-cap bad">三个入口没有从同一个出口出去</div>'
    : groups.length === 1 && g1Count(groups) === exits.filter((e) => e.listening).length && exits.every((e) => e.listening)
      ? '<div class="route-cap good">三入口 · 一出口</div>'
      : '';

  return `<div class="route" style="height:${H}px">
    <svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" aria-hidden="true">${paths}</svg>
    ${entries}${right}${caption}
  </div>`;
}

function g1Count(groups: ExitGroup[]) {
  return groups[0]?.exits.length ?? 0;
}
