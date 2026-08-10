package vipanel

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/1Panel-dev/1Panel/agent/global"
)

// 等人做决定的上限。
//
// **必须显著短于 hook 层自己的 600s 超时**，因为那一层超时是 fail-OPEN——
// 到点了工具会被直接放行。我们必须抢在它之前给出一个明确答复，
// 而且超时时给的是 ask（退回终端让人在那边确认），不是 allow。
const decisionTimeout = 120 * time.Second

type Decision string

const (
	DecideAllow Decision = "allow"
	DecideDeny  Decision = "deny"
	DecideAsk   Decision = "ask" // 退回终端，由 TUI 自己问
)

// PermRequest 是一次待决的工具调用。
type PermRequest struct {
	ID        string          `json:"id"`
	SessionID string          `json:"sessionId"`
	Tool      string          `json:"tool"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"toolUseId"`
	Cwd       string          `json:"cwd"`
	Mode      string          `json:"mode"`
	Deadline  int64           `json:"deadline"` // 毫秒时间戳，前端据此倒计时

	// AskUserQuestion 专用：解析好的问题，前端直接渲染成选择控件
	Questions []AskQuestion `json:"questions,omitempty"`

	// -- 以下是 MCP（面板操作）专用，普通工具调用不填 --

	// Kind 为空是原来的工具卡片；"mcp" 是一次面板操作。
	Kind string `json:"kind,omitempty"`
	// FirstUse 为 true 时这张卡片同时是该板块的首次授权：
	// 批准它等于「允许这次操作」+「本会话内这个板块的只读不再询问」。
	// 不拆成两张卡片连着弹，是因为每张最多等 120 秒而 hook 只等 150 秒。
	FirstUse bool `json:"firstUse,omitempty"`
	// Title 是渲染好的一句人话，比如「删除网站 example.com，同时删除数据库和备份」。
	// 卡片上显示它而不是原始 JSON——人看不懂就会一路点允许，
	// 那时权限代理是摆设不是护栏。
	Title string `json:"title,omitempty"`
	Risk  string `json:"risk,omitempty"`
	// Danger 为 true 时前端显示红色危险横幅并要求双击确认。
	Danger bool `json:"danger,omitempty"`
	// CanAlways 决定「总是允许」按钮出不出现。删除类永远为 false。
	CanAlways        bool   `json:"canAlways,omitempty"`
	Module           string `json:"module,omitempty"`
	ModuleTitle      string `json:"moduleTitle,omitempty"`
	OpCount          int    `json:"opCount,omitempty"`
	DestructiveCount int    `json:"destructiveCount,omitempty"`
}

type AskQuestion struct {
	Question    string      `json:"question"`
	Header      string      `json:"header"`
	MultiSelect bool        `json:"multiSelect"`
	Options     []AskOption `json:"options"`
}

type AskOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Verdict 是面板给 hook 的答复。
type Verdict struct {
	Decision Decision `json:"decision"`
	// Reason 会原样进 permissionDecisionReason。
	// 对 AskUserQuestion 来说，这里装的是**用户选的答案**——
	// hook 只能 allow/deny/ask，没有「返回一个值」的通道，
	// 但实测 claude 会把 deny 的 reason 当作反馈接受并继续。
	Reason string `json:"reason"`
}

const askUserQuestionTool = "AskUserQuestion"

type broker struct {
	mu      sync.Mutex
	pending map[string]*pendingReq
}

type pendingReq struct {
	req  PermRequest
	done chan Verdict
	once sync.Once
}

var permBroker = &broker{pending: map[string]*pendingReq{}}

func Broker() *broker { return permBroker }

// Ask 登记一次待决请求并阻塞等待，直到有人决定或超时。
// 由 hook 的回调端点调用。
func (b *broker) Ask(req PermRequest) Verdict {
	if req.Tool == askUserQuestionTool {
		req.Questions = parseAskQuestions(req.Input)
	}
	req.Deadline = time.Now().Add(decisionTimeout).UnixMilli()

	p := &pendingReq{req: req, done: make(chan Verdict, 1)}
	b.mu.Lock()
	b.pending[req.ID] = p
	b.mu.Unlock()

	if s, ok := M().Get(req.SessionID); ok {
		s.broadcastPermission(req)
	}

	var v Verdict
	select {
	case v = <-p.done:
	case <-time.After(decisionTimeout):
		// 超时**绝不能放行**。至于退回什么，取决于这个 harness 的钩子认什么：
		// Claude 可以退回终端让 TUI 自己问；Codex 只认 allow/deny，只能拒绝。
		// 见 PermissionDialect。
		d := timeoutDecisionFor(req.SessionID)
		reason := "面板等待超时，已退回终端确认"
		if d == DecideDeny {
			reason = "面板在时限内没有人确认，已拒绝。请回到面板重试。"
		}
		v = Verdict{Decision: d, Reason: reason}
		global.LOG.Infof("vipanel: 权限请求 %s 超时，降级为 ask", req.ID)
	}

	b.mu.Lock()
	delete(b.pending, req.ID)
	b.mu.Unlock()

	if s, ok := M().Get(req.SessionID); ok {
		s.broadcastPermissionResolved(req.ID, v.Decision)
	}
	return v
}

// Resolve 由浏览器调用。第二次调用是无害的空操作——
// 多设备同时看着时，谁先点算谁，后点的那次不该产生第二个决定。
func (b *broker) Resolve(id string, v Verdict) bool {
	b.mu.Lock()
	p := b.pending[id]
	b.mu.Unlock()
	if p == nil {
		return false
	}
	ok := false
	p.once.Do(func() {
		p.done <- v
		ok = true
	})
	return ok
}

// PendingFor 返回某个会话上所有待决请求，用于新客户端接上来时补发。
func (b *broker) PendingFor(sessionID string) []PermRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []PermRequest
	for _, p := range b.pending {
		if p.req.SessionID == sessionID {
			out = append(out, p.req)
		}
	}
	return out
}

func parseAskQuestions(raw json.RawMessage) []AskQuestion {
	var in struct {
		Questions []AskQuestion `json:"questions"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil
	}
	return in.Questions
}
