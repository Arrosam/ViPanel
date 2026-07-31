package vipanel

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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

// Discover 扫 ~/.claude/projects，列出还没被面板收录的对话。
//
// 只读文件头尾各一小段来取 cwd 和标题：单个 transcript 可以到几十上百 MB，
// 为了列个目录把它们整个读一遍是不可接受的。
func (claudeCode) Discover(known map[string]bool, limit int) []Discovered {
	root := projectsDir()
	if root == "" {
		return nil
	}
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []Discovered
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, d.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			id := strings.TrimSuffix(name, ".jsonl")
			if known[id] {
				continue
			}
			full := filepath.Join(root, d.Name(), name)
			st, err := os.Stat(full)
			if err != nil || st.Size() == 0 {
				continue
			}
			meta := peek(full)
			if meta == nil {
				continue // 空会话或损坏，列出来只是噪音
			}
			meta.ID = id
			meta.Mtime = st.ModTime().UnixMilli()
			meta.Size = st.Size()
			out = append(out, *meta)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mtime > out[j].Mtime })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

const peekWindow = 64 << 10

// peek 读头尾各一小段，取 cwd / 标题 / 是否真的有对话。
func peek(path string) *Discovered {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}

	read := func(off int64, n int64) []byte {
		if n > st.Size()-off {
			n = st.Size() - off
		}
		if n <= 0 {
			return nil
		}
		buf := make([]byte, n)
		if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
			return nil
		}
		return buf
	}
	chunks := [][]byte{read(0, peekWindow)}
	if st.Size() > peekWindow {
		chunks = append(chunks, read(st.Size()-peekWindow, peekWindow))
	}

	var cwd, title string
	hasConversation := false
	for _, chunk := range chunks {
		for _, line := range strings.Split(string(chunk), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var r struct {
				Type        string `json:"type"`
				Cwd         string `json:"cwd"`
				CustomTitle string `json:"customTitle"`
				Title       string `json:"title"`
			}
			if json.Unmarshal([]byte(line), &r) != nil {
				continue // 头尾截断处必然有半行，跳过
			}
			if cwd == "" && r.Cwd != "" {
				cwd = r.Cwd
			}
			switch r.Type {
			case "custom-title":
				if r.CustomTitle != "" {
					title = r.CustomTitle
				}
			case "ai-title":
				if title == "" && r.Title != "" {
					title = r.Title
				}
			case "assistant":
				hasConversation = true
			}
		}
	}
	if !hasConversation || cwd == "" {
		return nil
	}
	if title == "" {
		title = filepath.Base(cwd)
	}
	return &Discovered{Cwd: cwd, Title: title}
}

// -- Claude 自己的登录 -------------------------------------------------------

func (claudeCode) AuthStatus() AuthState {
	out, err := exec.Command("claude", "auth", "status", "--json").Output()
	if err != nil {
		return AuthState{Supported: true}
	}
	var r struct {
		LoggedIn         bool   `json:"loggedIn"`
		AuthMethod       string `json:"authMethod"`
		Email            string `json:"email"`
		SubscriptionType string `json:"subscriptionType"`
	}
	if json.Unmarshal(out, &r) != nil {
		return AuthState{Supported: true}
	}
	return AuthState{
		Supported: true, LoggedIn: r.LoggedIn,
		AuthMethod: r.AuthMethod, Email: r.Email, Plan: r.SubscriptionType,
	}
}

// LoginSpec 起一个跑 `claude auth login` 的 PTY。
//
// 两个环境变量是必须的，而且都是**为了阻止服务器自己去开浏览器**：
//   - BROWSER=/usr/bin/true  让 xdg-open 之类的调用变成一个立刻成功的空操作
//   - 清掉 DISPLAY / WAYLAND_DISPLAY
//
// 面板跑在服务器上，用户在别的设备上看网页。服务器上开浏览器的结果只有两种：
// 没有图形会话时报错刷屏，有图形会话时**在服务器的屏幕上**弹出登录页——
// 那个屏幕没人看着。正确做法是把链接提取出来交给用户，在他自己的设备上打开。
func (claudeCode) LoginSpec(mode string) PtySpec {
	args := []string{"auth", "login", "--claudeai"}
	if mode == "console" {
		args = []string{"auth", "login", "--console"}
	}
	return PtySpec{
		File: "claude", Args: args, Cols: 100, Rows: 30,
		Env: []string{"BROWSER=/usr/bin/true", "DISPLAY=", "WAYLAND_DISPLAY="},
	}
}

func (claudeCode) Logout() error {
	return exec.Command("claude", "auth", "logout").Run()
}
