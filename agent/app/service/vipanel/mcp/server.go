package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/1Panel-dev/1Panel/agent/global"
)

// MCP 服务端。协议面只有五个方法，手写 JSON-RPC 比引一个 SDK 划算——
// agent/go.mod 是 rebase 冲突面，每加一个依赖就多一处上游合并时要手动裁决的地方。
// 见 MCP.md §3.5。
const protocolVersion = "2025-06-18"

// ToolPrefix 是 harness 给 MCP 工具加的前缀，钩子那侧看到的是完整名字。
const ToolPrefix = "mcp__vipanel__"

// Transport 是一条能双向收发 JSON-RPC 消息的通道。
// 之所以不能只是「POST 一次拿一次回应」：notifications/tools/list_changed
// 是**服务端主动推**的，请求/响应模型接不住。见 MCP.md §3.3。
type Transport interface {
	Recv() ([]byte, error)
	Send([]byte) error
}

type Server struct {
	tp        Transport
	sessionID string

	mu     sync.Mutex
	opened map[string]bool // 已经取过工具清单的板块
}

func NewServer(tp Transport, sessionID string) *Server {
	return &Server{tp: tp, sessionID: sessionID, opened: map[string]bool{}}
}

type rpcReq struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func (s *Server) Run() {
	for {
		raw, err := s.tp.Recv()
		if err != nil {
			return
		}
		var req rpcReq
		if json.Unmarshal(raw, &req) != nil {
			continue
		}
		s.handle(req)
	}
}

func (s *Server) handle(req rpcReq) {
	switch req.Method {
	case "initialize":
		s.reply(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
			"serverInfo":      map[string]any{"name": "vipanel", "version": "1"},
			"instructions":    Instructions(),
		})
	case "notifications/initialized":
		// 无需回应
	case "ping":
		s.reply(req.ID, map[string]any{})
	case "tools/list":
		s.reply(req.ID, map[string]any{"tools": s.tools()})
	case "tools/call":
		s.call(req)
	default:
		if len(req.ID) > 0 {
			s.fail(req.ID, -32601, "不支持的方法 "+req.Method)
		}
	}
}

func (s *Server) reply(id json.RawMessage, result any) {
	if len(id) == 0 {
		return
	}
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	_ = s.tp.Send(raw)
}

func (s *Server) fail(id json.RawMessage, code int, msg string) {
	raw, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": msg},
	})
	_ = s.tp.Send(raw)
}

func (s *Server) notify(method string) {
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	_ = s.tp.Send(raw)
}

// -- 工具清单 ---------------------------------------------------------------

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

const emptySchema = `{"type":"object","properties":{}}`

// Instructions 是**唯一**一段会被上游加载的散文——工具描述是 defer 的，
// 所以板块的关键词索引只能放这里。Claude Code 把它截断在 2KB。
func Instructions() string {
	var b strings.Builder
	b.WriteString("ViPanel 控制这台服务器的 1Panel 面板。每个板块有一个 <板块>_tools 工具，" +
		"调用它会返回该板块的具体工具并使其可用。\n")
	for _, m := range Modules {
		b.WriteString(m.Key + " " + m.Desc + " · ")
	}
	b.WriteString("\n文件读写、执行命令请用你自带的工具，本服务只提供面板独有的能力。")
	return b.String()
}

func (s *Server) tools() []tool {
	s.mu.Lock()
	opened := make(map[string]bool, len(s.opened))
	for k, v := range s.opened {
		opened[k] = v
	}
	s.mu.Unlock()

	out := []tool{
		{Name: "vipanel_overview",
			Description: "这台服务器的面板概况：版本、已装应用/网站/容器数量、Docker 状态。先调它建立整体认识。",
			InputSchema: json.RawMessage(emptySchema)},
		{Name: "vipanel_task",
			Description: "查一个长任务的状态和日志尾巴。安装应用、申请证书这类操作会立刻返回 taskID，用它轮询。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"taskID":{"type":"string"}},"required":["taskID"]}`)},
	}
	for _, m := range Modules {
		n := len(ModuleOps(m.Key))
		if n == 0 {
			continue // 还没铺清单的板块不露出来，免得模型调了拿到空手
		}
		out = append(out, tool{
			Name:        m.Key + "_tools",
			Description: fmt.Sprintf("列出「%s」板块的 %d 个工具（%s）并使其可用。不改变任何东西，可以放心调。", m.Title, n, m.Desc),
			InputSchema: json.RawMessage(emptySchema),
		})
	}
	for i := range Catalog {
		op := &Catalog[i]
		if op.Resident || opened[op.Module] {
			out = append(out, toolOf(op))
		}
	}
	return out
}

func toolOf(op *Op) tool {
	schema := op.InputSchema
	// 列表类工具额外接受整形参数。不给的话模型没法自己缩小范围，
	// 只能撞上 25k 截断——而截断是**静默**的，它会照着残缺数据做决定。
	if len(op.Fields) > 0 {
		schema = injectShaperParams(schema)
	}
	desc := op.DescZH
	if desc == "" {
		desc = op.DescEN
	}
	if op.Risk == "destructive" {
		desc = "【危险】" + desc
	}
	return tool{Name: op.Name, Description: desc, InputSchema: json.RawMessage(schema)}
}

func injectShaperParams(schema string) string {
	var s map[string]any
	if json.Unmarshal([]byte(schema), &s) != nil {
		return schema
	}
	props, _ := s["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		s["properties"] = props
	}
	props["vpPage"] = map[string]any{"type": "integer", "description": "第几页，从 1 开始"}
	props["vpPageSize"] = map[string]any{"type": "integer", "description": "每页条数，默认 20，最大 200"}
	props["vpGrep"] = map[string]any{"type": "string", "description": "正则，对整条记录匹配，可用来按未显示的字段过滤"}
	props["vpFields"] = map[string]any{"type": "string", "description": `要显示的字段，逗号分隔；传 "*" 拿全部字段`}
	raw, err := json.Marshal(s)
	if err != nil {
		return schema
	}
	return string(raw)
}

// -- 调用 -------------------------------------------------------------------

type callParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	Meta      struct {
		ToolUseID string `json:"claudecode/toolUseId"`
	} `json:"_meta"`
}

func (s *Server) call(req rpcReq) {
	var p callParams
	if json.Unmarshal(req.Params, &p) != nil {
		s.fail(req.ID, -32602, "参数不是合法 JSON")
		return
	}
	if p.Arguments == nil {
		p.Arguments = map[string]any{}
	}

	// 板块入口：只返回清单，不碰服务器上的任何东西，所以不需要授权。
	// 授权发生在这个板块**第一次真正调用**时。见 MCP.md §4.4。
	if mod, ok := strings.CutSuffix(p.Name, "_tools"); ok && moduleExists(mod) {
		s.openModule(req.ID, mod)
		return
	}

	switch p.Name {
	case "vipanel_overview":
		s.text(req.ID, overview())
		return
	case "vipanel_task":
		s.text(req.ID, taskStatus(fmt.Sprint(p.Arguments["taskID"])))
		return
	}

	op := ByName(p.Name)
	if op == nil {
		s.fail(req.ID, -32602, "没有这个工具："+p.Name)
		return
	}

	// 对账：钩子那侧没看见过的调用，一律不执行。
	//
	// 这堵的是一个现有的空洞——把 vipanel-hook 删掉，会话照跑但没有任何闸门。
	// 对 Bash 那是既定的信任模型；对 MCP 来说没闸门 = 对面板不受限的 root 权力。
	// 有了对账，那种情况下 MCP **整体失效而不是整体放行**。见 MCP.md §6.3。
	shaper := TakeShaper(p.Arguments)
	raw, _ := json.Marshal(p.Arguments)
	if !Gate().Consume(s.sessionID, p.Name, InputHash(raw), p.Meta.ToolUseID) {
		s.text(req.ID, "这次调用没有对应的权限决定记录，已拒绝执行。\n"+
			"通常意味着 ViPanel 的权限代理没有生效（钩子未安装或被移除）。"+
			"请让用户在面板上检查「权限代理」状态，不要改用其他方式绕过。")
		return
	}

	data, err := Call(op, p.Arguments)
	if err != nil {
		s.text(req.ID, "调用失败："+err.Error())
		return
	}

	shaped, note := Shape(data, shaper, op.Fields)
	out, _ := json.MarshalIndent(shaped, "", " ")
	body := string(out)
	if note != "" {
		body += "\n\n" + note
	}
	s.text(req.ID, body)
}

func (s *Server) openModule(id json.RawMessage, mod string) {
	s.mu.Lock()
	first := !s.opened[mod]
	s.opened[mod] = true
	s.mu.Unlock()

	ops := ModuleOps(mod)
	var b strings.Builder
	title := mod
	for _, m := range Modules {
		if m.Key == mod {
			title = m.Title
		}
	}
	fmt.Fprintf(&b, "「%s」板块的 %d 个工具已可用：\n\n", title, len(ops))
	for _, op := range ops {
		mark := ""
		switch op.Risk {
		case "destructive":
			mark = " 【危险】"
		case "write":
			mark = " [变更]"
		}
		desc := op.DescZH
		if desc == "" {
			desc = op.DescEN
		}
		fmt.Fprintf(&b, "- %s%s：%s\n", op.Name, mark, desc)
	}
	b.WriteString("\n参数说明按需取用：这些工具的 schema 已随本次调用一并注册，直接调用即可。\n" +
		"首次真正调用本板块的工具时，面板会向用户请求一次授权。")

	// 清单里只给名字和一句话，**不给完整 schema**。
	// website 有 65 个工具，一次吐 65 份 JSON Schema 就是几千 token，
	// 渐进式披露当场作废。完整 schema 走下面这条 list_changed 之后的 tools/list，
	// 那条路上 harness 会继续按需 defer。见 MCP.md §4.2。
	s.text(id, b.String())
	if first {
		s.notify("notifications/tools/list_changed")
	}
}

func (s *Server) text(id json.RawMessage, body string) {
	s.reply(id, map[string]any{
		"content": []any{map[string]any{"type": "text", "text": body}},
	})
}

func moduleExists(key string) bool {
	for _, m := range Modules {
		if m.Key == key {
			return len(ModuleOps(key)) > 0
		}
	}
	return false
}

// -- 内建工具 ---------------------------------------------------------------

func overview() string {
	var b strings.Builder
	b.WriteString("面板：1Panel " + global.CONF.Base.Version + "\n")

	type probe struct {
		label string
		op    Op
	}
	for _, p := range []probe{
		{"已安装应用", Op{Path: "/apps/installed/list", Method: "GET"}},
		{"网站", Op{Path: "/websites/list", Method: "GET"}},
		{"容器", Op{Path: "/containers/status", Method: "GET"}},
		{"Docker", Op{Path: "/containers/docker/status", Method: "GET"}},
	} {
		data, err := Call(&p.op, map[string]any{})
		if err != nil {
			fmt.Fprintf(&b, "%s：读取失败（%v）\n", p.label, err)
			continue
		}
		if items, _, ok := listOf(data); ok {
			fmt.Fprintf(&b, "%s：%d 个\n", p.label, len(items))
			continue
		}
		raw, _ := json.Marshal(data)
		fmt.Fprintf(&b, "%s：%s\n", p.label, truncate(string(raw), 200))
	}

	b.WriteString("\n可用板块（调用 <板块>_tools 取具体工具）：\n")
	for _, m := range Modules {
		if n := len(ModuleOps(m.Key)); n > 0 {
			fmt.Fprintf(&b, "- %s_tools　%s（%d 个工具）\n", m.Key, m.Desc, n)
		}
	}
	return b.String()
}

func taskStatus(taskID string) string {
	if strings.TrimSpace(taskID) == "" || taskID == "<nil>" {
		return "需要 taskID"
	}
	data, err := Call(&Op{Path: "/logs/tasks/search", Method: "POST"}, map[string]any{
		"taskID": taskID, "page": 1, "pageSize": 5,
	})
	if err != nil {
		return "查任务失败：" + err.Error()
	}
	raw, _ := json.MarshalIndent(truncateDeep(data, nil), "", " ")
	return string(raw)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
