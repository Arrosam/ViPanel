// 检查控制台用到的每个 i18n 键在全部语言文件里都存在。
//
// 缺键不会让构建失败——vue-i18n 会静默把键名原样渲染出来，
// 界面上就是一行 aiTools.console.xxx。所以只能靠这个检查兜住。
import fs from 'node:fs';
import path from 'node:path';

const LANG_DIR = 'frontend/src/lang/modules';
// 所有会用到 aiTools.console.* 的 ViPanel 界面。
//
// 一开始只扫控制台那一个目录，结果登录页的品牌文案加进去之后**完全没被检查到**——
// 这个脚本存在的意义就是防「键缺了但没人发现」，它自己漏扫是最讽刺的失败方式。
// 新增 ViPanel 界面时记得把目录加进来。
const VIEW_DIRS = ['frontend/src/views/ai/console', 'frontend/src/views/login'];

// 从源码里收集所有 aiTools.console.* 的用法
const used = new Set();
const walk = (d) => {
    for (const e of fs.readdirSync(d, { withFileTypes: true })) {
        const p = path.join(d, e.name);
        if (e.isDirectory()) { walk(p); continue; }
        const src = fs.readFileSync(p, 'utf8');
        for (const m of src.matchAll(/aiTools\.console\.([\w.]+)/g)) used.add(m[1]);
    }
};
VIEW_DIRS.forEach(walk);

// 模板字符串里的动态键（如 `aiTools.console.status.${s.status}`）
// 会被上面的正则抓成 "status."。展开成后端 SessionStatus 的全部取值——
// 这类键最容易漏，而且漏了以后界面上直接显示 aiTools.console.status.working。
const DYNAMIC = { 'status.': ['idle', 'working', 'unread', 'sleeping', 'error'] };
for (const [prefix, members] of Object.entries(DYNAMIC)) {
    if (!used.delete(prefix)) continue;
    for (const m of members) used.add(prefix + m);
}

// 从语言文件里抽出 console 块内定义的键（含一层嵌套，如 status.idle）
function definedKeys(file) {
    const src = fs.readFileSync(file, 'utf8');
    const start = src.indexOf('        console: {');
    if (start < 0) return null;
    let depth = 0, i = start, end = -1;
    for (; i < src.length; i++) {
        if (src[i] === '{') depth++;
        else if (src[i] === '}') { depth--; if (depth === 0) { end = i; break; } }
    }
    const blk = src.slice(start, end);
    const keys = new Set();
    let group = null, gdepth = 0;
    for (const line of blk.split('\n')) {
        const open = line.match(/^\s{12}(\w+):\s*\{/);
        if (open) { group = open[1]; gdepth = 1; continue; }
        if (group && /^\s{12}\},?$/.test(line)) { group = null; continue; }
        const kv = line.match(/^\s+(\w+):\s*['"]/);
        if (kv) keys.add(group ? `${group}.${kv[1]}` : kv[1]);
    }
    return keys;
}

let bad = 0;
const langs = fs.readdirSync(LANG_DIR).filter((f) => f.endsWith('.ts'));
for (const f of langs) {
    const keys = definedKeys(path.join(LANG_DIR, f));
    if (!keys) { console.log(`✗ ${f}: 没有 console 段`); bad++; continue; }
    const missing = [...used].filter((k) => !keys.has(k));
    if (missing.length) { console.log(`✗ ${f}: 缺 ${missing.length} 个 — ${missing.slice(0, 6).join(', ')}`); bad++; }
    else console.log(`✓ ${f}`);
}
console.log(`\n用到的键: ${used.size}  语言文件: ${langs.length}  有问题: ${bad}`);
process.exit(bad ? 1 : 0);
