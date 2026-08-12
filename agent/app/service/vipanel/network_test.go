package vipanel

import (
	"strings"
	"testing"
)

// 代理环境变量必须大小写两份都给：Go 和 Rust 的 http 客户端惯例不同，
// 只给一种的表现是「配了但某个 CLI 不认」。
func TestProxyEnvCoversBothCases(t *testing.T) {
	env := Proxy{URL: "http://127.0.0.1:7890"}.Env()
	joined := strings.Join(env, "\n")
	for _, k := range []string{"HTTP_PROXY=", "http_proxy=", "HTTPS_PROXY=", "https_proxy=", "ALL_PROXY=", "all_proxy="} {
		if !strings.Contains(joined, k) {
			t.Errorf("缺少 %s", k)
		}
	}
	// 回环不能走代理
	if !strings.Contains(joined, "NO_PROXY=") || !strings.Contains(joined, "127.0.0.1") {
		t.Error("NO_PROXY 必须包含回环地址")
	}
	if len(Proxy{}.Env()) != 0 {
		t.Error("没配代理时不该产生任何环境变量")
	}
}

func TestProxyValidate(t *testing.T) {
	ok := []string{"", "http://127.0.0.1:7890", "https://p.example.com:8443", "socks5://10.0.0.1:1080", "socks5h://a:b@h:1"}
	for _, u := range ok {
		if err := (Proxy{URL: u}).Validate(); err != nil {
			t.Errorf("%q 应当合法：%v", u, err)
		}
	}
	bad := []string{"127.0.0.1:7890", "ftp://h:1", "http://", "不是地址"}
	for _, u := range bad {
		if err := (Proxy{URL: u}).Validate(); err == nil {
			t.Errorf("%q 应当被拒绝", u)
		}
	}
}

// 两家的中转机制**不同**，这条锁住这个事实：
// claude 靠环境变量，codex 必须靠 -c 配置（它不认 OPENAI_BASE_URL）。
func TestProviderInjectionDiffersPerHarness(t *testing.T) {
	p := Provider{BaseURL: "https://relay.example.com", APIKey: "sk-test"}

	env, args := providerInjection(Get("claude-code"), p)
	if len(args) != 0 {
		t.Error("claude 不该需要命令行参数")
	}
	je := strings.Join(env, "\n")
	if !strings.Contains(je, "ANTHROPIC_BASE_URL=https://relay.example.com") {
		t.Errorf("claude 缺 base url：%v", env)
	}
	if !strings.Contains(je, "ANTHROPIC_AUTH_TOKEN=sk-test") {
		t.Errorf("claude 缺令牌：%v", env)
	}

	env, args = providerInjection(Get("codex"), p)
	ja := strings.Join(args, " ")
	if !strings.Contains(ja, `model_provider="vipanel"`) || !strings.Contains(ja, "base_url=") {
		t.Errorf("codex 的中转必须走 -c 配置：%v", args)
	}
	if strings.Contains(strings.Join(env, "\n"), "OPENAI_BASE_URL") {
		t.Error("codex 不认 OPENAI_BASE_URL，不该设置它")
	}
	if !strings.Contains(strings.Join(env, "\n"), "OPENAI_API_KEY=sk-test") {
		t.Errorf("codex 缺密钥：%v", env)
	}
}

// 只填一半是配错了，症状是「看起来配好了但请求全失败」。
func TestProviderRejectsHalfConfig(t *testing.T) {
	if err := SetProvider("claude-code", Provider{BaseURL: "https://x.com"}); err == nil {
		t.Error("只填地址应当被拒绝")
	}
	if err := SetProvider("claude-code", Provider{APIKey: "k"}); err == nil {
		t.Error("只填密钥应当被拒绝")
	}
	if err := SetProvider("shell", Provider{BaseURL: "https://x.com", APIKey: "k"}); err == nil {
		t.Error("shell 不支持中转，应当被拒绝")
	}
}

// 没启用中转时不该注入任何东西——否则会把官方登录顶掉。
func TestNoInjectionWhenProviderEmpty(t *testing.T) {
	env, args := providerInjection(Get("claude-code"), Provider{})
	if len(env) != 0 || len(args) != 0 {
		t.Errorf("空 provider 不该产生注入：env=%v args=%v", env, args)
	}
}

// 每个需要外网的 harness 都要声明端点；shell 不该声明。
func TestEndpointsDeclared(t *testing.T) {
	for _, id := range []string{"claude-code", "codex"} {
		d, ok := Get(id).(NetworkDeps)
		if !ok || len(d.Endpoints()) == 0 {
			t.Errorf("%s 应当声明依赖端点", id)
		}
		for _, e := range d.Endpoints() {
			if !strings.HasPrefix(e.URL, "https://") || e.Purpose == "" {
				t.Errorf("%s: 端点声明不完整 %+v", id, e)
			}
		}
	}
	if _, ok := Get("shell").(NetworkDeps); ok {
		t.Error("shell 不需要外网，不该声明端点")
	}
}
