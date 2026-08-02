package vipanel

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/1Panel-dev/1Panel/agent/app/service/vipanel/mcp"
	"github.com/1Panel-dev/1Panel/agent/global"
)

// MCP 调用的权限判定。
//
// 这里**不是第二套策略引擎**。harness 自己有权限机制，我们的钩子只是把它的请求
// 转述到浏览器。三档决定的只有一件事：转述时要不要打扰人、打扰得多重。
// 见 MCP.md §6.1。
//
//	read        已授权板块内直接放行，不弹
//	write       弹卡片；可「总是允许」，名单存进 harness 自己的配置
//	destructive 弹卡片 + 红色危险横幅 + 双击确认，且永远不给「总是允许」
func DecideMCP(req PermRequest) (Verdict, bool) {
	cls := mcp.Classify(req.Tool)
	if !cls.IsVipanel {
		return Verdict{}, false
	}
	short := mcp.ShortName(req.Tool)

	// 板块入口和内建工具不碰服务器上的任何东西，直接放行。
	// 它们仍然要登记台账，否则 MCP 服务端那侧的对账会把自己拦下来。
	if cls.Meta {
		mcp.Gate().Record(req.SessionID, short, mcp.InputHash(req.Input), req.ToolUseID)
		return Verdict{Decision: DecideAllow}, true
	}

	op := cls.Op
	if op == nil {
		return Verdict{Decision: DecideDeny,
			Reason: "这个工具不在 ViPanel 的工具目录里。目录可能刚更新过，请重新调用 <板块>_tools 取一次清单。"}, true
	}
	// 第二层防御：按**实际要发出的 path** 再查一次。生成器那层是第一层，
	// 但目录是生成出来的，上游改前缀或有人手改 catalog.gen.go 都能绕过它。
	// /ai/console/permission/resolve 一旦可调，agent 就能批准自己的请求。
	if strings.HasPrefix(op.Path, "/ai/console") {
		audit(req, op, "deny", "会话自身的控制面")
		return Verdict{Decision: DecideDeny,
			Reason: "拒绝：这个操作属于会话自身的控制面，任何 agent 都不能调用它。"}, true
	}

	// 板块授权：第一次真正用到这个板块时连着这次调用一起问。
	//
	// **不能拆成两张卡片连着弹。** 每张卡片最多等 120 秒，两张就是 240 秒，
	// 而 hook 那侧的 HTTP 超时只有 150 秒——首次使用某个板块时很容易直接超时降级。
	// 合成一张也更贴「在请求特定功能的时候弹窗」：人要判断的本来就是
	// 「要不要让它干这件事」，板块是这件事的上下文，不是另一个问题。
	firstUse := !mcp.Gate().ModuleAuthorized(req.SessionID, op.Module)

	allow := func(reason string) (Verdict, bool) {
		if firstUse {
			mcp.Gate().AuthorizeModule(req.SessionID, op.Module)
		}
		mcp.Gate().Record(req.SessionID, short, mcp.InputHash(req.Input), req.ToolUseID)
		audit(req, op, "allow", reason)
		return Verdict{Decision: DecideAllow}, true
	}

	// 已授权板块内的只读操作直接放行。未授权时连只读也要问——
	// 否则「只读默认开放」等于开局就把整台机器的侦察能力给满：
	// 有哪些库、哪些用户、装了什么，一个弹窗都不用。
	if op.Risk == "read" && !firstUse {
		return allow("只读")
	}

	// 「总是允许」的名单存在 harness 自己的权限配置里，由我们的钩子读。
	//
	// 实测过：把工具写进 permissions.allow **不会**让钩子不触发——
	// PreToolUse 跑在权限规则之前，我们一返回 allow/deny，它的规则就没机会执行了。
	// 所以名单只能由我们来认。但存在它的配置里，真相仍然只有一份，
	// 用户在标准位置看得到改得了。见 MCP.md §6.2。
	if !op.NoAlways && op.Risk == "write" {
		if s, ok := M().Get(req.SessionID); ok {
			if ps, ok := s.Harness.(PermissionStore); ok && ps.AlwaysAllowed(req.Tool) {
				return allow("用户此前选择了总是允许")
			}
		}
	}

	card := req
	card.Kind = "mcp"
	card.FirstUse = firstUse
	if firstUse {
		card.OpCount, card.DestructiveCount = mcp.ModuleStats(op.Module)
	}
	card.Risk = op.Risk
	card.Danger = op.Risk == "destructive"
	card.CanAlways = op.Risk == "write" && !op.NoAlways
	card.Module = op.Module
	card.ModuleTitle = mcp.ModuleTitle(op.Module)
	card.Title = renderCard(op, req.Input)

	v := Broker().Ask(card)
	switch v.Decision {
	case DecideAllow:
		return allow("用户允许")
	case Decision("always"):
		if s, ok := M().Get(req.SessionID); ok {
			if ps, ok := s.Harness.(PermissionStore); ok {
				if err := ps.AddAlwaysAllow(req.Tool); err != nil {
					global.LOG.Warnf("vipanel: 写入总是允许名单失败: %v", err)
				}
			}
		}
		return allow("用户选择总是允许")
	default:
		audit(req, op, string(v.Decision), v.Reason)
		reason := v.Reason
		if reason == "" {
			reason = "用户拒绝了这次面板操作。"
		}
		// 光回一个 denied，模型会转头用 Bash 直接干同一件事——
		// 既绕过了限制，也绕过了审计。必须明说不要改道。
		if firstUse {
			reason = fmt.Sprintf("本会话未获授权使用「%s」板块。%s",
				mcp.ModuleTitle(op.Module), reason)
		}
		return Verdict{Decision: DecideDeny,
			Reason: reason + " 请不要改用命令行等其他方式绕过，先向用户说明你想做什么。"}, true
	}
}

// -- 卡片文案 ---------------------------------------------------------------

var placeholder = regexp.MustCompile(`\[([A-Za-z_][A-Za-z0-9_]*)\]`)

// renderCard 把 x-panel-log 的模板渲染成一句人话。
//
// 「删除网站 example.com，同时删除数据库和备份」和 POST /websites/del {"id":3}
// 的差别，决定了权限代理是真护栏还是摆设——人看不懂就会一路点允许。
func renderCard(op *mcp.Op, input json.RawMessage) string {
	tpl := op.FormatZH
	if tpl == "" {
		tpl = op.DescZH
	}
	if tpl == "" {
		return op.Name
	}

	var body map[string]any
	_ = json.Unmarshal(input, &body)
	if body == nil {
		body = map[string]any{}
	}
	if op.Fixed != "" {
		var fixed map[string]any
		if json.Unmarshal([]byte(op.Fixed), &fixed) == nil {
			for k, v := range fixed {
				body[k] = v
			}
		}
	}
	resolveNames(op, body)

	return placeholder.ReplaceAllStringFunc(tpl, func(m string) string {
		key := m[1 : len(m)-1]
		if v, ok := body[key]; ok {
			return fmt.Sprint(v)
		}
		return m
	})
}

// resolveNames 跑 x-panel-log 的 BeforeFunctions，把 id 换成名字。
// 查不到就**保留原样**，绝不能因为一次 DB 查询失败就卡住弹窗。
func resolveNames(op *mcp.Op, body map[string]any) {
	if len(op.Resolvers) == 0 || global.DB == nil {
		return
	}
	for _, r := range op.Resolvers {
		if !safeIdent(r.DB) || !safeIdent(r.InputColumn) || !safeIdent(r.OutputColumn) {
			continue
		}
		v, ok := body[r.InputValue]
		if !ok {
			continue
		}
		var names []string
		sql := fmt.Sprintf("SELECT %s FROM %s WHERE %s IN (?);", r.OutputColumn, r.DB, r.InputColumn)
		if err := global.DB.Raw(sql, v).Scan(&names).Error; err != nil || len(names) == 0 {
			continue
		}
		body[r.OutputValue] = strings.Join(names, ", ")
	}
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func safeIdent(s string) bool { return identRe.MatchString(s) }

// -- 审计 -------------------------------------------------------------------

// audit 把每一次 MCP 调用写进面板操作日志。
//
// MCP 走的是 agent socket，绕开了 core 的操作日志中间件，不自己补写的话
// 面板日志里会出现「没有任何人操作过，但网站没了」——那比没有日志更糟，
// 它会把排查引向「是不是被入侵了」。
//
// **被拒绝的也写。**「agent 试图删库但被拒」比一次成功操作更值得留痕。
func audit(req PermRequest, op *mcp.Op, decision, note string) {
	if global.CoreDB == nil {
		return
	}
	title := req.SessionID
	if s, ok := M().Get(req.SessionID); ok && s.Title != "" {
		title = s.Title
	}
	status, zh := "Success", renderCard(op, req.Input)
	if decision != "allow" {
		status = "Failed"
		zh = "[已拒绝] " + zh
	}
	if note != "" {
		zh += "（" + note + "）"
	}

	err := global.CoreDB.Exec(
		`INSERT INTO operation_logs (created_at, updated_at, source, user, ip, path, method, status, message, detail_zh, detail_en)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		time.Now(), time.Now(), "ViPanel MCP", "ViPanel Agent · "+title, "local",
		op.Path, op.Method, status, note, zh, op.DescEN,
	).Error
	if err != nil {
		global.LOG.Warnf("vipanel: 写操作日志失败: %v", err)
	}
}
