// 仅开发预览用（vite dev，不在 Wails 里时）：模拟后端，便于在浏览器里看各种场景。
// 场景用网址参数切换：?s=real（默认，读 dev/real.json）、?s=fresh（没配好的新电脑）、?s=split（出口不一致）。
import type { Report, Result } from './types';

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
      Init: async () => ({ version: '2.0.0', settings: { portCli: 43865, portBrowser: 43866, portDesktop: 43867, selectedBrowser: 'KK-Claude', regionOverride: '', sharePackageRoot: '', startUrl: 'https://ip.net.coffee/claude/' }, note: '', regions: [{ id: 'us-west', label: '美国西部（洛杉矶）', countryCode: 'US', iana: 'America/Los_Angeles' }], isAdmin: false, defaults: { portCli: 43865, portBrowser: 43866, portDesktop: 43867, startUrl: 'https://ip.net.coffee/claude/' } }),
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
      CleanPlan: async () => clean,
      Clean: async () => ({ ok: true, removed: ['x'], errors: [], message: '已清理 1 项。' }),
      SaveSettings: async () => '',
      SelectBrowser: async () => '',
      LaunchBrowser: async () => '',
      OpenPath: async () => '',
      ReportText: async () => 'report',
      Log: async (m: string) => console.log('[log]', m),
    },
  },
};
