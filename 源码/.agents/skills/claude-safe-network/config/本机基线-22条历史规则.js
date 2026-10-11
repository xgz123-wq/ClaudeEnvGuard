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
    "+.clau.de",
    "+.claudemcpclient.com",
    "+.claudemcpcontent.com",
    "+.claudeusercontent.com",
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
    "DOMAIN-SUFFIX,clau.de",
    "DOMAIN-SUFFIX,claudemcpclient.com",
    "DOMAIN-SUFFIX,claudemcpcontent.com",
    "DOMAIN-SUFFIX,claudeusercontent.com",
    "DOMAIN,servd-anthropic-website.b-cdn.net",
    "DOMAIN,anthropic.com.cdn.cloudflare.net",
    "DOMAIN,anthropic.auth0.com",
    "DOMAIN,anthropic-com.ghost.io",
    "DOMAIN-SUFFIX,sentry.io",
    "DOMAIN-SUFFIX,statsigapi.net",
    "DOMAIN,browser-intake-us5-datadoghq.com",
    "DOMAIN-KEYWORD,datadog",
    "DOMAIN-KEYWORD,sift",
    "DOMAIN-SUFFIX,intercom.io",
    "DOMAIN-SUFFIX,intercomcdn.com",
    "DOMAIN,cdn.usefathom.com"
  ];
  var claudeRules = claudeRuleMatchers.map(function (matcher) {
    return matcher + "," + groupName;
  }).concat([
    "IP-CIDR,160.79.104.0/21," + groupName + ",no-resolve",
    "IP-CIDR6,2607:6bc0::/32," + groupName + ",no-resolve",
    "IP-ASN,399358," + groupName + ",no-resolve"
  ]);
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
  // 通用/国内 DNS 保持订阅无关，避免影响飞书和日常网络。
  if (!config.dns || typeof config.dns !== "object") {
    config.dns = {};
  }
  if (!config.dns["nameserver-policy"] ||
      typeof config.dns["nameserver-policy"] !== "object" ||
      Array.isArray(config.dns["nameserver-policy"])) {
    config.dns["nameserver-policy"] = {};
  }
  accountDnsDomains.forEach(function (domain) {
    config.dns["nameserver-policy"][domain] = accountDnsServers.slice();
  });

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

  if (!Array.isArray(config.rules)) {
    config.rules = [];
  }
  config.rules = config.rules.filter(function (rule) {
    return !isManagedClaudeRule(rule);
  });
  config.rules = [inboundRule].concat(claudeRules, config.rules);

  // IP-ASN 规则依赖 ASN.mmdb，默认源在 GitHub releases，墙内拉取失败会导致整份配置加载失败
  if (!config["geox-url"]) {
    config["geox-url"] = {};
  }
  if (!config["geox-url"].asn) {
    config["geox-url"].asn = "https://gh-proxy.com/https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/GeoLite2-ASN.mmdb";
  }

  return config;
}
