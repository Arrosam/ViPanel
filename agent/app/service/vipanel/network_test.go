package vipanel

import (
	"strings"
	"testing"
)

// 代理环境变量必须大小写两份都给：Go 和 Rust 的 http 客户端惯例不同，
// 只给一种的表现是「配了但某个 CLI 不认」。
func TestProxyEnvCoversBothCases(t *testing.T) {
	env := Proxy{Enabled: true, URL: "http://127.0.0.1:7890"}.Env()
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
	if len(Proxy{Enabled: true}.Env()) != 0 {
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

// 开关关掉时不能注入任何东西——否则「关了代理」只是界面上关了。
// 地址要留着：关开一次就得重填是很烦人的设计。
func TestProxyDisabledInjectsNothing(t *testing.T) {
	p := Proxy{Enabled: false, URL: "http://127.0.0.1:7890"}
	if len(p.Env()) != 0 {
		t.Error("关掉的代理不该产生环境变量")
	}
	if p.Active() {
		t.Error("关掉的代理不该是 active")
	}
	if p.URL == "" {
		t.Error("关掉之后地址应当保留")
	}
	// 开了但没填地址，同样不生效
	if (Proxy{Enabled: true}).Active() {
		t.Error("开了但没地址不该是 active")
	}
}
