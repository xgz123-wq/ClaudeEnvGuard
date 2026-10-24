// 仅开发预览用：模拟 Go 端 internal/update，便于在浏览器里看更新页的每种状态。
// 网址参数 ?u= 切换：update（默认，有更新）、cli（只有 CLI）、blocked、latest、neterr（下载断了）、fail（安全检查没过）、
// checkfail（查版本失败）、unsafe（指纹异常）、never（停在没查过）。点「一键更新」会按加速时间走完整个流程。
import type { UHistory, UProduct, UStep, UpdateView } from './types';

const scenario = new URLSearchParams(location.search).get('u') ?? 'update';
const SPEED = 8; // 1 秒真实时间 = 8 秒模拟时间（下载再快 4 倍）

const P = (key: string, name: string, installed: string, latest: string): UProduct => ({ key, name, installed, latest, behind: installed !== latest });

const history: UHistory[] = [
  { at: '10-24 14:27', status: 'up_to_date', tone: 'pass', what: '已是最新', seconds: 7.9, dir: 'C:\\Users\\x\\AppData\\Local\\ClaudeNetworkGuard\\更新记录-20261024-142740' },
  { at: '10-24 12:39', status: 'up_to_date', tone: 'pass', what: '已是最新', seconds: 26.4, dir: 'C:\\x\\更新记录-20261024-123911' },
  { at: '10-24 09:42', status: 'passed', tone: 'pass', what: 'CLI + 插件 → 2.1.310', seconds: 99.8, dir: 'C:\\x\\更新记录-20261024-094215' },
  { at: '10-24 09:39', status: 'plugin_update_needed', tone: 'info', what: '插件落后，停下等手点', seconds: 19.3, dir: 'C:\\x\\更新记录-20261024-093949' },
  { at: '10-23 05:40', status: 'passed', tone: 'pass', what: 'Desktop → 3.6556', seconds: 293.8, dir: 'C:\\x\\更新记录-20261023-054028' },
];

const targets = (unsafe = false) => [
  { name: 'Claude Desktop 主程序及内置浏览器', state: unsafe ? 'changed_unsigned' : 'unchanged' },
  { name: 'Claude Code npm 启动目标', state: 'unchanged' },
  { name: 'Claude Code npm 原生包', state: 'unchanged' },
  { name: 'Claude Code VS Code 插件', state: scenario === 'update' ? 'moved' : 'unchanged' },
];

function products(): UProduct[] {
  if (scenario === 'latest') return [P('desktop', 'Claude Desktop', '3.6556.2.0', '3.6556.2.0'), P('cli', 'Claude Code CLI', '2.1.310', '2.1.310'), P('plugin', 'VS Code 插件', '2.1.310', '2.1.310')];
  if (scenario === 'cli') return [P('desktop', 'Claude Desktop', '3.6556.2.0', '3.6556.2.0'), P('cli', 'Claude Code CLI', '2.1.310', '2.1.311'), P('plugin', 'VS Code 插件', '2.1.310', '2.1.310')];
  const plugin = scenario === 'blocked' ? P('plugin', 'VS Code 插件', '2.1.310', '2.1.311') : P('plugin', 'VS Code 插件', '2.1.311', '2.1.311');
  return [P('desktop', 'Claude Desktop', '3.6556.2.0', '3.6601.0.0'), P('cli', 'Claude Code CLI', '2.1.310', '2.1.311'), plugin];
}

function plan(): UStep[] {
  const S = (id: string, phase: number, name: string, est: number, extra: Partial<UStep> = {}): UStep => ({ id, phase, name, est, state: 'wait', secs: 0, ...extra });
  const cliOnly = scenario === 'cli';
  const steps = [S('check', 0, '查三端版本', 3, { detail: '三个版本源同时查；Claude 在运行就停下', state: 'done', secs: 1.9 }), S('cli', 1, 'npm 安装 CLI', 37, { detail: '只用 registry.npmjs.org' })];
  if (!cliOnly) steps.push(S('download', 1, '下载 Desktop 官方包', 180, { detail: '约 300 MB，经 43867 通道' }), S('inspect', 1, '审核安装包', 8, { detail: '签名、包家族、版本、架构' }));
  steps.push(S('hashes', 1, '登记 4 个程序指纹', 3, { detail: '文件变了必须有 Anthropic 签名' }));
  if (cliOnly) return steps;
  steps.push(
    S('uac', 2, '等你在 UAC 弹窗点「是」', 7, { detail: '整次只弹这一次；没看到弹窗就点任务栏上闪烁的盾牌图标' }),
    S('migrate', 3, '插件规则迁移到新目录', 4, { uac: true }),
    S('refresh', 3, 'CLI 指纹刷新', 10, { uac: true }),
    S('pkgcheck', 3, '复核安装包', 7, { uac: true }),
    S('install', 3, '安装新版 Desktop', 36, { uac: true }),
    S('verify', 3, '防护核验', 3, { uac: true, detail: '4 个程序、8 条规则' }),
    S('summary', 3, '只查动过的项', 5, { uac: true, detail: 'Desktop 策略与直连防护；原来全量总检约 18 秒' }),
    S('launch', 3, '启动条件检查', 5, { uac: true }),
    S('cleanup', 3, '清理旧安装包', 1, { uac: true, detail: '保留最新两版' }),
  );
  return steps;
}

const base = (): UpdateView => ({
  phase: 'never', title: '还没查过新版本', line: '打开卫士、体检完成后会自动查一次', hint: '', products: null, steps: null,
  needsUac: false, elapsed: 0, remaining: 0, percent: 0, estimate: 0, history, script: 'C:\\Users\\x\\Desktop\\AI应用开发\\docs\\配置\\Claude环境\\Desktop防护\\更新三端Claude.ps1',
});

function checked(): UpdateView {
  const steps = plan();
  const estimate = steps.reduce((a, s) => a + s.est, 0);
  const v: UpdateView = { ...base(), products: products(), targets: targets(), steps, checkedAt: '16:20', checkSecs: 1.9, estimate };
  switch (scenario) {
    case 'latest':
      return { ...v, phase: 'latest', title: '三端都是最新', line: '防护不用动。上次更新 10-24 09:42，用时 99.8 秒', steps: steps.slice(0, 1), estimate: 0 };
    case 'blocked':
      return {
        ...v, phase: 'blocked', title: '还差 2 件事要你先做', line: '做完点右边，卫士会重新检查并接着走', hint: '本次没有下载、没有改任何文件',
        blockers: [
          { kind: 'running', title: '退出 Claude', detail: '有 2 个 claude.exe 在运行：命令行 1 个、VS Code 插件 1 个；卫士不会替你强杀', procs: [] },
          { kind: 'plugin', title: '在 VS Code 里把插件更新到 2.1.311', detail: '插件 2.1.310 锁了版本不自动更新，这是唯一要手点的一步；点完退出 VS Code', link: true },
        ],
      };
    case 'unsafe':
      return { ...v, targets: targets(true), phase: 'unsafe', tier: 'ai', title: '受保护的程序有异常，先别更新', line: 'Claude Desktop 主程序及内置浏览器：文件变了但没有有效的 Anthropic 签名', hint: '卫士不绕过安全检查；AI 只读排查，要改会先问你', error: 'Claude Desktop 主程序及内置浏览器：文件变了但没有有效的 Anthropic 签名', aiText: '（模拟）复制给 AI 的内容' };
    case 'checkfail':
      return { ...base(), phase: 'checkfail', tier: 'retry', title: '没查到新版本', line: '官方入口无响应', hint: '多半是网络问题；连续失败再交给 AI', error: '官方入口无响应；检查 43867 通道。', checkedAt: '16:20', checkSecs: 60.2 };
    case 'cli':
      return { ...v, phase: 'update', title: '有 1 端可以更新', line: `会更新：Claude Code CLI 2.1.310 → 2.1.311；预计约 ${Math.round(estimate)} 秒`, hint: '不用弹 UAC' };
  }
  return { ...v, phase: 'update', needsUac: true, title: '有 2 端可以更新', line: '会更新：Claude Desktop 3.6556.2.0 → 3.6601.0.0、Claude Code CLI 2.1.310 → 2.1.311；预计约 5 分钟（含 Desktop 下载）', hint: '会弹一次 UAC，那就是确认' };
}

// ---------------------------------------------------------------- 模拟一次更新

let current: UpdateView = base();
let timer = 0;

function runView(steps: UStep[], t: number, from: UpdateView): UpdateView {
  let at = 0;
  let cur = -1;
  const out = steps.map((s, i) => {
    if (i === 0) { at += s.secs; return s; }
    const dur = s.est * 0.9;
    const x = { ...s };
    if (t >= at + dur) { x.state = 'done'; x.secs = Math.round(dur * 10) / 10; }
    else if (t >= at) { x.state = 'run'; x.secs = Math.round(t - at); if (cur < 0) cur = i; }
    at += dur;
    return x;
  });
  const total = at;
  const download = cur >= 0 && out[cur].id === 'download' ? { mb: Math.min(300, (out[cur].secs / (out[cur].est * 0.9)) * 300), totalMb: 300, speed: 1.8 } : null;
  const remaining = Math.max(2, Math.round(total - t));
  return {
    ...from, phase: 'running', steps: out, download, elapsed: Math.round(t), remaining, percent: Math.round((t / (t + remaining)) * 1000) / 10,
    title: (cur >= 0 ? out[cur].name : '收尾') + '…', line: `已用 ${Math.round(t)} 秒 · 预计还要 ${remaining} 秒（按最近几次记录估）`,
    hint: '可以最小化，做完窗口会自己弹回来', runDir: 'C:\\Users\\x\\AppData\\Local\\ClaudeNetworkGuard\\更新记录-20261024-162000', blockers: null,
  };
}

function failAt(steps: UStep[]): number {
  let at = 0;
  for (const s of steps) {
    if (scenario === 'neterr' && s.id === 'download') return at + s.est * 0.9 * 0.6;
    if (scenario === 'fail' && s.id === 'inspect') return at + 5;
    at += s.id === 'check' ? s.secs : s.est * 0.9;
  }
  return Infinity;
}

export function updateMock(emit: (v: UpdateView) => void) {
  const set = (v: UpdateView) => { current = v; emit(v); return v; };
  const start = (): UpdateView => {
    if (current.phase !== 'update') return current;
    const from = current;
    const steps = from.steps ?? [];
    const stopAt = failAt(steps);
    let t = 0;
    window.clearInterval(timer);
    timer = window.setInterval(() => {
      const v0 = runView(steps, t, from);
      const run = v0.steps?.find((s) => s.state === 'run');
      t += run?.id === 'download' ? SPEED * 4 : SPEED;
      if (t >= stopAt) {
        window.clearInterval(timer);
        const v = runView(steps, stopAt, from);
        const st = v.steps!.map((s) => (s.state === 'run' ? { ...s, state: 'fail' as const } : s.state === 'wait' ? { ...s, state: 'skip' as const } : s));
        if (scenario === 'neterr') {
          set({ ...v, steps: st, phase: 'failed', tier: 'retry', title: '这一步没成功，可以直接重试', line: 'Desktop 安装包下载失败；已完成的部分不会重做', hint: '连续失败 2 次再交给 AI', error: 'Desktop 安装包下载失败，curl 退出码 28；未完成文件已删除，重跑会整包重下。', failedStep: '下载 Desktop 官方包', percent: 0, remaining: 0 });
        } else {
          set({ ...v, steps: st, phase: 'failed', tier: 'ai', title: '安全检查没过，已停下', line: '安装包审核 失败；防护、日志、安装包都已保留', hint: '这类问题卫士不自己绕过；AI 只读排查，要改会先问你', error: '安装包审核 失败，退出码 1；日志：C:\\x\\安装包审核.log', failedStep: '审核安装包', aiText: '（模拟）复制给 AI 的内容', percent: 0, remaining: 0 });
        }
        return;
      }
      const v = runView(steps, t, from);
      if (!v.steps!.some((s) => s.state === 'run' || s.state === 'wait')) {
        window.clearInterval(timer);
        const after = (from.products ?? []).map((p) => (p.behind ? { ...p, installed: p.latest } : p));
        set({ ...v, phase: 'done', percent: 100, remaining: 0, download: null, products: after, title: `更新完成，用时 ${v.elapsed.toFixed(1)} 秒`, line: scenario === 'cli' ? '防护不用动：CLI 同路径更新按 Anthropic 签名放行，没弹 UAC' : '三端回读等于最新，防护核验 4/4 个程序、8/8 条规则通过', hint: '记录在 更新记录-20261024-162000\\记录.json' });
        return;
      }
      set(v);
    }, 1000 / 4);
    return set(runView(steps, 0, from));
  };
  return {
    UpdateView: async () => current,
    UpdateCheck: async () => {
      if (scenario === 'never') return set(base()); // 停在「从没查过」
      await new Promise((r) => setTimeout(r, 1600));
      return set(checked());
    },
    UpdateStart: async () => start(),
    UpdateRetry: async () => {
      await new Promise((r) => setTimeout(r, 1200));
      set(checked());
      return start();
    },
    UpdateOpenPlugin: async () => '',
  };
}
