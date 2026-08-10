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
