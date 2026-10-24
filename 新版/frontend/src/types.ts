// 与 Go 端 internal/model 的 JSON 字段一一对应。

export type Status = 'pass' | 'warn' | 'fail' | 'unknown' | 'na';
export type Kind = 'fixed' | 'ip' | 'info';
export type Group = 'overview' | 'browser' | 'cli' | 'desktop' | 'system';
export type Entry = 'browser' | 'cli' | 'desktop';

export interface Result {
  id: string;
  group: Group;
  title: string;
  kind: Kind;
  advanced?: boolean;
  status: Status;
  current?: string;
  target?: string;
  detail?: string;
  advice?: string;
  fixId?: string;
  pending?: boolean;
}

export interface Resolver { ip: string; countryCode?: string; isp?: string }
export interface DNSLeak { tested: boolean; resolvers?: Resolver[]; err?: string }

export interface Exit {
  entry: Entry;
  port: number;
  listening: boolean;
  ip?: string;
  countryCode?: string;
  country?: string;
  city?: string;
  timeZone?: string;
  asn?: string;
  org?: string;
  trustScore?: number;
  residential?: boolean;
  riskFlags?: string[];
  reach?: string;
  reachDetail?: string;
  dns?: DNSLeak;
  err?: string;
}

export interface Target {
  source: 'auto' | 'manual' | 'none';
  sourceText: string;
  countryCode?: string;
  country?: string;
  iana?: string;
  windowsTz?: string;
  lang?: string;
  acceptLang?: string;
  locale?: string;
}

export interface Summary {
  pass: number; warn: number; fail: number; unknown: number; na: number; fixable: number;
  headline: string; nextStep?: string; overall: Status;
}

export interface BrowserInfo { name: string; userDataDir: string; shortcut?: string; standard: boolean; lastUsed?: string }

export interface Report {
  version: string;
  startedAt: string;
  durationMs: number;
  offline: boolean;
  final: boolean;
  selectedBrowser?: string;
  browsers: BrowserInfo[] | null;
  exits: Exit[] | null;
  target: Target;
  results: Result[] | null;
  summary: Summary;
  notes?: string[];
}

export interface Change { what: string; from: string; to: string }
export interface FixAction {
  id: string; title: string; checkIds: string[]; changes: Change[] | null;
  needsAdmin: boolean; defaultOn: boolean; note?: string;
}
export interface FixPlan { target: Target; actions: FixAction[] | null; notes?: string[] }
export interface StepOutcome { actionId: string; op: string; ok: boolean; message: string }
export interface ApplyResult { backupId?: string; ok: boolean; outcomes: StepOutcome[] | null; message: string }
export interface BackupInfo { id: string; dir: string; createdAt: string; actions: string[] | null; restored: boolean }

export interface CleanItem { path: string; isDir: boolean; bytes: number; files: number; action: string; note?: string }
export interface CleanPlan { kind: string; title: string; items: CleanItem[] | null; blocked?: string; warning?: string; totalMb: number }
export interface CleanResult { ok: boolean; removed: string[] | null; errors: string[] | null; backupDir?: string; message: string }

export interface Settings {
  portCli: number; portBrowser: number; portDesktop: number;
  selectedBrowser: string; regionOverride: string; sharePackageRoot: string; startUrl: string;
}
export interface RegionChoice { id: string; label: string; countryCode: string; iana: string }
export interface InitState { version: string; settings: Settings; note: string; regions: RegionChoice[]; isAdmin: boolean; logDir?: string; defaults: Settings }

// ---------------------------------------------------------------- 更新页（Go 端 internal/update.View）

export type UPhase = 'never' | 'checking' | 'checkfail' | 'latest' | 'update' | 'blocked' | 'unsafe' | 'running' | 'done' | 'failed';
export type UStepState = 'wait' | 'run' | 'done' | 'fail' | 'skip';
export interface UProduct { key: string; name: string; installed: string; latest: string; behind: boolean }
export interface UTarget { name: string; state: string }
export interface UStep { id: string; phase: number; name: string; detail?: string; uac?: boolean; est: number; state: UStepState; secs: number }
export interface UProc { pid: number; path: string; kind: string }
export interface UBlocker { kind: string; title: string; detail: string; procs?: UProc[] | null; link?: boolean }
export interface UDownload { mb: number; totalMb: number; speed: number }
export interface UHistory { at: string; status: string; tone: 'pass' | 'info' | 'fail'; what: string; seconds: number; dir: string }
export interface UpdateView {
  phase: UPhase; title: string; line: string; hint: string; tier?: 'retry' | 'user' | 'ai';
  products: UProduct[] | null; targets?: UTarget[] | null; steps: UStep[] | null; blockers?: UBlocker[] | null;
  download?: UDownload | null; needsUac: boolean; elapsed: number; remaining: number; percent: number; estimate: number;
  error?: string; failedStep?: string; runDir?: string; aiText?: string; checkedAt?: string; checkSecs?: number;
  history: UHistory[] | null; script: string;
}
