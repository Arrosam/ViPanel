// ViPanel MCP 端到端。在 VM 里跑，直接打面板的 unix socket，不经过浏览器。
//
// 用 Node 22 内置的 WebSocket（无依赖）。事件流那条 ws 同时承载
// 「agent 说了什么」和「权限卡片」，所以一个连接就够驱动全流程。
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

// Node 的 WebSocket 不支持 unix socket，自己握手：HTTP Upgrade + 手写帧。
// 只需要文本帧、且长度都不大，所以实现得很省。
function wsConnect(path) {
    return new Promise((resolve, reject) => {
        const sock = createConnection(SOCK);
        const key = Buffer.from('vipanel-e2e-16byte').subarray(0, 16).toString('base64');
        sock.write(
            `GET /api/v2${path} HTTP/1.1\r\nHost: unix\r\nUpgrade: websocket\r\n` +
            `Connection: Upgrade\r\nSec-WebSocket-Key: ${key}\r\nSec-WebSocket-Version: 13\r\n\r\n`);
        let buf = Buffer.alloc(0), upgraded = false;
        const handlers = [];
        sock.on('data', (chunk) => {
            buf = Buffer.concat([buf, chunk]);
            if (!upgraded) {
                const i = buf.indexOf('\r\n\r\n');
                if (i < 0) return;
                const head = buf.subarray(0, i).toString();
                if (!/101/.test(head)) { reject(new Error('升级失败: ' + head.split('\r\n')[0])); return; }
                buf = buf.subarray(i + 4); upgraded = true;
                resolve({ send, onMessage: (f) => handlers.push(f), close: () => sock.destroy() });
            }
            for (;;) {
                if (buf.length < 2) return;
                const len0 = buf[1] & 0x7f;
                let off = 2, len = len0;
                if (len0 === 126) { if (buf.length < 4) return; len = buf.readUInt16BE(2); off = 4; }
                else if (len0 === 127) { if (buf.length < 10) return; len = Number(buf.readBigUInt64BE(2)); off = 10; }
                if (buf.length < off + len) return;
                const payload = buf.subarray(off, off + len).toString();
                buf = buf.subarray(off + len);
                handlers.forEach((f) => { try { f(payload); } catch (e) { log('handler 出错', e); } });
            }
        });
        sock.on('error', reject);
        function send(text) {
            const p = Buffer.from(text);
            const mask = Buffer.from([1, 2, 3, 4]);
            let header;
            if (p.length < 126) header = Buffer.from([0x81, 0x80 | p.length]);
            else { header = Buffer.alloc(4); header[0] = 0x81; header[1] = 0x80 | 126; header.writeUInt16BE(p.length, 2); }
            const masked = Buffer.alloc(p.length);
            for (let i = 0; i < p.length; i++) masked[i] = p[i] ^ mask[i % 4];
            sock.write(Buffer.concat([header, mask, masked]));
        }
    });
}

const [, , cwd] = process.argv;

(async () => {
    log('== 1. 建会话 ==');
    const created = await api('/ai/console/sessions/create',
        { cwd: cwd || '/srv/vpwork', title: 'mcp-e2e', harness: 'claude-code' });
    const id = created?.data?.id;
    if (!id) { log('建会话失败', JSON.stringify(created)); process.exit(1); }
    log('会话 id =', id);

    await api('/ai/console/sessions/activate', { id });
    log('已激活，等 agent 起来…');
    await sleep(12000);

    log('== 2. 连事件流 ==');
    const ws = await wsConnect(`/ai/console/events?id=${id}`);
    const seen = { cards: [], text: [] };
    ws.onMessage((raw) => {
        const m = JSON.parse(raw);
        if (m.type === 'permission_request') {
            const r = m.request;
            log(`\n[卡片] tool=${r.tool} kind=${r.kind || '-'} risk=${r.risk || '-'} ` +
                `danger=${!!r.danger} firstUse=${!!r.firstUse} canAlways=${!!r.canAlways}`);
            log(`       标题: ${r.title || '(无)'}`);
            if (r.firstUse) log(`       板块: ${r.moduleTitle} ${r.opCount} 个操作 / ${r.destructiveCount} 删除类`);
            seen.cards.push(r);
        } else if (m.type === 'events') {
            for (const e of m.events || []) {
                if (e.kind === 'assistant' || e.kind === 'tool' || e.kind === 'tool_result') {
                    const t = (e.text || '').slice(0, 220).replace(/\n/g, ' ');
                    if (t) { log(`[${e.kind}] ${t}`); seen.text.push(t); }
                }
            }
        }
    });

    const ask = (text) => { log(`\n>>> ${text}`); ws.send(JSON.stringify({ type: 'message', text })); };
    const waitFor = async (pred, secs) => {
        for (let i = 0; i < secs * 2; i++) { if (pred()) return true; await sleep(500); }
        return false;
    };

    log('\n== 3. Meta 工具应当免确认 ==');
    ask('调用 vipanel_overview 这个工具，把它返回的原文贴出来。不要做别的。');
    await waitFor(() => seen.text.some((t) => /面板|1Panel|板块/.test(t)), 90);
    log(`Meta 阶段弹出的卡片数：${seen.cards.length}（期望 0）`);

    log('\n== 4. 常驻只读应当免确认 ==');
    const beforeResident = seen.cards.length;
    ask('用 website_list 列出这台机器上的网站，把结果贴出来。');
    await waitFor(() => seen.text.some((t) => /website_list|网站/.test(t)), 90);
    log(`常驻只读阶段新增卡片：${seen.cards.length - beforeResident}（期望 0）`);

    log('\n== 5. 非常驻工具首次使用板块应当弹卡片 ==');
    const before = seen.cards.length;
    ask('调用 website_tools 取网站板块的工具清单，然后用 website_search 分页查一次网站。');
    const got = await waitFor(() => seen.cards.length > before, 120);
    if (!got) { log('❌ 没等到权限卡片'); process.exit(1); }
    const card = seen.cards[seen.cards.length - 1];
    log('批准它…');
    await api('/ai/console/permission/resolve', { id: card.id, decision: 'allow', reason: '' });
    await sleep(20000);

    log('\n== 6. 审计日志 ==');
    log('（在宿主机上查 core.db）');

    log('\n=== 汇总 ===');
    log('卡片总数', seen.cards.length);
    for (const c of seen.cards) log(` - ${c.tool} kind=${c.kind} firstUse=${!!c.firstUse} title=${c.title}`);
    ws.close();
    process.exit(0);
})().catch((e) => { log('挂了:', e); process.exit(1); });
