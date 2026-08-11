package vipanel

import (
	"encoding/json"
	"os/exec"
	"strings"
)

func init() { register(&codexCLI{}) }

// codexCLI 是 OpenAI Codex CLI 的适配层。
//
// 和 harness_claude.go 一样的规矩：**所有 Codex 特有的知识都收在这个文件里**。
//
// 这个 harness 存在的意义不只是「多一个 agent」——它是对 Harness 抽象的第一次
// 真实检验。shellHarness 是人造的全 false 证伪件，Codex 是真家伙：它在若干处
// 和 Claude 结构性地不同，凡是抽象没接住的地方都会在这里显形。
//
// 下面每一条 Capabilities 的取值都是**查证过的**，不是照着 Claude 类推的。
// 查不动的一律声明 false —— 声明假的能力比没有这个能力糟得多：
// 前者是按钮点了没反应，后者是按钮根本不出现。
type codexCLI struct{}

func (codexCLI) ID() string          { return "codex" }
func (codexCLI) DisplayName() string { return "Codex" }

func (codexCLI) Capabilities() Capabilities {
	return Capabilities{
		// **false，因为还没验证过。**
		// Codex 的会话记录格式和落盘位置目前没有公开文档，而没登录就产生不了
		// 真实对话去反推。声明 false 时控制台只显示终端那一栏——
		// 这条路 shellHarness 已经走通过，界面本来就按能力表决定显不显示聊天区。
		StructuredEvents: false,

		// **false，因为做不到。**
		// codex 没有 --session-id 这类预先指定会话 id 的选项（实测 --help 里没有），
		// id 由它自己生成。`codex resume <UUID>` 倒是收 UUID，但那要求面板先
		// 「认领」它生成的 id——需要给会话表加一列外部 id 并在启动后去发现它。
		// 那是一块独立的工作，没做之前不能声明 Resume。
		//
		// 后果要说清楚：实例池驱逐对 Codex 会话**是有损的**（Manager 那边写的
		// 「驱逐是无损的」只对能 resume 的 harness 成立）。
		Resume: false,

		// **false，因为没验过。**
		// Esc 中断是 Claude TUI 的约定，不能想当然套到 Codex 上。
		// 没登录就没有「正在生成」的状态可以打断，验不了。
		Interrupt: false,

		// true：codex login / logout / login status 都实测跑过
		// （未登录时 login status 输出 "Not logged in" 且退出码 1）
		Auth: true,

		// 模型名单**留空**：没有可靠来源。编几个模型名比不给更糟——
		// 用户点了一个不存在的模型，TUI 只会回一句错误。
		Models: nil,

		// 这几档来自官方配置文档的 model_reasoning_effort
		EffortLevels: []string{"minimal", "low", "medium", "high", "xhigh"},

		// **只给设备码这一种。**
		//
		// codex 其实有三种登录入口，另外两种在这里都是错的：
		//   - 默认的 `codex login` 会在服务器上起一个 localhost:1455 的回调，
		//     授权链接里的 redirect_uri 指向的是**服务器自己的** localhost，
		//     坐在另一台机器前的用户根本够不着。codex 自己也这么说：
		//     "On a remote or headless machine? Use `codex login --device-auth`
		//     instead." —— 而这个面板按定义就是那台远程无头机器。
		//   - `--with-api-key` 从标准输入读密钥，意味着把一个长期有效的 key
		//     经浏览器、WebSocket、伪终端一路传下去。要用密钥的人在服务器上
		//     自己敲一次就行，不必让面板经手。
		//
		// 设备码流的码是**终端给、用户拿到别的设备上输**，所以 NeedsCodeInput
		// 是 false —— 对话框不该再要用户粘什么回来。
		LoginModes: []LoginMode{{ID: "devicecode", NeedsCodeInput: false}},

		Commands: []Command{
			{"/init", "生成 AGENTS.md"},
			{"/status", "查看会话设置"},
			{"/permissions", "配置允许的操作"},
			{"/model", "切换模型与推理强度"},
			{"/review", "代码审查"},
		},
	}
}

func (codexCLI) Binary() string { return "codex" }

// InstallPlan：npm 上的 @openai/codex，bin 名 codex。
// 版本号能对上——registry 上的 latest 和实测机器上 `codex --version`
// 报的 codex-cli 0.147.0 是同一个。
func (codexCLI) InstallPlan() InstallPlan {
	return InstallPlan{
		Installable: true,
		Requires:    []Prereq{{Binary: "npm", Hint: nodeHint}},
		Spec: PtySpec{
			File: "npm",
			Args: []string{"install", "-g", "@openai/codex"},
			Cols: agentCols, Rows: agentRows,
		},
		Note: "从 npm 全局安装 @openai/codex",
	}
}

// Spawn 起一个交互式 Codex。
//
// 配置**全部走 -c 命令行覆盖，一个字节都不写进用户的 ~/.codex/config.toml**。
// 这不是偷懒，是三个实际好处：
//   - 不会写坏用户的配置（Claude 那侧就为并发写 ~/.claude.json 折腾过一轮）
//   - 不需要引 TOML 依赖（agent/go.mod 是 rebase 冲突面）
//   - 关掉功能时没有残留要清理
//
// -c 的值按 TOML 解析，嵌套结构可以内联表达——实测合法的能过、写错的会被
// 明确报错（"invalid type: string, expected a sequence in hooks"）。
func (c codexCLI) Spawn(ctx SpawnContext) PtySpec {
	args := []string{
		// 这里**没有** --skip-git-repo-check。
		// 那个参数属于 `codex exec`，不属于交互式 `codex`；带上它进程会以
		// "unexpected argument '--skip-git-repo-check' found" 立刻退出，
		// 表现为会话建好了、alive 是 true、屏幕上什么都没有。
		// 第一版就是照着 `codex exec --help` 里那句报错抄的，真机一跑就现形。

		// **不要沙箱。** 这正是这个面板存在的前提：agent 要能管整台机器，
		// 关进沙箱就只能管一个目录（PORT-1PANEL.md §1.2.5）。
		// 唯一的护栏是下面那个钩子，和 Claude 那侧是同一条边界。
		"--sandbox", "danger-full-access",
		"--ask-for-approval", "on-request",
	}

	if hook := hookPath(); hook != "" {
		args = append(args, "-c", codexHookConfig(hook))
		// 不加这个，非「受信」的钩子会被**静默跳过**——会话照跑，
		// 但一次工具调用都不经过面板确认，而界面上看不出任何异常。
		// 这个标志名字很吓人，但这里的取舍是明确的：钩子是面板自己装的、
		// 路径由 agent 自己的可执行文件位置推出来的，来源本来就是被审过的；
		// 而另一种选择是让用户在一个没有护栏的会话里以为自己有护栏。
		args = append(args, "--dangerously-bypass-hook-trust")
	}
	if mcp := mcpPath(); mcp != "" && MCPEnabled() {
		args = append(args, "-c", `mcp_servers.vipanel={command="`+mcp+`"}`)
	}

	return PtySpec{
		File: "codex",
		Args: args,
		Cwd:  ctx.Cwd,
		Env:  ctx.Env,
		Cols: ctx.Cols,
		Rows: ctx.Rows,
	}
}

// codexHookConfig 拼出 PreToolUse 的内联 TOML。
//
// matcher 用 ".*" 拦下所有工具，理由和 Claude 那侧的 "*" 一样：没有沙箱兜底时，
// 白名单式的部分拦截等于给自己留一堆想不到的口子。
//
// 官方文档说 PreToolUse 覆盖 Bash、apply_patch（含 Edit/Write 别名）、MCP 工具
// 和本地函数工具，但**不覆盖 WebSearch 这类托管工具**——这一条是 Codex 与
// Claude 的实质差别，写进 docs/trust-model.md 了。
//
// timeout 给 150 秒：要大于面板 120 秒的决定超时，又要远小于 Codex 默认的
// 600 秒（那一层到点之后的行为不由我们控制）。和 vipanel-hook 里的取值一致。
func codexHookConfig(hook string) string {
	cmd, _ := json.Marshal(hook) // 借 JSON 来加引号转义，TOML 的字符串语法在这里是兼容的
	return `hooks.PreToolUse=[{matcher=".*", hooks=[{type="command", command=` +
		string(cmd) + `, timeout=150}]}]`
}

// Submit：Codex 的 TUI 和 Claude 一样是「正文和回车分开写」还是可以合并，
// 没有验证过。这里沿用分开写——它对两种情况都是安全的：
// 就算 TUI 能接受合并的写法，分开写也只是多一次 write。
// 反过来（合并写但 TUI 不接受）会让消息永远发不出去，那是 Claude 那侧踩过的坑。
func (codexCLI) Submit(write func([]byte), text string) {
	claudeCode{}.Submit(write, text)
}

// Interrupt 声明为不支持（见 Capabilities），这里给一个空实现。
// 面板不会调它——canInterrupt 是按能力表决定的。
func (codexCLI) Interrupt(func([]byte)) {}

// HasHistory 恒为 false：没有 Resume 能力，问这个没有意义。
func (codexCLI) HasHistory(string, string) bool { return false }

// TimeoutDecision：面板等不到人时给 Codex 的答复是**拒绝**，不是退回终端。
//
// Claude 那侧可以退回 ask —— 它的 TUI 会自己弹确认，那边至少还有个人能看见。
// Codex 的 PreToolUse 只认 allow / deny 两种决定，没有 ask 这一档，退无可退。
// 在「放行」和「拒绝」之间，没有沙箱兜底的机器上只能选拒绝。
func (codexCLI) TimeoutDecision() Decision { return DecideDeny }

// -- 登录 ---------------------------------------------------------------------

func (c codexCLI) AuthStatus() AuthState {
	st := AuthState{Supported: true, HookInstalled: HookInstalled()}
	out, err := exec.Command("codex", "login", "status").CombinedOutput()
	if err != nil {
		// 未登录时退出码就是 1，这不是"命令坏了"。实测输出是 "Not logged in"。
		return st
	}
	line := strings.TrimSpace(string(out))
	if line == "" || strings.Contains(strings.ToLower(line), "not logged in") {
		return st
	}
	st.LoggedIn = true
	// 输出里通常带着账号信息，原样带给界面即可——
	// 与其解析一个没文档的格式，不如把原文显示出来。
	st.Email = line
	return st
}

// LoginSpec 起一个设备码登录。
//
// 输出实测长这样（面板会把链接挑出来单独推给前端，一次性码原样显示）：
//
//  1. Open this link in your browser and sign in to your account
//     https://auth.openai.com/codex/device
//  2. Enter this one-time code (expires in 15 minutes)
//     K33V-9MBAJ
//
// BROWSER / DISPLAY 的处理和 Claude 那侧一样：不让它去开一个根本不存在的浏览器。
func (codexCLI) LoginSpec(string) PtySpec {
	return PtySpec{
		File: "codex",
		Args: []string{"login", "--device-auth"},
		Env:  []string{"BROWSER=/usr/bin/true", "DISPLAY=", "WAYLAND_DISPLAY="},
		Cols: agentCols,
		Rows: agentRows,
	}
}

func (codexCLI) Logout() error {
	return exec.Command("codex", "logout").Run()
}

// -- 运行中调整 ---------------------------------------------------------------

// Codex 的斜杠命令和 Claude 同形（/model 之类），沿用同一套发送方式。
// CycleMode 没有对应物：Codex 的审批策略是启动参数 --ask-for-approval，
// 不是一个可以在会话中循环切换的模式，所以这里是空实现，
// 而 Capabilities.Interrupt=false 让界面根本不显示那个按钮。
func (codexCLI) CycleMode(func([]byte)) {}

func (c codexCLI) SetModel(write func([]byte), v string)  { codexSlash(write, "/model "+v) }
func (c codexCLI) SetEffort(write func([]byte), v string) { codexSlash(write, "/model "+v) }

func codexSlash(write func([]byte), cmd string) {
	claudeCode{}.Submit(write, cmd)
}

// ReadMode 返回空串：Codex 底栏没有 Claude 那样的常驻模式指示，
// 没有可读的事实源就不要编一个出来。
func (codexCLI) ReadMode([]byte) string { return "" }
