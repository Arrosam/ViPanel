package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// gate 记两样按会话隔离的东西：
//
//  1. **板块授权**——哪些板块这个会话已经被人批准使用。只在本会话内有效，
//     不跨会话记忆：会话是意图的边界，「上次我允许它碰数据库」不等于
//     「这次也该允许」。见 MCP.md §4.4。
//
//  2. **决定台账**——每一条已经过人（或规则）决定的调用。MCP 服务端在真正
//     发出请求前必须能在台账里对上一条，对不上就拒绝执行。见 §6.3。
//
// 台账存在的理由是一个**现有**的空洞：把 /usr/local/bin/vipanel-hook 删掉，
// ensureHook() 会把钩子配置一起摘掉，会话照跑但没有任何闸门。
// 对 Bash 来说那是既定的信任模型；但对 MCP 来说，没闸门 = agent 对面板
// 有不受限的 root 权力。有了台账，这种情况下 MCP **整体失效而不是整体放行**。
type gate struct {
	mu      sync.Mutex
	modules map[string]map[string]bool // sessionID → module → 已授权
	ledger  map[string][]*ticket       // sessionID → 未消费的决定
}

type ticket struct {
	tool      string
	inputHash string
	toolUseID string
	expires   time.Time
}

// 台账条目的存活时间。钩子返回 allow 之后 claude 会立刻发 tools/call，
// 中间只隔一次进程间往返，给足余量即可。留太久等于把一次批准变成一段时间窗。
const ticketTTL = 2 * time.Minute

var g = &gate{
	modules: map[string]map[string]bool{},
	ledger:  map[string][]*ticket{},
}

func Gate() *gate { return g }

// -- 板块授权 ---------------------------------------------------------------

func (g *gate) ModuleAuthorized(sessionID, module string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.modules[sessionID][module]
}

func (g *gate) AuthorizeModule(sessionID, module string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.modules[sessionID] == nil {
		g.modules[sessionID] = map[string]bool{}
	}
	g.modules[sessionID][module] = true
}

func (g *gate) AuthorizedModules(sessionID string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for k, ok := range g.modules[sessionID] {
		if ok {
			out = append(out, k)
		}
	}
	return out
}

// Forget 清掉一个会话的全部状态。会话被删或被驱逐时调用——
// 不清的话，同一个 id 被重新激活后会**继承上一次的板块授权**，
// 那就等于跨会话记忆了，正是我们不要的。
func (g *gate) Forget(sessionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.modules, sessionID)
	delete(g.ledger, sessionID)
}

// -- 决定台账 ---------------------------------------------------------------

// InputHash 是对账的主键之一，**两侧必须算出同一个值**：
// 钩子那侧拿到的是 harness 发来的原始 JSON 字节，MCP 服务端那侧拿到的是
// 解析后又摘掉整形参数的 map。直接对字节做 sha256 的话，键序、空格、
// 以及被摘掉的 vp* 参数，三样都会让两边对不上——结果是**每一次**面板操作
// 都被自己的对账拦死，而且症状是「权限代理没生效」这种指向完全错误的报错。
//
// 所以先归一化：解析成 map、丢掉整形参数、再用 encoding/json 重新序列化
// （它对 map 的键是排序输出的）。解析不了就退回原始字节，总比崩了强。
func InputHash(raw []byte) string {
	return hex.EncodeToString(hashOf(canonical(raw)))
}

// InputHashOfArgs 给已经解析成 map 的那一侧用，和 InputHash 等价。
func InputHashOfArgs(args map[string]any) string {
	cleaned := map[string]any{}
	for k, v := range args {
		if shaperKeys[k] {
			continue
		}
		cleaned[k] = v
	}
	b, err := json.Marshal(cleaned)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(hashOf(b))
}

func canonical(raw []byte) []byte {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	for k := range shaperKeys {
		delete(m, k)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return b
}

func hashOf(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:8]
}

// Record 登记一条「已被批准」的调用。只有 allow 才登记，deny 不登记——
// 台账是放行凭据，不是审计日志（审计另有去处，且被拒的也要记）。
func (g *gate) Record(sessionID, tool, inputHash, toolUseID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()

	kept := g.ledger[sessionID][:0]
	for _, t := range g.ledger[sessionID] {
		if t.expires.After(now) {
			kept = append(kept, t)
		}
	}
	g.ledger[sessionID] = append(kept, &ticket{
		tool: tool, inputHash: inputHash, toolUseID: toolUseID,
		expires: now.Add(ticketTTL),
	})
}

// Consume 找一条能对上的凭据并**一次性消费**掉。
//
// 一次性是必须的：连着两次一模一样的 container_list，一次批准不能盖住两次调用。
// 消费不到就返回 false，调用方必须拒绝执行。
//
// 对账键要能跨 harness，所以主键是「工具名 + 入参指纹」——两侧本来就都有。
// toolUseID 只在两边都有值时用来做更精确的匹配；非 Claude 的 harness 没有它，
// 不能因此就对不上。
func (g *gate) Consume(sessionID, tool, inputHash, toolUseID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	list := g.ledger[sessionID]

	best := -1
	for i, t := range list {
		if t.expires.Before(now) || t.tool != tool || t.inputHash != inputHash {
			continue
		}
		if toolUseID != "" && t.toolUseID != "" && t.toolUseID != toolUseID {
			continue
		}
		// 优先用 toolUseID 精确命中的那条
		if best < 0 || (toolUseID != "" && t.toolUseID == toolUseID) {
			best = i
			if toolUseID != "" && t.toolUseID == toolUseID {
				break
			}
		}
	}
	if best < 0 {
		return false
	}
	g.ledger[sessionID] = append(list[:best], list[best+1:]...)
	return true
}
