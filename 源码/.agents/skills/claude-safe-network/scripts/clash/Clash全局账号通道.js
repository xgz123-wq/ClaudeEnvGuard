function main(config, profileName) {
  var groupName = "ACCOUNT-US";
  var listenerName = "account-us-socks";
  var inboundRule = "IN-NAME," + listenerName + "," + groupName;
  var accountDnsServers = [
    "https://1.1.1.1/dns-query#" + groupName,
    "https://8.8.8.8/dns-query#" + groupName
  ];
  var accountDnsDomains = [
    "+.anthropic.com",
    "+.claude.ai",
    "+.claude.com",
    "+.claude.app",
    "+.clau.de",
    "+.claudemcpclient.com",
    "+.claudemcpcontent.com",
    "+.claudeusercontent.com"
  ];
  var legacyAccountDnsDomains = [
    "+.sentry.io",
    "+.statsigapi.net",
    "+.datadoghq.com",
    "+.intercom.io",
    "+.intercomcdn.com"
  ];
  var claudeRuleMatchers = [
    "DOMAIN-SUFFIX,anthropic.com",
    "DOMAIN-SUFFIX,claude.ai",
    "DOMAIN-SUFFIX,claude.com",
    "DOMAIN-SUFFIX,claude.app",
    "DOMAIN-SUFFIX,clau.de",
    "DOMAIN-SUFFIX,claudemcpclient.com",
    "DOMAIN-SUFFIX,claudemcpcontent.com",
    "DOMAIN-SUFFIX,claudeusercontent.com",
    "DOMAIN,servd-anthropic-website.b-cdn.net",
    "DOMAIN,anthropic.com.cdn.cloudflare.net",
    "DOMAIN,anthropic.auth0.com",
    "DOMAIN,anthropic-com.ghost.io"
  ];
  var claudeRules = claudeRuleMatchers.map(function (matcher) {
    return matcher + "," + groupName;
  }).concat([
    "IP-CIDR,160.79.104.0/21," + groupName + ",no-resolve",
    "IP-CIDR6,2607:6bc0::/32," + groupName + ",no-resolve",
    "IP-ASN,399358," + groupName + ",no-resolve"
  ]);
  // 浏览器/应用自带加密 DNS 的端点（DoH 走 443，不被 53 劫持）强制直连：
  // 落兜底规则会进美国线路，导致国内域名被解析成海外 IP（2026-09-28 Edge 实例）。
  var dohRuleMatchers = [
    "DOMAIN-SUFFIX,cloudflare-dns.com",
    "DOMAIN-SUFFIX,dns.google"
  ];
  var dohRules = dohRuleMatchers.map(function (matcher) {
    return matcher + ",DIRECT";
  });
  var legacyMatchers = claudeRuleMatchers.concat([
    "DOMAIN,claude.com",
    "DOMAIN,clau.de",
    "DOMAIN,claudemcpclient.com",
    "DOMAIN,claudemcpcontent.com",
    "DOMAIN,claudeusercontent.com",
    "DOMAIN,sentry.io",
    "DOMAIN,statsigapi.net",
    "DOMAIN,cdn.anthropic.com",
    "DOMAIN,mcp.anthropic.com",
    "DOMAIN,console.anthropic.com",
    "DOMAIN,workbench.anthropic.com"
  ]).concat([
    "DOMAIN-SUFFIX,sentry.io",
    "DOMAIN-SUFFIX,statsigapi.net",
    "DOMAIN,browser-intake-us5-datadoghq.com",
    "DOMAIN-KEYWORD,datadog",
    "DOMAIN-KEYWORD,sift",
    "DOMAIN-SUFFIX,intercom.io",
    "DOMAIN-SUFFIX,intercomcdn.com",
    "DOMAIN,cdn.usefathom.com"
  ]);

  function isManagedClaudeRule(rule) {
    if (typeof rule !== "string") {
      return false;
    }
    if (rule === inboundRule) {
      return true;
    }
    if (rule.indexOf("IP-CIDR,160.79.104.0/21,") === 0 ||
        rule.indexOf("IP-CIDR6,2607:6bc0::/32,") === 0 ||
        rule.indexOf("IP-ASN,399358,") === 0) {
      return true;
    }
    return legacyMatchers.some(function (matcher) {
      return rule.indexOf(matcher + ",") === 0;
    });
  }

  function isManagedDohRule(rule) {
    if (typeof rule !== "string") {
      return false;
    }
    return dohRuleMatchers.some(function (matcher) {
      return rule.indexOf(matcher + ",DIRECT") === 0;
    });
  }

  // 订阅可能注入邮件端口无条件 DIRECT；精确过滤后让它们继续按 CN / MATCH 规则分流。
  function isUnconditionalMailDirectRule(rule) {
    if (typeof rule !== "string") {
      return false;
    }
    return /^DST-PORT,(25|110|143|465|587|993|995),DIRECT(?:,.*)?$/i.test(
      rule.replace(/\s+/g, "")
    );
  }

  if (!Array.isArray(config["proxy-groups"])) {
    config["proxy-groups"] = [];
  }
  config["proxy-groups"] = config["proxy-groups"].filter(function (group) {
    return !group || group.name !== groupName;
  });
  config["proxy-groups"].unshift({
    name: groupName,
    type: "select",
    "include-all-proxies": true,
    "exclude-type": "Direct|Reject"
  });

  // 只加固 Claude 链路：相关域名使用加密 DNS，且 DoH 连接明确经过 ACCOUNT-US。
  // 普通/国内 DNS 保留订阅与 Clash 的通用解析器，不绑定任何专用代理组。
  if (!config.dns || typeof config.dns !== "object") {
    config.dns = {};
  }
  if (!config.dns["nameserver-policy"] ||
      typeof config.dns["nameserver-policy"] !== "object" ||
      Array.isArray(config.dns["nameserver-policy"])) {
    config.dns["nameserver-policy"] = {};
  }
  legacyAccountDnsDomains.forEach(function (domain) {
    delete config.dns["nameserver-policy"][domain];
  });
  accountDnsDomains.forEach(function (domain) {
    config.dns["nameserver-policy"][domain] = accountDnsServers.slice();
  });

  // 2026-09-11 DNS 分层：不覆盖 nameserver、direct-nameserver 或 fallback。
  // 这样飞书等普通应用的 DNS 不依赖 ACCOUNT-US；Claude 域名仍由上方 policy 加固。
  config.dns.enable = true;

  // Windows 多网卡 DNS 旁路防护：ETW 已确认 WLAN 与 TUN 并行代查。
  config.tun = config.tun || {};
  config.tun["strict-route"] = true;
  config.tun["dns-hijack"] = ["any:53", "tcp://any:53"];

  if (!Array.isArray(config.listeners)) {
    config.listeners = [];
  }
  config.listeners = config.listeners.filter(function (listener) {
    return !listener || listener.name !== listenerName;
  });
  config.listeners.unshift({
    name: listenerName,
    type: "socks",
    listen: "127.0.0.1",
    port: 43866,
    udp: false
  });

  // Claude Desktop：HTTP 专用入口直接进入账号组，不改变其他应用的 DNS/分流。
  config.listeners = config.listeners.filter(function (listener) {
    return !listener || listener.name !== "claude-desktop-http";
  });
  config.listeners.unshift({
    name: "claude-desktop-http",
    type: "http",
    listen: "127.0.0.1",
    port: 43867,
    proxy: groupName,
    users: []
  });

  if (!Array.isArray(config.rules)) {
    config.rules = [];
  }
  config.rules = config.rules.filter(function (rule) {
    return !isManagedClaudeRule(rule) &&
      !isManagedDohRule(rule) &&
      !isUnconditionalMailDirectRule(rule);
  });
  config.rules = [inboundRule].concat(claudeRules, dohRules, config.rules);

  // 使用 Mihomo 官方生态 MetaCubeX 数据，每日更新；失败时继续保留本地旧文件。
  config["geo-auto-update"] = true;
  config["geo-update-interval"] = 24;
  if (!config["geox-url"]) {
    config["geox-url"] = {};
  }
  config["geox-url"].geoip = "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.dat";
  config["geox-url"].geosite = "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat";
  config["geox-url"].mmdb = "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/country.mmdb";
  config["geox-url"].asn = "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/GeoLite2-ASN.mmdb";

  return config;
}
