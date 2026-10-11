// 对 Wails 生成的绑定做一层薄封装：统一类型，并保证任何异常都变成可展示的结果，不让界面卡死。
import * as App from '../wailsjs/go/main/App';
import { EventsOn, ClipboardSetText } from '../wailsjs/runtime/runtime';
import type {
  ApplyResult, BackupInfo, CleanPlan, CleanResult, FixPlan, InitState, Report, Settings,
} from './types';

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));

export const api = {
  init: () => App.Init() as unknown as Promise<InitState>,
  check: (offline: boolean, browser: string) => App.Check(offline, browser) as unknown as Promise<Report>,
  cancel: () => App.CancelCheck(),
  plan: (alias: string) => App.Plan(alias) as unknown as Promise<FixPlan>,
  apply: async (ids: string[], alias: string): Promise<ApplyResult> => {
    try {
      return (await App.Apply(ids, alias)) as unknown as ApplyResult;
    } catch (e) {
      return { ok: false, outcomes: [], message: '修复调用失败：' + errText(e) };
    }
  },
  backups: () => App.Backups() as unknown as Promise<BackupInfo[]>,
  restore: async (id: string): Promise<ApplyResult> => {
    try {
      return (await App.Restore(id)) as unknown as ApplyResult;
    } catch (e) {
      return { ok: false, outcomes: [], message: '还原调用失败：' + errText(e) };
    }
  },
  cleanPlan: (kind: string) => App.CleanPlan(kind) as unknown as Promise<CleanPlan>,
  clean: async (kind: string, paths: string[]): Promise<CleanResult> => {
    try {
      return (await App.Clean(kind, paths)) as unknown as CleanResult;
    } catch (e) {
      return { ok: false, removed: [], errors: [errText(e)], message: '清理调用失败' };
    }
  },
  saveSettings: (s: Settings) => App.SaveSettings(s as never) as Promise<string>,
  selectBrowser: (name: string) => App.SelectBrowser(name) as Promise<string>,
  launchBrowser: (name: string, url: string) => App.LaunchBrowser(name, url) as Promise<string>,
  openPath: (p: string) => App.OpenPath(p) as Promise<string>,
  reportText: () => App.ReportText() as Promise<string>,
  log: (msg: string) => { try { App.Log(msg); } catch { /* 日志失败不影响界面 */ } },
  onReport: (fn: (r: Report) => void) => EventsOn('report', fn),
  copy: (text: string) => ClipboardSetText(text),
};

export { errText };
