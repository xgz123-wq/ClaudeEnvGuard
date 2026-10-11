'use strict';
// 仅访问公开测试端点；不加载 Claude、账号资料或浏览器配置。
const net = require('node:net');
const http = require('node:http');
const tls = require('node:tls');
const dgram = require('node:dgram');
const { Resolver } = require('node:dns/promises');
const { randomBytes } = require('node:crypto');
const results = {};
async function run(name, fn) {
  const start = Date.now();
  try { results[name] = { ok: true, value: await fn(), ms: Date.now() - start }; }
  catch (e) { results[name] = { ok: false, code: e.code || e.message, ms: Date.now() - start }; }
}
function tcp(host, port) {
  return new Promise((resolve, reject) => {
    const socket = net.createConnection({ host, port });
    socket.setTimeout(5000, () => { socket.destroy(); reject(new Error('TIMEOUT')); });
    socket.once('connect', () => { socket.destroy(); resolve('connected'); });
    socket.once('error', reject);
  });
}
function udp(host, family) {
  return new Promise((resolve, reject) => {
    const socket = dgram.createSocket(family);
    const timer = setTimeout(() => { socket.close(); reject(new Error('TIMEOUT')); }, 5000);
    socket.send(Buffer.from('claude-network-guard-probe'), 3478, host, error => {
      clearTimeout(timer); socket.close();
      if (error) reject(error); else resolve('send accepted by OS; not delivery proof');
    });
  });
}
async function dns(server) {
  const resolver = new Resolver({ timeout: 1500, tries: 1 });
  if (server) resolver.setServers([server]);
  try { return await resolver.resolve4('example.com'); } finally { resolver.cancel(); }
}
function stun() {
  return new Promise((resolve, reject) => {
    // 地址经专用代理中的 DoH 实时核对；这是探针端点，不是产品固定配置。
    const host = '162.159.207.0';
    const socket = dgram.createSocket('udp4');
    const packet = Buffer.alloc(20); packet.writeUInt16BE(1, 0); packet.writeUInt32BE(0x2112a442, 4);
    randomBytes(12).copy(packet, 8);
    let done = false;
    const finish = (error, value) => {
      if (done) return; done = true; clearTimeout(timer); clearInterval(retry); socket.close();
      if (error) reject(error); else resolve(value);
    };
    const timer = setTimeout(() => finish(new Error('TIMEOUT')), 5000);
    const send = () => socket.send(packet, 3478, host, error => { if (error) finish(error); });
    const retry = setInterval(send, 1000);
    socket.on('error', error => finish(error));
    socket.on('message', (message, remote) => {
      if (remote.address === host && remote.port === 3478 && message.length >= 20 &&
          message.readUInt16BE(0) === 0x0101 && message.subarray(4, 20).equals(packet.subarray(4, 20))) {
        finish(null, 'STUN binding response received');
      }
    });
    send();
  });
}
function proxy(port) {
  return new Promise((resolve, reject) => {
    const request = http.request({ host: '127.0.0.1', port, method: 'CONNECT', path: 'api4.ipify.org:443' });
    let secure;
    const timer = setTimeout(() => {
      request.destroy(); if (secure) secure.destroy(); reject(new Error('TIMEOUT'));
    }, 15000);
    const finish = (err, value) => {
      clearTimeout(timer); request.destroy(); if (secure) secure.destroy();
      if (err) reject(err); else resolve(value);
    };
    request.once('error', e => finish(e));
    request.once('connect', (response, socket, head) => {
      if (response.statusCode !== 200 || head.length) { socket.destroy(); return finish(new Error('CONNECT_' + response.statusCode)); }
      secure = tls.connect({ socket, servername: 'api4.ipify.org', rejectUnauthorized: true });
      let body = '';
      secure.setEncoding('utf8');
      secure.once('secureConnect', () => secure.write('GET / HTTP/1.1\r\nHost: api4.ipify.org\r\nConnection: close\r\n\r\n'));
      secure.on('data', chunk => { body += chunk; if (body.length > 16384) finish(new Error('OVERSIZE')); });
      secure.once('error', e => finish(e));
      secure.once('end', () => {
        const payload = body.split('\r\n\r\n').slice(1).join('\r\n\r\n').trim();
        const ip = payload.match(/\b(?:\d{1,3}\.){3}\d{1,3}\b/);
        if (!/^HTTP\/1\.[01] 200\b/.test(body) || !ip) return finish(new Error('INVALID_RESPONSE'));
        finish(null, { ip: ip[0], proxy: '127.0.0.1:' + port });
      });
    });
    request.end();
  });
}
(async () => {
  await Promise.all([
    run('directTcp4', () => tcp('1.1.1.1', 443)),
    run('directTcp6', () => tcp('2606:4700:4700::1111', 443)),
    run('directTcpDns', () => tcp('1.1.1.1', 53)),
    run('directUdp4', () => udp('1.1.1.1', 'udp4')),
    run('directUdp6', () => udp('2606:4700:4700::1111', 'udp6')),
    run('stunUdp4', stun),
    run('nativeDnsDefault', () => dns()),
    run('nativeDnsPublic', () => dns('1.1.1.1')),
    run('localProxy', () => proxy(43867)),
    run('absentProxy', () => proxy(43997))
  ]);
  process.stdout.write(JSON.stringify({ at: new Date().toISOString(), executable: process.execPath, results }));
})().catch(e => { process.stderr.write(String(e)); process.exitCode = 1; });
