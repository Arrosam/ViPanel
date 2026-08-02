// Package mcp 是面板暴露给 agent 的工具目录与 MCP 服务端。
//
// 目录不是手写的，是 mcpgen 从 1Panel 自己的 swagger 里编译出来的，
// 人工评审面只有 manifest.yaml 一处。见 MCP.md §5。
package mcp

// Module 是一个板块。每个板块在 Tier 0 有一个 <key>_tools 工具，
// 调用它才会把这个板块的具体工具注册出来。
type Module struct {
	Key   string
	Title string
	// Desc 会进 server instructions 的关键词索引。
	// 那是**唯一**一段会被上游加载的散文（工具描述是 defer 的），
	// 2KB 硬上限，所以每条要短。见 MCP.md §4.2。
	Desc string
}

// Resolver 是 1Panel 操作日志用的 id→名字解析配方，直接来自 swagger 的 x-panel-log。
// 权限卡片靠它把「删除网站 {id:3}」变成「删除网站 example.com」——
// 人看不懂就会一路点允许，这条决定了权限代理是真护栏还是摆设。
type Resolver struct {
	DB           string
	InputColumn  string
	InputValue   string
	IsList       bool
	OutputColumn string
	OutputValue  string
}

// Op 是一个可被调用的面板操作，一对一映射成一个 MCP 工具。
type Op struct {
	Name   string // 工具名，如 website_delete；对外是 mcp__vipanel__website_delete
	Module string
	Path   string // 面板 API 路径，不含 /api/v2 前缀
	Method string

	// Risk 决定「转述给人时要不要打扰、打扰得多重」，不是第二个决策者。
	//   read        已授权板块内直接放行
	//   write       弹卡片
	//   destructive 弹卡片 + 危险横幅 + 双击确认
	Risk string
	// Resident 为 true 的工具常驻 Tier 0，不需要先调 <板块>_tools。
	// 只给那几个最高频的只读操作，多一个都要论证——它们绕过了板块授权。
	Resident bool
	// NoAlways 禁用「总是允许」。给会花钱或不可逆的操作用。
	NoAlways bool

	DescZH string
	DescEN string

	// FormatZH/EN 是权限卡片的句子模板，形如「删除网站 [domain]」。
	FormatZH string
	FormatEN string

	InputSchema string // 展平后自包含的 JSON Schema，原样透传给 MCP 客户端
	Fields      []string
	PathParams  []string
	QueryParams []string
	Resolvers   []Resolver

	// Fixed 是运行时要合并进请求体的固定参数，JSON 对象。
	//
	// /containers/operate 的 operation 是 oneof=up start stop restart kill pause
	// unpause remove——remove 会删容器。一个静态的 Risk 字段表达不了
	// 「档位取决于入参」，所以拆成一个动作一个工具：container_remove 固定
	// operation=remove 标 destructive，container_restart 固定 restart 标 write。
	// 这些字段不出现在 InputSchema 里——露出来模型就会去填。
	Fixed string
}

// ByName 查一个 op。找不到返回 nil——调用方必须处理，
// 因为运行时还要按实际 path 再查一次 /ai/console 前缀（第二层防御）。
func ByName(name string) *Op {
	for i := range Catalog {
		if Catalog[i].Name == name {
			return &Catalog[i]
		}
	}
	return nil
}

// ModuleOps 返回一个板块下的全部 op。
func ModuleOps(key string) []Op {
	var out []Op
	for _, o := range Catalog {
		if o.Module == key {
			out = append(out, o)
		}
	}
	return out
}
