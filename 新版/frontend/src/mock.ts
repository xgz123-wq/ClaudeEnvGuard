// 仅开发预览用（vite dev，不在 Wails 里时）：模拟后端，便于在浏览器里看各种场景。
// 场景用网址参数切换：?s=real（默认，读 dev/real.json）、?s=fresh（没配好的新电脑）、?s=split（出口不一致）。
// 记录页：?r=empty 没有记录；默认是一组覆盖四类、各种状态的假记录（按项还原会改这份内存数据）。
import type { LogRecord, Report, Result, UpdateView } from './types';
import { updateMock } from './mock-update';

const files = import.meta.glob('../dev/*.json', { eager: true, import: 'default' }) as Record<string, unknown>;
const real = (files['../dev/real.json'] as Report) ?? null;
const plan = files['../dev/plan.json'] ?? { actions: [], notes: [] };
const clean = files['../dev/clean.json'] ?? { kind: 'browser', title: '清理', items: [], totalMb: 0 };

const scenario = new URLSearchParams(location.search).get('s') ?? 'real';
const clone = <T>(x: T): T => JSON.parse(JSON.stringify(x));

function setStatus(rs: Result[], id: string, patch: Partial<Result>) {
  const r = rs.find((x) => x.id === id);
  if (r) Object.assign(r, patch);
}

function build(): Report {
  const rep = clone(real);
  const rs = rep.results ?? [];
  if (scenario === 'fresh') {
    rep.browsers = [];
    rep.selectedBrowser = '';
    for (const r of rs) {
      if (r.group === 'browser') Object.assign(r, { status: 'na', current: '', detail: '还没有专用浏览器。', fixId: '' });
    }
    setStatus(rs, 'B1', { status: 'fail', current: '没有找到专用浏览器', detail: '没在 ClaudeAccountBrowsers 下找到专用浏览器。', advice: '点「一键修复」新建一个。', fixId: 'browser.create' });
    setStatus(rs, 'C2', { status: 'warn', current: 'HTTPS_PROXY=未设置，HTTP_PROXY=未设置', detail: 'HTTPS_PROXY 没设：只能靠 TUN 规则分流，TUN 关掉就会直连', fixId: 'cli.proxy', advice: '点「一键修复」写入 settings.json。' });
    setStatus(rs, 'C3', { status: 'fail', current: 'settings.json：ANTHROPIC_BASE_URL=https://relay.example.com', detail: 'settings.json 里的 ANTHROPIC_BASE_URL 会把请求发到第三方中转', fixId: 'cli.thirdparty' });
    setStatus(rs, 'D2', { status: 'fail', current: '没有配置', detail: 'Desktop 没有代理策略，会按系统网络连接（TUN 关时直连）。', fixId: 'desktop.policy' });
    setStatus(rs, 'S1', { status: 'warn', current: 'China Standard Time（UTC+8）', detail: '系统是 UTC+8，出口所在地是 UTC-4：网页能读到系统时区，和 IP 对不上。', fixId: 'sys.timezone' });
    setStatus(rs, 'S4', { status: 'fail', current: '公网 IPv6 地址：1 个；能直连 IPv6，出口 2408:8207::1', detail: '有 IPv6 流量绕过代理直接出去（在 中国大陆）。', fixId: 'sys.ipv6' });
    rep.summary = { ...rep.summary, pass: 14, warn: 2, fail: 4, fixable: 6, overall: 'fail', headline: '有 4 项没配好，需要处理，另有 2 项建议处理', nextStep: '专用浏览器：可以一键修复。' };
  }
  if (scenario === 'split' && rep.exits) {
    rep.exits[1] = { ...rep.exits[1], ip: '203.0.113.7', countryCode: 'JP', country: '日本', city: 'Tokyo', trustScore: 62, residential: false };
    rep.exits[2] = { ...rep.exits[2], listening: false, ip: '', err: '端口 43867 没有程序在监听' };
    setStatus(rs, 'P3', { status: 'fail', current: '198.51.100.10（专用浏览器）；203.0.113.7（Claude Code）', detail: '不同入口从不同国家出去。' });
    setStatus(rs, 'P1', { status: 'fail', current: '43865 verge-mihomo；43866 verge-mihomo；43867 未监听', detail: '部分端口没开。' });
    rep.summary = { ...rep.summary, fail: 2, overall: 'fail', headline: '有 2 项没配好，需要处理', nextStep: '代理客户端与三个专用端口：确认 Clash Verge 已导入分享包的全局脚本。' };
  }
  return rep;
}

const L = 'C:\\Users\\x\\AppData\\Local';
const records: LogRecord[] = new URLSearchParams(location.search).get('r') === 'empty' ? [] : [
  { id: 'fix:20261024-101500', kind: 'fix', at: '2026-10-24 10:15', title: 'Claude Code 走专用端口 等 3 项', state: 'restorable', backupId: '20261024-101500', dir: L + '\\ClaudeEnvGuard\\备份\\20261024-101500', meta: '改之前已备份，可以按项还原',
    diffs: [
      { itemId: 'cli.proxy', item: 'Claude Code 走专用端口', what: 'HTTPS_PROXY', from: '未设置', to: 'http://127.0.0.1:43865', restorable: true },
      { itemId: 'cli.proxy', item: 'Claude Code 走专用端口', what: 'HTTP_PROXY', from: '未设置', to: 'http://127.0.0.1:43865', restorable: true },
      { itemId: 'sys.timezone', item: '系统时区跟随出口', what: '系统时区', from: 'China Standard Time', to: 'Eastern Standard Time', restorable: true },
      { itemId: 'desktop.policy', item: 'Claude Desktop 走 Desktop 专用端口', what: 'HKLM 策略 egressProxyUrl', from: '没有', to: 'http://127.0.0.1:43867', restorable: true },
    ] },
  { id: 'clean:清理-20261024-093000.json', kind: 'clean', at: '2026-10-24 09:30', title: '清理 Claude Code 登录', state: 'irreversible', dir: L + '\\ClaudeEnvGuard\\记录', meta: '已清理 2 项。',
    diffs: [{ what: '删除', from: '%USERPROFILE%\\.claude\\.credentials.json', to: '已删除' }, { what: '只删登录字段', from: '%USERPROFILE%\\.claude.json', to: '删了 oauthAccount，其他设置保留' }] },
  { id: 'update:更新记录-20261024-094215', kind: 'update', at: '2026-10-24 09:42', title: 'CLI + 插件 → 2.1.310', state: 'done', dir: L + '\\ClaudeNetworkGuard\\更新记录-20261024-094215', meta: '用时 99.8 秒 · 结果 passed',
    diffs: [{ what: 'Claude Code CLI', from: '2.1.309', to: '2.1.310' }, { what: '直连防护', from: '旧版本路径', to: '已刷新（4 个指纹，8 条规则）' }] },
  { id: 'update:更新记录-20261023-052309', kind: 'update', at: '2026-10-23 05:23', title: '失败：安装包审核 失败，退出码 1', state: 'failed', dir: L + '\\ClaudeNetworkGuard\\更新记录-20261023-052309', meta: '用时 61.2 秒 · 结果 failed',
    diffs: [{ what: '出错步骤', from: '', to: '安装包审核 失败，退出码 1' }] },
  { id: 'fix:20261015-053351', kind: 'fix', at: '2026-10-15 05:33', title: '区域格式与国家改为 en-US / 美国', state: 'restorable', backupId: '20261015-053351', dir: L + '\\ClaudeEnvGuard\\备份\\20261015-053351', meta: '改之前已备份，可以按项还原',
    diffs: [{ itemId: 'sys.region', item: '区域格式与国家改为 en-US / 美国', what: '区域格式', from: 'zh-CN', to: 'en-US', restorable: true }, { itemId: 'sys.region', item: '区域格式与国家改为 en-US / 美国', what: '国家或地区', from: 'CN', to: 'US', restorable: true }] },
  { id: 'restore:20261011-125030:0', kind: 'restore', at: '2026-10-11 12:50', title: '还原「区域格式与国家改为 en-US / 美国」', state: 'restored', dir: L + '\\ClaudeEnvGuard\\备份\\20261011-125030', meta: '按 20261011-125030 里的备份还原，只动选中的项',
    diffs: [{ itemId: 'sys.region', what: '区域格式', from: 'en-US', to: 'zh-CN' }, { itemId: 'sys.region', what: '国家或地区', from: 'US', to: 'CN' }] },
  { id: 'fix:20261011-125030', kind: 'fix', at: '2026-10-11 12:50', title: '区域格式与国家改为 en-US / 美国', state: 'restored', backupId: '20261011-125030', dir: L + '\\ClaudeEnvGuard\\备份\\20261011-125030', meta: '改之前已备份，可以按项还原',
    diffs: [{ itemId: 'sys.region', item: '区域格式与国家改为 en-US / 美国', what: '区域格式', from: 'zh-CN', to: 'en-US', restoredAt: '2026-10-11 12:50:43' }, { itemId: 'sys.region', item: '区域格式与国家改为 en-US / 美国', what: '国家或地区', from: 'CN', to: 'US', restoredAt: '2026-10-11 12:50:43' }] },
];

function mockRestore(backupId: string, items: string[]) {
  const r = records.find((x) => x.backupId === backupId);
  if (!r) return { ok: false, outcomes: [], message: '找不到这次修复的备份' };
  const now = new Date().toISOString().slice(0, 16).replace('T', ' ');
  const chosen = (r.diffs ?? []).filter((d) => d.itemId && items.includes(d.itemId) && d.restorable);
  for (const d of chosen) { d.restorable = false; d.restoredAt = now + ':00'; }
  const left = (r.diffs ?? []).some((d) => d.restorable);
  r.state = left ? 'partial' : 'restored';
  records.unshift({ id: 'restore:' + backupId + ':' + records.length, kind: 'restore', at: now, title: '还原「' + r.title + '」' + (left ? '中的 ' + items.length + ' 项' : ''), state: 'restored', dir: r.dir,
    meta: '按 ' + backupId + ' 里的备份还原，只动选中的项', diffs: chosen.map((d) => ({ ...d, from: d.to, to: d.from, restorable: false, restoredAt: '' })) });
  return { ok: true, outcomes: chosen.map((d) => ({ actionId: d.itemId, ok: true, message: '已还原：' + d.what })), message: left ? `已还原 ${items.length} 项，其余保持现状。` : '已全部还原。' };
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const listeners: Record<string, ((d: unknown) => void)[]> = {};

(window as any).runtime = {
  EventsOnMultiple: (name: string, cb: (d: unknown) => void) => { (listeners[name] ??= []).push(cb); return () => {}; },
  EventsOff: () => {},
  ClipboardSetText: async (t: string) => { console.log(t); return true; },
};

(window as any).go = {
  main: {
    App: {
      Init: async () => ({ version: '2.2.0', logDir: L + '\\ClaudeEnvGuard\\日志',
        paths: { settings: L + '\\ClaudeEnvGuard\\设置.json', backups: L + '\\ClaudeEnvGuard\\备份', logs: L + '\\ClaudeEnvGuard\\日志', records: L + '\\ClaudeEnvGuard\\记录', updates: L + '\\ClaudeNetworkGuard', manifest: L + '\\ClaudeNetworkGuard\\部署清单.json' }, settings: { portCli: 43865, portBrowser: 43866, portDesktop: 43867, selectedBrowser: 'KK-Claude', regionOverride: '', sharePackageRoot: '', startUrl: 'https://ip.net.coffee/claude/' }, note: '', regions: [{ id: 'us-west', label: '美国西部（洛杉矶）', countryCode: 'US', iana: 'America/Los_Angeles' }], isAdmin: false, portSource: '部署清单', shareRoot: L.replace(/\\AppData\\Local$/, '\\Desktop\\AI应用开发'), defaults: { portCli: 43865, portBrowser: 43866, portDesktop: 43867, startUrl: 'https://ip.net.coffee/claude/' } }),
      Check: async () => {
        const full = build();
        const partial = clone(full);
        partial.final = false;
        partial.exits = (partial.exits ?? []).map((e) => ({ ...e, ip: '', country: '', trustScore: undefined }));
        for (const r of partial.results ?? []) if (['P3', 'P4', 'P5', 'B8', 'C6', 'D4', 'S5'].includes(r.id)) r.pending = true;
        await sleep(300);
        (listeners.report ?? []).forEach((cb) => cb(partial));
        await sleep(1800);
        return full;
      },
      CancelCheck: async () => {},
      Plan: async () => plan,
      Apply: async (ids: string[]) => { await sleep(900); return { ok: true, backupId: '20261011-120000', message: '完成 ' + ids.length + ' 项。', outcomes: ids.map((id) => ({ actionId: id, ok: true, message: id + '：完成' })) }; },
      Backups: async () => [{ id: '20261011-120000', dir: 'C:\\Users\\x\\AppData\\Local\\ClaudeEnvGuard\\备份\\20261011-120000', createdAt: '2026-10-11 12:00:00', actions: ['区域格式与国家改为 en-US / 美国'], restored: false }],
      Restore: async () => ({ ok: true, message: '已全部还原。', outcomes: [{ ok: true, message: '已还原：region_set en-US' }] }),
      RestoreItems: async (id: string, items: string[]) => { await sleep(700); return mockRestore(id, items); },
      Records: async () => JSON.parse(JSON.stringify(records)).sort((a: LogRecord, b: LogRecord) => (a.at < b.at ? 1 : a.at > b.at ? -1 : 0)),
      DeepCheck: async () => {
        await sleep(2500);
        const items = [
          { status: 'PASS', item: 'Rule / TUN / Claude 规则', evidence: '基础验证 18/18；Clash 配置语法通过' },
          { status: 'PASS', item: '国内连通', evidence: '飞书 HTTP 200' },
          { status: 'PASS', item: 'ACCOUNT-US', evidence: '当前节点：美国 01；43866 出口：198.51.x.x' },
          { status: 'PASS', item: 'DNS 防旁路', evidence: 'strict-route=true；UDP/TCP 53 劫持已开启' },
          { status: 'WARN', item: 'Claude Desktop 自动更新', evidence: '策略没写 disableAutoUpdates（示例）' },
          { status: 'PASS', item: 'Claude Desktop / Code 直连防护', evidence: 'Desktop=3.6556.2.0；目标文件 4/4；8 条精确阻断规则有管理员 Verify' },
          { status: 'MANUAL', item: '网页最终验收', evidence: '专用 Chrome / Desktop 内置浏览器分别确认：Claude IP、DNS、WebRTC、IPv6、语言和时区全绿' },
        ];
        return { at: '17:40', secs: 15.2, ok: true, pass: 5, warn: 1, fail: 0, manual: 1, items, script: 'x', path: L + '\\ClaudeEnvGuard\\深度总检\\结果.json', summary: '日常运行检查通过；有 1 个防护提醒' };
      },
      LaunchDesktopEnglish: async () => {
        await sleep(1200);
        return { ok: false, started: false, script: 'x', message: '请先完全退出 Claude Desktop，再使用这个入口。已有进程不会重新加载语言参数。',
          lines: ['[PASS] 固定代理：HKLM 策略 -> http://127.0.0.1:43867', '[BLOCK] 请先完全退出 Claude Desktop，再使用这个入口。已有进程不会重新加载语言参数。'] };
      },
      PickDir: async () => 'C:\\Users\\x\\Desktop\\Claude环境分享版',
      CleanPlan: async () => clean,
      Clean: async () => { await sleep(500); return { ok: true, removed: ['x'], errors: [], message: '已清理 1 项。', logPath: L + '\\ClaudeEnvGuard\\记录\\清理-x.json' }; },
      SaveSettings: async () => '',
      SelectBrowser: async () => '',
      LaunchBrowser: async () => '',
      OpenPath: async () => '',
      ReportText: async () => 'report',
      Log: async (m: string) => console.log('[log]', m),
      ...updateMock((v: UpdateView) => (listeners.update ?? []).forEach((cb) => cb(v))),
    },
  },
};
