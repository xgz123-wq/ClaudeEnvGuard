// 设置页（照原型）：三个端口（只读，来自部署清单）、目标地区、专用浏览器、分享包、数据放在哪、关于。保存后回体检页并重新检查。
import { api } from './api';
import { ctx, defaultStartUrl, type Page } from './ctx';
import type { Settings } from './types';
import { $, esc, toast } from './util';

let root: HTMLElement;
let back: Page = 'check';
let browsers: () => string[] = () => [];

export function mountSettings(el: HTMLElement, listBrowsers: () => string[]) {
  root = el;
  browsers = listBrowsers;
  root.addEventListener('click', onClick);
}

// showSettings 每次进设置页都按当前设置重画，没保存的改动不留。
export function showSettings(from: Page) {
  if (from !== 'settings') back = from;
  const init = ctx.init;
  if (!init) {
    root.innerHTML = '<div class="set-wrap"><div class="g-loading">还在读设置…</div></div>';
    return;
  }
  const s = init.settings;
  const p = init.paths;
  const regions = [`<option value="">跟随出口 IP（推荐）</option>`]
    .concat(init.regions.map((r) => `<option value="${esc(r.id)}" ${s.regionOverride === r.id ? 'selected' : ''}>${esc(r.label)} · ${esc(r.iana)}</option>`)).join('');
  const names = browsers();
  if (s.selectedBrowser && !names.includes(s.selectedBrowser)) names.unshift(s.selectedBrowser);
  const brOpts = names.length
    ? names.map((n) => `<option value="${esc(n)}" ${n === s.selectedBrowser ? 'selected' : ''}>${esc(n)}</option>`).join('')
    : '<option value="">（还没有专用浏览器）</option>';
  const field = (label: string, input: string, btn = '') => `<label class="set-field"><span>${label}</span>${input}${btn || '<span></span>'}</label>`;
  // 端口只读：源头是 Clash，部署清单由一键总检从 Clash 抄下来；卫士不提供改端口的入口。
  const port = (v: number) => `<span class="mono set-path">${v}</span>`;
  const portNote = init.portSource === '部署清单'
    ? '来自部署清单；要改端口先在 Clash 里改，再跑一次一键总检刷新清单'
    : `还没有部署清单，暂按${esc(init.portSource || '默认值')}；跑一次一键总检会从 Clash 读出端口并生成清单`;
  const path = (label: string, v: string | undefined, open: string | undefined, note = '') => field(label,
    `<span class="mono set-path" title="${esc(v ?? '')}">${esc(v ?? '')}${note}</span>`,
    open ? `<button class="btn small ghost" data-open="${esc(open)}">打开</button>` : '');
  const dirOf = (f?: string) => (f ? f.replace(/[\\/][^\\/]*$/, '') : '');
  root.innerHTML = `<div class="set-wrap">
    <div class="set-top"><span class="set-title">设置</span>
      <div class="set-btns"><button class="btn ghost big" data-act="reset">恢复默认网址</button>
        <button class="btn ghost big" data-act="cancel">取消</button>
        <button class="btn primary big" data-act="save">保存并重新检查</button></div></div>
    <div class="set-grid">
      <section class="set-group"><span class="set-gh"><b>三个专用端口（只读）</b><span>${portNote}</span></span>
        ${field('专用浏览器', port(s.portBrowser))}${field('Claude Code', port(s.portCli))}${field('Claude Desktop', port(s.portDesktop))}</section>
      <section class="set-group"><span class="set-gh"><b>目标地区</b><span>「随 IP」的检查项（时区、语言、区域）按这里判断；一般跟随出口即可</span></span>
        ${field('地区', `<select id="st-region">${regions}</select>`)}</section>
      <section class="set-group"><span class="set-gh"><b>专用浏览器</b><span>体检默认查哪一个；打开、复核时先进哪个网址</span></span>
        ${field('默认检查', `<select id="st-browser">${brOpts}</select>`)}${field('起始网址', `<input id="st-url" value="${esc(s.startUrl)}" placeholder="${defaultStartUrl}">`)}</section>
      <section class="set-group"><span class="set-gh"><b>分享包</b><span>读「直连防护」状态、找三端更新脚本；留空 = 按部署清单，没有清单时在桌面和「文档」里找</span></span>
        ${field('分享包目录', `<input id="st-root" value="${esc(s.sharePackageRoot)}" placeholder="留空 = 按部署清单">`, '<button class="btn small ghost" data-act="pick">选…</button>')}
        ${field('当前使用', `<span class="mono set-path" title="${esc(init.shareRoot ?? '')}">${esc(init.shareRoot || '没找到分享包')}</span>`)}</section>
      <section class="set-group wide"><span class="set-gh"><b>数据放在哪</b><span>都在本机，不上传</span></span>
        ${path('设置', p?.settings, dirOf(p?.settings))}${path('修复备份', p?.backups, p?.backups)}${path('清理记录', p?.records, p?.records)}
        ${path('运行日志', p?.logs, p?.logs, '（保留 7 天）')}${path('更新记录', p?.updates ? p.updates + '\\更新记录-*' : '', p?.updates)}</section>
      <section class="set-group wide"><span class="set-gh"><b>部署清单</b><span>分享包脚本写（一键总检从 Clash 刷新端口、登记专用浏览器），卫士只读</span></span>
        ${path('部署清单', p?.manifest, dirOf(p?.manifest))}</section>
      <section class="set-group"><span class="set-gh"><b>关于</b><span>Go + Wails · 命令行版 ceg.exe</span></span>
        ${field('版本', `<span class="mono set-path">v${esc(init.version)}${init.isAdmin ? ' · 以管理员运行' : ''}</span>`, '<button class="btn small ghost" data-act="cli">复制命令行用法</button>')}</section>
    </div></div>`;
}

const val = (id: string) => ($(id, root) as HTMLInputElement).value.trim();

async function onClick(e: MouseEvent) {
  const t = e.target as HTMLElement;
  const o = t.closest<HTMLElement>('[data-open]');
  if (o) {
    const err = await api.openPath(o.dataset.open!);
    if (err) toast('打不开：' + err, 'err');
    return;
  }
  const b = t.closest<HTMLElement>('[data-act]');
  if (!b) return;
  const init = ctx.init;
  if (!init) return;
  switch (b.dataset.act) {
    case 'cancel':
      ctx.showPage(back);
      return;
    case 'reset':
      ($('#st-url', root) as HTMLInputElement).value = init.defaults.startUrl || defaultStartUrl;
      toast('已填回默认值，点「保存并重新检查」才生效', 'info');
      return;
    case 'pick': {
      const dir = await api.pickDir('选择分享包目录（Claude环境分享版）');
      if (dir) ($('#st-root', root) as HTMLInputElement).value = dir;
      return;
    }
    case 'cli':
      await api.copy('ceg check            全量检查\nceg fix --plan       只列修复计划\nceg restore --list   列出修复备份（含每一项）\nceg restore --id <备份> --items <动作id,...>   按项还原\nceg records          列出记录\nceg update --check   查三端版本\nceg clean code --plan  列清理清单');
      toast('已复制：ceg check / ceg fix --plan / ceg restore / ceg records / ceg update --check', 'ok');
      return;
    case 'save': {
      const next: Settings = {
        ...init.settings,
        regionOverride: ($('#st-region', root) as HTMLSelectElement).value,
        selectedBrowser: ($('#st-browser', root) as HTMLSelectElement).value || init.settings.selectedBrowser,
        startUrl: val('#st-url'),
        sharePackageRoot: val('#st-root'),
      };
      const err = await api.saveSettings(next);
      if (err) {
        toast(err, 'err');
        return;
      }
      init.settings = next;
      toast('已保存到 设置.json，正在按新设置重新检查', 'ok');
      ctx.showPage('check');
      ctx.runCheck();
      return;
    }
  }
}
