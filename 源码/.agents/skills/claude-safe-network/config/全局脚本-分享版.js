// 分享模板；未部署到本机。适用 Clash Verge Rev / Mihomo。
// 粘贴位置：订阅 → 全局扩展脚本。已有 main 时请合并，不能直接覆盖。
function main(config, profileName) {
  var groupName = "ACCOUNT-US";
  var mixedPort = 43865; // 同步修改 Claude Code 的 HTTP_PROXY / HTTPS_PROXY。
  var socksPort = 43866; // 同步修改 Chrome 快捷方式的 --proxy-server。
  var listenerName = "account-us-socks";
  // 宽泛第三方遥测规则也会影响其他应用，默认不启用。
  var includeThirdPartyRules = false;
  var coreDomains = [
    "anthropic.com", "claude.ai", "claude.com", "claude.app", "clau.de",
    "claudemcpclient.com", "claudemcpcontent.com", "claudeusercontent.com"
  ];
  var exactDomains = [
    "servd-anthropic-website.b-cdn.net", "anthropic.com.cdn.cloudflare.net",
    "anthropic.auth0.com", "anthropic-com.ghost.io"
  ];
  var thirdPartyMatchers = [
    "DOMAIN-SUFFIX,sentry.io", "DOMAIN-SUFFIX,statsigapi.net",
    "DOMAIN,browser-intake-us5-datadoghq.com", "DOMAIN-KEYWORD,datadog",
    "DOMAIN-KEYWORD,sift", "DOMAIN-SUFFIX,intercom.io",
    "DOMAIN-SUFFIX,intercomcdn.com", "DOMAIN,cdn.usefathom.com"
  ];
  var thirdPartyDnsDomains = [
    "sentry.io", "statsigapi.net", "datadoghq.com", "intercom.io", "intercomcdn.com"
  ];
  var matchers = coreDomains.map(function (domain) {
    return "DOMAIN-SUFFIX," + domain;
  }).concat(exactDomains.map(function (domain) {
    return "DOMAIN," + domain;
  }));
  if (includeThirdPartyRules) matchers = matchers.concat(thirdPartyMatchers);
  var inboundRule = "IN-NAME," + listenerName + "," + groupName;
  var addedRules = [inboundRule].concat(matchers.map(function (matcher) {
    return matcher + "," + groupName;
  }));

  // 本模板拥有这三个名称/端口；保留其他节点、策略组和入站。
  config["mixed-port"] = mixedPort;
  if (!Array.isArray(config["proxy-groups"])) config["proxy-groups"] = [];
  config["proxy-groups"] = config["proxy-groups"].filter(function (group) {
    return !group || group.name !== groupName;
  });
  config["proxy-groups"].unshift({
    name: groupName,
    type: "select",
    "include-all-proxies": true,
    "exclude-type": "Direct|Reject"
  });
  if (!Array.isArray(config.listeners)) config.listeners = [];
  config.listeners = config.listeners.filter(function (listener) {
    return !listener || listener.name !== listenerName;
  });
  config.listeners.unshift({
    name: listenerName, type: "socks", listen: "127.0.0.1",
    port: socksPort, udp: false
  });

  // 定向 DoH：仅管理 Claude 域名；其他订阅 DNS 设置保留。
  if (!config.dns || typeof config.dns !== "object" || Array.isArray(config.dns)) {
    config.dns = {};
  }
  if (!config.dns["nameserver-policy"] ||
      typeof config.dns["nameserver-policy"] !== "object" ||
      Array.isArray(config.dns["nameserver-policy"])) {
    config.dns["nameserver-policy"] = {};
  }
  var dnsServers = [
    "https://1.1.1.1/dns-query#" + groupName,
    "https://8.8.8.8/dns-query#" + groupName
  ];
  var dnsDomains = coreDomains.slice();
  if (includeThirdPartyRules) dnsDomains = dnsDomains.concat(thirdPartyDnsDomains);
  dnsDomains.forEach(function (domain) {
    config.dns["nameserver-policy"]["+." + domain] = dnsServers.slice();
  });
  exactDomains.forEach(function (domain) {
    config.dns["nameserver-policy"][domain] = dnsServers.slice();
  });

  // 去掉订阅里的邮件端口无条件 DIRECT；邮件流量继续按后续 CN / MATCH 规则分流。
  function isUnconditionalMailDirectRule(rule) {
    if (typeof rule !== "string") return false;
    return /^DST-PORT,(25|110|143|465|587|993|995),DIRECT(?:,.*)?$/i.test(
      rule.replace(/\s+/g, "")
    );
  }

  // 除上述七个邮件端口无条件 DIRECT 外，只去重本模板生成的条目，保留其他订阅规则及顺序。
  // DOMAIN 匹配目标地址，不匹配 Chrome/CLI/Desktop 进程。
  if (!Array.isArray(config.rules)) config.rules = [];
  config.rules = addedRules.concat(config.rules.filter(function (rule) {
    return addedRules.indexOf(rule) < 0 && !isUnconditionalMailDirectRule(rule);
  }));
  // 不添加 MATCH、不改 TUN 开关、不写订阅节点、不要求 ASN 数据库。
  return config;
}
