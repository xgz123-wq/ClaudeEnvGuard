// ============================================================
// Clash Verge 订阅级脚本模板：固定链式代理节点（入口节点 → socks5 出口）
//
// 用法：只改下方「三项配置」，整份粘贴到目标订阅的「订阅级脚本」里。
// 原理：mihomo 已移除 relay 组类型，链式代理用普通节点 + dialer-proxy 实现。
// 效果：链式节点出现在代理组里，像普通节点一样点选即用，无需再开链式代理页。
// ============================================================

// ---------- 三项配置（只改这里） ----------

// 1) 入口节点：必须与订阅里的节点全名一字不差（含国旗 emoji、倍率后缀如 | x0.8）
var ENTRY_NODE = "🇭🇰 香港Z06 | x0.8";

// 2) 出口节点：对应 socks5://用户名:密码@IP:端口
var EXIT = {
  name: "美国纽约",          // 出口节点显示名，可自定
  server: "填出口IP",
  port: 0,          // 填端口
  username: "填用户名",
  password: "填密码"
};

// 3) 链式节点显示名（GUI 里点选时看到的名字，可自定）
var CHAIN_NAME = "🇺🇸 纽约住宅(链式)";

// ---------- 以下为固定逻辑，一般不用改 ----------

// 需要把链式节点顶到列表首位的组；组不存在时自动跳过
// 默认注入 ACCOUNT-US（全局脚本所建）和订阅自带的"🔰 选择节点"；订阅组名不同就改成实际组名
var TARGET_GROUPS = ["ACCOUNT-US", "🔰 选择节点"];

function main(config, profileName) {
  if (!Array.isArray(config.proxies)) {
    config.proxies = [];
  }

  // 出口节点（幂等：已存在则跳过）
  if (!config.proxies.some(function (p) { return p && p.name === EXIT.name; })) {
    config.proxies.push({
      name: EXIT.name,
      type: "socks5",
      server: EXIT.server,
      port: EXIT.port,
      username: EXIT.username,
      password: EXIT.password,
      udp: true
    });
  }

  // 链式节点 = 出口节点 + dialer-proxy 指向入口节点
  config.proxies = config.proxies.filter(function (p) { return p && p.name !== CHAIN_NAME; });
  config.proxies.push({
    name: CHAIN_NAME,
    type: "socks5",
    server: EXIT.server,
    port: EXIT.port,
    username: EXIT.username,
    password: EXIT.password,
    udp: true,
    "dialer-proxy": ENTRY_NODE
  });

  // 注入到订阅自带的选择器组，GUI 里可直接点选
  if (!Array.isArray(config["proxy-groups"])) {
    config["proxy-groups"] = [];
  }
  TARGET_GROUPS.forEach(function (groupName) {
    var group = null;
    for (var i = 0; i < config["proxy-groups"].length; i++) {
      if (config["proxy-groups"][i] && config["proxy-groups"][i].name === groupName) {
        group = config["proxy-groups"][i];
        break;
      }
    }
    if (!group) {
      return;
    }
    if (!Array.isArray(group.proxies)) {
      group.proxies = [];
    }
    group.proxies = group.proxies.filter(function (name) { return name !== CHAIN_NAME; });
    group.proxies.unshift(CHAIN_NAME);
  });

  return config;
}
