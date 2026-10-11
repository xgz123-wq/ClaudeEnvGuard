// 小工具：转义、DOM 快捷方式、提示条。

export const esc = (s: unknown): string =>
  String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]!));

export const $ = <T extends HTMLElement = HTMLElement>(sel: string, root: ParentNode = document) =>
  root.querySelector(sel) as T | null;

export const $$ = <T extends HTMLElement = HTMLElement>(sel: string, root: ParentNode = document) =>
  Array.from(root.querySelectorAll(sel)) as T[];

export const h = (html: string): HTMLElement => {
  const t = document.createElement('template');
  t.innerHTML = html.trim();
  return t.content.firstElementChild as HTMLElement;
};

export const mb = (bytes: number) => (bytes / 1024 / 1024).toFixed(bytes > 100 * 1024 * 1024 ? 0 : 1) + ' MB';

let toastTimer = 0;
export function toast(msg: string, tone: 'ok' | 'err' | 'info' = 'info') {
  let el = $('#toast');
  if (!el) {
    el = h('<div id="toast" role="status" aria-live="polite"></div>');
    document.body.appendChild(el);
  }
  el.textContent = msg;
  el.className = 'show ' + tone;
  window.clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => el!.classList.remove('show'), 3200);
}

export const statusLabel: Record<string, string> = {
  pass: '通过', warn: '警告', fail: '失败', unknown: '未知', na: '不适用',
};

export const kindLabel: Record<string, string> = { fixed: '固定', ip: '随 IP', info: '信息' };

export const kindHint: Record<string, string> = {
  fixed: '固定：和地区无关，谁都应该这样配',
  ip: '随 IP：目标值按出口 IP 所在地推算，换节点后会跟着变',
  info: '信息：只展示现场情况，供参考',
};

export const entryLabel: Record<string, string> = { browser: '专用浏览器', cli: 'Claude Code', desktop: 'Claude Desktop' };
