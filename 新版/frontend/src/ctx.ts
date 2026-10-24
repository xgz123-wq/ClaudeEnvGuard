// 各页共用的状态和跨页动作：页面模块之间不直接互相 import，经这里转一下，避免循环依赖。
import type { InitState } from './types';

export type Page = 'check' | 'update' | 'records' | 'settings';

export const ctx = {
  init: null as InitState | null,
  page: 'check' as Page,
  // 由 main.ts 填上
  showPage: (_p: Page) => {},
  renderTabs: () => {},
  // 由 check.ts 填上：保存设置、还原之后按新状态重新检查
  runCheck: (_offline?: boolean) => {},
  // 由 records.ts 填上：修复、还原、清理之后刷新记录页
  refreshRecords: () => {},
  // 由 check.ts 填上：⋯ 菜单里的「深度总检」
  openDeep: () => {},
};

export const defaultStartUrl = 'https://ip.net.coffee/claude/';

export function startUrl(): string {
  return ctx.init?.settings.startUrl || ctx.init?.defaults.startUrl || defaultStartUrl;
}
