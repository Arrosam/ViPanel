package vipanel

import (
	"strings"
	"testing"
)

// 每个注册的 harness 都必须声明一个非空的 Binary()。
// 空串会让 Installed() 恒为 false，界面上那个 harness 永远显示「未安装」——
// 一个静默的、看起来像环境问题的失败。
func TestEveryHarnessDeclaresBinary(t *testing.T) {
	for _, h := range List() {
		if strings.TrimSpace(h.Binary()) == "" {
			t.Errorf("%s: Binary() 为空", h.ID())
		}
	}
}

// 声明了 Auth 的 harness 必须同时实现 Authenticator 并给出至少一种登录方式，
// 否则界面会显示一个「去登录」按钮，点开是一个没有任何选项的对话框。
func TestAuthCapabilityIsBackedByImplementation(t *testing.T) {
	for _, h := range List() {
		c := h.Capabilities()
		if !c.Auth {
			continue
		}
		if _, ok := h.(Authenticator); !ok {
			t.Errorf("%s: 声明了 Auth 但没实现 Authenticator", h.ID())
		}
		if len(c.LoginModes) == 0 {
			t.Errorf("%s: 声明了 Auth 但没有任何 LoginModes", h.ID())
		}
	}
}

func TestCodexRegistered(t *testing.T) {
	h := Get("codex")
	if h.ID() != "codex" {
		t.Fatalf("codex 没注册上，Get 退回了 %s", h.ID())
	}
	if h.Capabilities().Resume {
		t.Error("Resume 必须为 false：codex 没有 --session-id，面板无法预先指定会话 id")
	}
}

// Codex 的 PreToolUse 只认 allow / deny。面板等不到人时**绝不能**回 ask ——
// 那是一个 codex 不认识的值，落到它自己的审批策略上，
// 而结果取决于我们没有控制的一层。
func TestCodexTimeoutDenies(t *testing.T) {
	d, ok := Get("codex").(PermissionDialect)
	if !ok {
		t.Fatal("codex 应当实现 PermissionDialect")
	}
	if got := d.TimeoutDecision(); got != DecideDeny {
		t.Fatalf("超时决定 = %q，要的是 deny", got)
	}
}

// 没实现 PermissionDialect 的 harness 保持原来的行为（ask）。
// 这条锁住的是「加一个可选接口不会改变既有 harness 的行为」。
func TestTimeoutDefaultsToAsk(t *testing.T) {
	if _, ok := Get("claude-code").(PermissionDialect); ok {
		t.Skip("claude 现在自己声明了方言，这条不再适用")
	}
	if got := timeoutDecisionFor("不存在的会话"); got != DecideAsk {
		t.Fatalf("默认超时决定 = %q，要的是 ask", got)
	}
}

// 启动参数必须关掉沙箱——关进沙箱的 agent 只能管一个目录，面板就没意义了。
//
// 同样重要的是**不能**出现 --skip-git-repo-check：那是 `codex exec` 的参数，
// 交互式 codex 见到它会以 "unexpected argument" 立刻退出。这条负向断言是
// 真机验证换来的，别把它当多余的。
func TestCodexSpawnArgs(t *testing.T) {
	spec := Get("codex").Spawn(SpawnContext{Cwd: "/tmp", Cols: 80, Rows: 24})
	if spec.File != "codex" {
		t.Fatalf("File = %q", spec.File)
	}
	joined := strings.Join(spec.Args, " ")
	if !strings.Contains(joined, "--sandbox danger-full-access") {
		t.Errorf("启动参数缺少沙箱豁免：%s", joined)
	}
	if strings.Contains(joined, "--skip-git-repo-check") {
		t.Errorf("--skip-git-repo-check 只属于 codex exec，交互式 codex 会直接退出：%s", joined)
	}
}

// 钩子配置必须是 codex 能解析的内联 TOML，且路径要被正确加引号。
// 拼错了的后果不是报错，是**钩子被静默跳过**——会话照跑，护栏没了。
func TestCodexHookConfigQuotesPath(t *testing.T) {
	got := codexHookConfig(`/opt/vi panel/vipanel-hook`)
	for _, want := range []string{
		`hooks.PreToolUse=[{matcher=".*"`,
		`command="/opt/vi panel/vipanel-hook"`,
		`timeout=150`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少 %q\n实际: %s", want, got)
		}
	}
}

// 一次性码要能从真实输出里挑出来——连带 ANSI 转义一起。
//
// 样本是真机上跑 `codex login --device-auth` 抓的原文。
func TestCodexLoginCodeFromRealOutput(t *testing.T) {
	real := "2. Enter this one-time code \x1b[90m(expires in 15 minutes)\x1b[0m\r\n" +
		"   \x1b[94mNTDC-6BNE8\x1b[0m\r\n"
	if got := Get("codex").(LoginCodeSource).LoginCode([]byte(real)); got != "NTDC-6BNE8" {
		t.Errorf("挑出来的是 %q，要的是 NTDC-6BNE8", got)
	}
}

// 不该在普通输出里认出码——给用户一个错的码比不给更糟。
func TestCodexLoginCodeDoesNotFalsePositive(t *testing.T) {
	src := Get("codex").(LoginCodeSource)
	for _, s := range []string{
		"Welcome to Codex [v0.147.0]",
		"1. Open this link in your browser and sign in to your account",
		"   https://auth.openai.com/codex/device",
		"Continue only if you started this login in Codex.",
		"added 2 packages in 5s",
	} {
		if got := src.LoginCode([]byte(s)); got != "" {
			t.Errorf("%q 里不该认出码，却得到 %q", s, got)
		}
	}
}

// Claude 的登录方向相反（浏览器给码、粘回终端），它不该声明这个能力。
func TestClaudeHasNoLoginCode(t *testing.T) {
	if _, ok := Get("claude-code").(LoginCodeSource); ok {
		t.Error("claude 的码是粘回终端的，不该声明 LoginCodeSource")
	}
}

// CODEX_HOME 必须被认——写死 ~/.codex 会让快照读写一个 codex 不看的位置。
func TestCodexHonorsCodexHome(t *testing.T) {
	t.Setenv("CODEX_HOME", "/tmp/vp-codex-home-test")
	arts := Get("codex").(AccountStore).AccountArtifacts()
	if len(arts) == 0 || !strings.HasPrefix(arts[0].Path, "/tmp/vp-codex-home-test/") {
		t.Errorf("CODEX_HOME 没被认，artifact 路径是 %v", arts)
	}
}

// 可达性探测不能用会触发人机校验的地址。
//
// auth.openai.com 根路径挂着 Cloudflare 的 "Just a moment"，任何来源都回 403，
// 包括完全没有地区限制的机器。用它当判据会把正常机器误报成被封锁。
func TestCodexEndpointsAvoidChallengePages(t *testing.T) {
	for _, e := range Get("codex").(NetworkDeps).Endpoints() {
		if e.URL == "https://auth.openai.com/" || e.URL == "https://auth.openai.com" {
			t.Errorf("不能拿 auth.openai.com 根路径做可达性判据（它对所有来源都回 403）")
		}
	}
}
