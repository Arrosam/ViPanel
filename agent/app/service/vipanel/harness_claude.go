package vipanel

import (
	"os"
	"path/filepath"
	"time"
)

func init() {
	register(&claudeCode{})
	register(&shellHarness{})
}

// claudeCode 是 Claude Code 的适配层。
// **所有** Claude 特有的知识都必须收在这个文件里，外面看不到 claude 三个字。
type claudeCode struct{}

func (claudeCode) ID() string          { return "claude-code" }
func (claudeCode) DisplayName() string { return "Claude Code" }

func (claudeCode) Capabilities() Capabilities {
	return Capabilities{
		StructuredEvents: true,
		Resume:           true,
		Interrupt:        true,
		Auth:             true,
		Models:           []string{"fable", "opus", "sonnet", "haiku"},
		EffortLevels:     []string{"low", "medium", "high", "xhigh", "max"},
	}
}

// Spawn：首次用 --session-id 指定 id，之后用 --resume 接回。
//
// --resume 不带 --fork-session 时**复用**原 session id，transcript 还是同一个
// 文件，历史不丢。带上 --fork-session 会分叉出新 id，那不是我们要的。
func (claudeCode) Spawn(ctx SpawnContext) PtySpec {
	args := []string{"--session-id", ctx.SessionID}
	if ctx.Resume {
		args = []string{"--resume", ctx.SessionID}
	}
	return PtySpec{
		File: "claude",
		Args: args,
		Cwd:  ctx.Cwd,
		Env:  ctx.Env,
		Cols: ctx.Cols,
		Rows: ctx.Rows,
	}
}

// Submit 必须把正文和回车拆成两次写。
//
// 合成一次 write(text + "\r") 的话，TUI 会把它当成一次「粘贴了带换行的文本」，
// 结果是换行进了输入框，消息根本没发出去。这条是实测出来的，不是猜的。
func (claudeCode) Submit(write func([]byte), text string) {
	write([]byte(text))
	time.Sleep(200 * time.Millisecond)
	write([]byte("\r"))
}

func (claudeCode) Interrupt(write func([]byte)) { write([]byte{0x1b}) }

// HasHistory 看 transcript 文件在不在。
//
// 定位方式是**按 uuid 遍历**，不是拼目录名。claude 用
// path.replace(/[^A-Za-z0-9-]/g, '-') 把 cwd 转成目录名，这个映射**会碰撞**
// （比如 /a/b 和 /a_b 会落到同一个目录），照着规则拼出来的路径不可靠。
// session id 是 uuid，全局唯一，直接找它才是对的。
func (claudeCode) HasHistory(sessionID, _ string) bool {
	return transcriptPath(sessionID) != ""
}

func projectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// transcriptPath 返回该 session 的 transcript 路径，找不到返回空串。
func transcriptPath(sessionID string) string {
	root := projectsDir()
	if root == "" {
		return ""
	}
	dirs, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		p := filepath.Join(root, d.Name(), sessionID+".jsonl")
		if st, err := os.Stat(p); err == nil && st.Size() > 0 {
			return p
		}
	}
	return ""
}

// ---------------------------------------------------------------------------

// shellHarness 是一个能力全 false 的实现，用来证伪上面那套抽象。
//
// 它不是玩具：只要面板有任何一处「默认 agent 一定支持某件事」的假设，
// 换到这个 harness 上立刻就会暴露。demo 阶段它第一天就抓到过一个空指针。
type shellHarness struct{}

func (shellHarness) ID() string                 { return "shell" }
func (shellHarness) DisplayName() string        { return "Shell" }
func (shellHarness) Capabilities() Capabilities { return Capabilities{} }

func (shellHarness) Spawn(ctx SpawnContext) PtySpec {
	return PtySpec{File: LoginShell(), Cwd: ctx.Cwd, Env: ctx.Env, Cols: ctx.Cols, Rows: ctx.Rows}
}

func (shellHarness) Submit(write func([]byte), text string) { write([]byte(text + "\n")) }
func (shellHarness) Interrupt(write func([]byte))           { write([]byte{0x03}) }
func (shellHarness) HasHistory(string, string) bool         { return false }
