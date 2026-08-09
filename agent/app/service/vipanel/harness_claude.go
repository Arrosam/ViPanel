package vipanel

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
		Commands: []Command{
			{"/model", "切换模型"},
			{"/effort", "切换推理强度"},
			{"/clear", "清空当前对话"},
			{"/compact", "压缩上下文"},
			{"/status", "查看会话状态"},
			{"/cost", "查看本次用量"},
			{"/init", "生成 CLAUDE.md"},
			{"/review", "代码审查"},
			{"/theme", "切换主题"},
			{"/help", "帮助"},
		},
	}
}

// Spawn：首次用 --session-id 指定 id，之后用 --resume 接回。
//
// --resume 不带 --fork-session 时**复用**原 session id，transcript 还是同一个
// 文件，历史不丢。带上 --fork-session 会分叉出新 id，那不是我们要的。
func (claudeCode) Spawn(ctx SpawnContext) PtySpec {
	// 起进程前先把首次运行向导标记掉，否则会话会卡在向导里，
	// 而向导只存在于 agent 屏幕上，transcript 里一个字都没有
	ensureOnboarded(ctx.Cwd)
	ensureHook()
	ensureMCP()

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

// -- 首次运行向导 -----------------------------------------------------------

// ensureOnboarded 把 claude 的首次运行向导标记为已完成。
//
// 为什么要做这件事：全新机器上第一次跑 claude 会进一个交互式向导
// （选主题 → 选登录方式 → 授权）。面板**已经**通过自己的登录流程做过授权了，
// 再让用户在 agent 屏幕里把同一件事重做一遍纯属多余；更糟的是，
// 如果用户不知道要去看 agent 屏幕，会话就静静地卡在那儿，
// 表现为「发消息没反应」——极难查。
//
// 只补缺失的键，绝不覆盖已有值：用户自己选过的主题必须留着。
//
// projects[cwd].hasTrustDialogAccepted 也一并补上。那个对话框问的是
// 「你信任这个目录里的文件吗」，而用户刚刚**指定这个目录建了一个会话**，
// 那就是同一件事的答复。
func ensureOnboarded(cwd string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(home, ".claude.json")

	cfg := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(raw, &cfg) != nil {
			return // 解析不了就别动它，写坏了比不写糟得多
		}
	}

	changed := false
	setIfAbsent := func(k string, v any) {
		if _, ok := cfg[k]; !ok {
			cfg[k] = v
			changed = true
		}
	}
	setIfAbsent("hasCompletedOnboarding", true)
	setIfAbsent("theme", "dark")

	// 一次性提示（新渲染器推荐、版本更新说明之类）同样会挡在输入框前面。
	// 它们各自有一个计数器/水位线，预置成「已经看过」即可跳过。
	//
	// 这是一份**会过时的清单**：claude 每个版本都可能新增这类提示，
	// 而新的那一个我们不认识。所以它只是把常见情况铺平，不是根治。
	// 真正的兜底是 agent PTY 一直有人在读（见 mirror.go）——
	// 那保证的是「不会因为缓冲写满而死锁」，不是「不会被弹窗挡住」。
	setIfAbsent("fullscreenUpsellSeenCount", 99)
	setIfAbsent("passesUpsellSeenCount", 99)
	if v := claudeVersion(); v != "" {
		setIfAbsent("lastReleaseNotesSeen", v)
	}

	if cwd != "" {
		projects, _ := cfg["projects"].(map[string]any)
		if projects == nil {
			projects = map[string]any{}
		}
		p, _ := projects[cwd].(map[string]any)
		if p == nil {
			p = map[string]any{}
		}
		if v, ok := p["hasTrustDialogAccepted"]; !ok || v != true {
			p["hasTrustDialogAccepted"] = true
			projects[cwd] = p
			cfg["projects"] = projects
			changed = true
		}
	}
	if !changed {
		return
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	_ = writeConfig(path, raw)
}

// claudeVersion 取「2.1.220」这样的版本号，取不到返回空串。
func claudeVersion() string {
	out, err := exec.Command("claude", "--version").Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// writeConfig 原子地写一个配置文件：先写同目录的临时文件，再 rename 覆盖。
//
// 直接 os.WriteFile 是「先截断再写」，中间有一个窗口能被读到半截文件。
// 而 ~/.claude.json 是 claude 自己也在读写的——它随时可能在我们写到一半时去读。
// 实测 20 次并发 spawn/exit 没撞出来（写 34KB 的窗口很短），
// 但那是「撞不出来」不是「撞不了」。rename 在同一文件系统上是原子的，
// 读者要么看到旧的完整文件、要么看到新的完整文件，没有中间态。
//
// 注意这解决不了「丢更新」：我们读-改-写的间隙里 claude 写了什么，
// 会被我们的写覆盖掉。要根治那个得上文件锁，而 claude 并不参与任何锁协议，
// 所以那一半只能接受——影响是它可能丢一条刚写的 project 记录，不致命。
func writeConfig(path string, raw []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".vipanel-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // rename 成功后这个是空操作

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// hookPath 是权限代理可执行体的位置。与 agent 二进制同目录。
func hookPath() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	p := filepath.Join(filepath.Dir(self), "vipanel-hook")
	if st, err := os.Stat(p); err != nil || st.Mode()&0o111 == 0 {
		return "" // 不存在或不可执行
	}
	return p
}

// HookInstalled 供界面判断要不要标出「权限代理未生效」。
func HookInstalled() bool { return hookPath() != "" }

// AutoAllow：claude 自己的内部机制类工具，不该打扰人。
//
//   - ToolSearch —— tool search 默认开启，MCP 工具的 schema 被 defer，
//     模型每次要用面板工具都先走它一次。实测它**确实会过 PreToolUse 钩子**，
//     不放行的话每找一次工具就弹一次窗，agent 直接卡死在那儿。
//     它只读工具定义，不碰任何东西。
//   - WaitForMcpServers —— 关掉 tool search 时的替代品，同样只是等待。
func (claudeCode) AutoAllow(tool string) bool {
	switch tool {
	case "ToolSearch", "WaitForMcpServers":
		return true
	}
	return false
}

func mcpPath() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	p := filepath.Join(filepath.Dir(self), "vipanel-mcp")
	if st, err := os.Stat(p); err != nil || st.Mode()&0o111 == 0 {
		return ""
	}
	return p
}

// MCPInstalled 供界面判断面板操作能力在不在。
func MCPInstalled() bool { return mcpPath() != "" }

// ensureMCP 把 ViPanel 的 MCP 服务写进 claude 的用户级配置。
//
// **user scope（~/.claude.json 顶层 mcpServers）而不是 project scope**：
// project scope 要往用户的项目目录写 .mcp.json（污染用户仓库），而且每个新目录
// 都要交互式批准——会话每换一个 cwd 卡一次批准，这个功能就废了。
//
// user scope 的代价是它也会出现在用户自己终端里跑的 claude 上，
// 但那个进程不是面板 spawn 的后代，连上来会被血缘校验拒掉（ancestry.go），
// 所以外溢只是「配置看得到」，不是「能用」。
//
// timeout 必须显式给：装应用这类操作撞得上默认值。
func ensureMCP() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(home, ".claude.json")

	cfg := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(raw, &cfg) != nil {
			return // 解析不了就别动，写坏用户的配置比不写糟得多
		}
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}

	bin := mcpPath()
	if bin == "" || !MCPEnabled() {
		// 二进制不在、或用户在设置里关掉了：把配置**删掉**，不留死引用。
		// 留着的话 claude 每次起会话都会去连一个连不上的服务。
		if _, ok := servers["vipanel"]; !ok {
			return
		}
		delete(servers, "vipanel")
	} else {
		want := map[string]any{
			"type":    "stdio",
			"command": bin,
			"timeout": float64(600000),
		}
		if same(servers["vipanel"], want) {
			return
		}
		servers["vipanel"] = want
	}
	cfg["mcpServers"] = servers

	if raw, err := json.MarshalIndent(cfg, "", "  "); err == nil {
		_ = writeConfig(path, raw)
	}
}

// -- PermissionStore：「总是允许」名单 ----------------------------------------

func claudeSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "settings.json")
}

func (c claudeCode) AlwaysAllowed(tool string) bool {
	p := claudeSettingsPath()
	if p == "" {
		return false
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	var cfg struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return false
	}
	for _, v := range cfg.Permissions.Allow {
		if v == tool {
			return true
		}
	}
	return false
}

func (c claudeCode) AddAlwaysAllow(tool string) error {
	p := claudeSettingsPath()
	if p == "" {
		return os.ErrNotExist
	}
	cfg := map[string]any{}
	if raw, err := os.ReadFile(p); err == nil {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return err // 解析不了就别动
		}
	}
	perms, _ := cfg["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	list, _ := perms["allow"].([]any)
	for _, v := range list {
		if s, ok := v.(string); ok && s == tool {
			return nil
		}
	}
	perms["allow"] = append(list, tool)
	cfg["permissions"] = perms

	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeConfig(p, raw)
}

// ensureHook 把 PreToolUse 钩子写进 claude 的用户级 settings.json。
//
// 用 matcher "*" 拦下**所有**工具：这个面板没有沙箱兜底，
// 白名单式的部分拦截等于给自己留一堆想不到的口子。
// AskUserQuestion 也在其中——它同样走这条通道（见 ROADMAP P1）。
//
// 找不到 hook 可执行体时**主动把配置摘掉**，而不是留一条指向不存在文件的
// 命令：后者会让 claude 每次调工具都报一次钩子执行失败。
func ensureHook() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".claude")
	path := filepath.Join(dir, "settings.json")

	cfg := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(raw, &cfg) != nil {
			return // 解析不了就别动，写坏用户的配置比不写糟得多
		}
	}

	hooks, _ := cfg["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	bin := hookPath()
	if bin == "" {
		if _, ok := hooks["PreToolUse"]; !ok {
			return
		}
		delete(hooks, "PreToolUse")
	} else {
		want := []any{map[string]any{
			"matcher": "*",
			"hooks":   []any{map[string]any{"type": "command", "command": bin}},
		}}
		if same(hooks["PreToolUse"], want) {
			return
		}
		hooks["PreToolUse"] = want
	}
	cfg["hooks"] = hooks

	if raw, err := json.MarshalIndent(cfg, "", "  "); err == nil {
		_ = writeConfig(path, raw)
	}
}

func same(a, b any) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(x) == string(y)
}

// -- 运行中调整 -------------------------------------------------------------

// CycleMode 就是 Shift+Tab。TUI 用它循环 auto / manual / plan。
func (claudeCode) CycleMode(write func([]byte)) { write([]byte("\x1b[Z")) }

func (c claudeCode) SetModel(write func([]byte), v string)  { c.slash(write, "/model "+v) }
func (c claudeCode) SetEffort(write func([]byte), v string) { c.slash(write, "/effort "+v) }

// slash 发一条斜杠命令。和普通消息一样，正文与回车必须拆开写。
func (claudeCode) slash(write func([]byte), cmd string) {
	write([]byte(cmd))
	time.Sleep(200 * time.Millisecond)
	write([]byte("\r"))
}

// modeLine 匹配底栏的模式指示。TUI 在不同版本里写法不一样，
// 所以三种都认，而不是死抠某一种。
// 底栏的三种写法都认，而不是死抠某一种：
//
//	⏸ manual mode on · ...
//	⏵⏵ accept edits on (shift+tab to cycle)
//	⏸ plan mode on
var modeLine = regexp.MustCompile(`(?i)(auto|manual|plan|bypass)\s+mode\s+on|(accept edits)\s+on`)

// ReadMode 从屏幕字节里读当前模式。
//
// 之所以读屏幕而不是记一个本地变量：用户完全可以在终端里自己按 Shift+Tab，
// 那时面板记的值就错了。屏幕是唯一的事实来源。
func (claudeCode) ReadMode(screen []byte) string {
	clean := csiPattern.ReplaceAll(screen, nil)
	// 只看最后 4KB：底栏总在最新的输出里，全量扫会撞上历史里的同类字样
	if len(clean) > 4096 {
		clean = clean[len(clean)-4096:]
	}
	// 取**最后一个**匹配，不是第一个。
	// 窗口里混着历史输出，正着扫会抓到早先那次的模式；
	// 底栏是屏幕上最新的东西，只有最后一条才是当前值。
	ms := modeLine.FindAllSubmatch(clean, -1)
	if len(ms) == 0 {
		return ""
	}
	for _, g := range ms[len(ms)-1][1:] {
		if len(g) > 0 {
			return strings.ToLower(string(g))
		}
	}
	return ""
}
