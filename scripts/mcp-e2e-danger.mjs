// 危险操作那条路：卡片文案要把 id 解析成名字、要带 danger 标记、
// 拒绝之后目标必须还在。这是 MCP 版的 G2。
import { createConnection } from 'node:net';
import { request } from 'node:http';

const SOCK = '/etc/1panel/agent.sock';
const api = (path, body) =>
    new Promise((res, rej) => {
        const req = request(
            { socketPath: SOCK, path: '/api/v2' + path, method: body ? 'POST' : 'GET',
              headers: body ? { 'content-type': 'application/json' } : {} },
            (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => {
                try { res(JSON.parse(d)); } catch { res({ raw: d }); } }); });
        req.on('error', rej);
        if (body) req.write(JSON.stringify(body));
        req.end();
    });
const log = (...a) => console.log(...a);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function wsConnect(path) {
    return new Promise((resolve, reject) => {
        const sock = createConnection(SOCK);
        const key = Buffer.from('vipanel-e2e-16byte').subarray(0, 16).toString('base64');
        sock.write(`GET /api/v2${path} HTTP/1.1\r\nHost: unix\r\nUpgrade: websocket\r\n` +
            `Connection: Upgrade\r\nSec-WebSocket-Key: ${key}\r\nSec-WebSocket-Version: 13\r\n\r\n`);
        let buf = Buffer.alloc(0), up = false; const hs = [];
        sock.on('data', (c) => {
            buf = Buffer.concat([buf, c]);
            if (!up) {
                const i = buf.indexOf('\r\n\r\n'); if (i < 0) return;
                const head = buf.subarray(0, i).toString();
                if (!/101/.test(head)) return reject(new Error(head.split('\r\n')[0]));
                buf = buf.subarray(i + 4); up = true;
                resolve({ send, onMessage: (f) => hs.push(f), close: () => sock.destroy() });
            }
            for (;;) {
                if (buf.length < 2) return;
                const l0 = buf[1] & 0x7f; let off = 2, len = l0;
                if (l0 === 126) { if (buf.length < 4) return; len = buf.readUInt16BE(2); off = 4; }
                else if (l0 === 127) { if (buf.length < 10) return; len = Number(buf.readBigUInt64BE(2)); off = 10; }
                if (buf.length < off + len) return;
                const p = buf.subarray(off, off + len).toString(); buf = buf.subarray(off + len);
                hs.forEach((f) => f(p));
            }
        });
        sock.on('error', reject);
        function send(text) {
            const p = Buffer.from(text), mask = Buffer.from([1, 2, 3, 4]);
            let h;
            if (p.length < 126) h = Buffer.from([0x81, 0x80 | p.length]);
            else { h = Buffer.alloc(4); h[0] = 0x81; h[1] = 0x80 | 126; h.writeUInt16BE(p.length, 2); }
            const m = Buffer.alloc(p.length);
            for (let i = 0; i < p.length; i++) m[i] = p[i] ^ mask[i % 4];
            sock.write(Buffer.concat([h, mask, m]));
        }
    });
}

(async () => {
    const s = await api('/ai/console/sessions/create',
        { cwd: '/srv/vpwork', title: 'mcp-danger', harness: 'claude-code' });
    const id = s?.data?.id;
    log('会话', id);
    await api('/ai/console/sessions/activate', { id });
    await sleep(12000);

    const ws = await wsConnect(`/ai/console/events?id=${id}`);
    const cards = [];
    ws.onMessage((raw) => {
        const m = JSON.parse(raw);
        if (m.type === 'permission_request') {
            const r = m.request;
            log(`\n[卡片] ${r.tool}`);
            log(`  kind=${r.kind} risk=${r.risk} danger=${r.danger} firstUse=${!!r.firstUse} canAlways=${!!r.canAlways}`);
            log(`  标题: ${r.title}`);
            cards.push(r);
            // 中间那些非删除类的自动放行——我们要验的是删除类那一张，
            // 卡在前面的确认上等于什么都没测到
            if (r.risk !== 'destructive') {
                api('/ai/console/permission/resolve', { id: r.id, decision: 'allow', reason: '' })
                    .then(() => log('  → 自动放行'));
            }
        }
    });

    log('\n>>> 让它删掉那个计划任务');
    ws.send(JSON.stringify({ type: 'message',
        text: '调用 cron_tools 拿计划任务板块的工具，用 cron_list 找到名字叫 vp-mcp-target 的任务，然后用 cron_delete 把它删掉。' }));

    for (let i = 0; i < 300; i++) {
        if (cards.some((c) => c.risk === 'destructive')) break;
        await sleep(1000);
    }
    const d = cards.find((c) => c.risk === 'destructive');
    if (!d) { log('❌ 没等到删除类卡片；收到的卡片：', cards.map((c) => c.tool)); process.exit(1); }

    log('\n>>> 拒绝它');
    await api('/ai/console/permission/resolve', { id: d.id, decision: 'deny', reason: '' });
    await sleep(15000);

    log('\n=== 结果 ===');
    log('删除类卡片:', d.tool);
    log('  danger =', d.danger, '（必须为 true → 前端显示红色横幅 + 双击）');
    log('  canAlways =', d.canAlways, '（必须为 false → 删除类永不给「总是允许」）');
    log('  标题 =', d.title);
    ws.close();
    process.exit(0);
})().catch((e) => { log('挂了:', e); process.exit(1); });
