package vipanel

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 出站：代理、中转端点、可达性检查。
//
// 存在的直接原因是一台香港的服务器：TCP 和 TLS 都正常，但
// api.anthropic.com 回 403 "Request not allowed"，api.openai.com 回
// 403 unsupported_country_region_territory。表现是登录卡在最后一步、
// 会话发不出消息，而界面上没有任何线索。
//
// 解决办法是让出站走一个能到境外的代理（通常是本机 VPN 客户端开的
// 本地端口，比如 127.0.0.1:7890）。
//
// **中转端点（第三方 API relay）这条路有意不做**：那要放弃官方订阅、
// 改成按 key 计费，而且代码要经过第三方。用代理的话官方订阅照用、
// OAuth 登录也能走完，是同一个问题更干净的解法。
//
// 分工：代理是**通用的**——HTTPS_PROXY 那套环境变量两个 CLI 都认
//（从二进制里查过），所以注入逻辑不进 harness 接口；而依赖哪些端点
// 因 harness 而异，由各自声明。

// Proxy 是面板级的出站代理设置。
type Proxy struct {
	// Enabled 是总开关。
	//
	// 和「URL 留空」分开是有意的：关掉代理时地址要留着，
	// 否则用户每次开关都得重新填一遍。
	Enabled bool `json:"enabled"`
	// URL 形如 http://127.0.0.1:7890 或 socks5://127.0.0.1:1080。
	URL string `json:"url"`
}

// Active 表示这份配置现在真的会生效。
func (p Proxy) Active() bool { return p.Enabled && strings.TrimSpace(p.URL) != "" }

// Env 把代理翻成子进程的环境变量。
//
// 大小写两份都给：Go 和 Rust 的 http 客户端惯例不同，有的只看小写。
// 多给几个变量的成本是零，漏一个的代价是「配了但没生效」。
//
// NO_PROXY 里必须有回环：面板自己的 unix socket 走不到代理，但 agent
// 里的工具可能去访问 127.0.0.1 上的服务，把它们也塞进代理是错的。
func (p Proxy) Env() []string {
	if !p.Active() {
		return nil
	}
	u := strings.TrimSpace(p.URL)
	noProxy := "localhost,127.0.0.1,::1"
	return []string{
		"HTTP_PROXY=" + u, "http_proxy=" + u,
		"HTTPS_PROXY=" + u, "https_proxy=" + u,
		"ALL_PROXY=" + u, "all_proxy=" + u,
		"NO_PROXY=" + noProxy, "no_proxy=" + noProxy,
	}
}

// Validate 在存下来之前检查一次格式。
// 存一个拼错的代理，症状是所有出站请求静默失败——比不配还难查。
func (p Proxy) Validate() error {
	u := strings.TrimSpace(p.URL)
	if u == "" {
		return nil
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return fmt.Errorf("代理地址解析失败: %v", err)
	}
	switch parsed.Scheme {
	case "http", "https":
	case "socks5", "socks5h":
		// **两个 CLI 都不会说 SOCKS5**，这是真机上撞出来的：
		//   codex  带 socks5 时设备码登录报 "error sending request"——
		//          比不配代理还糟，不配至少能拿到一个看得懂的 403。
		//   claude 是 Node，undici 默认不支持 socks。
		// 面板自己的探测用 Go 客户端，socks5 是能走通的，于是会出现
		// 「可达性全绿但登录仍然失败」这种最难查的错位。所以直接拒绝。
		return fmt.Errorf("暂不支持 SOCKS 代理：Claude Code 与 Codex 都只认 HTTP 代理。" +
			"如果你的代理软件同时提供 HTTP 端口，请改填 http://…")
	default:
		return fmt.Errorf("不支持的代理协议 %q，只支持 http / https", parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("代理地址缺少主机名")
	}
	return nil
}

// -- 可达性检查 ---------------------------------------------------------------

// Endpoint 是一个 harness 必须能连上的地址。
type Endpoint struct {
	URL string
	// Purpose 是给人看的用途，出问题时界面直接显示它。
	Purpose string
}

// NetworkDeps 由「需要连外网」的 harness 实现。shell 不需要。
type NetworkDeps interface {
	Endpoints() []Endpoint
}

// ReachResult 是一次探测的结果。
type ReachResult struct {
	URL     string `json:"url"`
	Purpose string `json:"purpose"`
	OK      bool   `json:"ok"`
	// Status 是 HTTP 状态码，0 表示连都没连上。
	Status int `json:"status"`
	// Detail 是给人看的一句话，**要说清是什么类型的失败**：
	// 地区封锁和网络不通的处理方式完全不同，混成一句「连接失败」等于没说。
	Detail string `json:"detail"`
}

// CheckReachability 探测某个 harness 依赖的全部端点。
//
// 探测本身走已配置的代理——否则配了代理之后检查仍然报红，
// 用户会以为代理没生效。
func CheckReachability(harnessID string, p Proxy) []ReachResult {
	deps, ok := Get(harnessID).(NetworkDeps)
	if !ok {
		return nil
	}
	client := newProbeClient(p)

	var out []ReachResult
	for _, e := range deps.Endpoints() {
		out = append(out, probe(client, e))
	}
	return out
}

// newProbeClient 造一个探测用的 http 客户端。
// 抽成函数是为了让测试用**同一套行为**，而不是自己造一个近似的。
func newProbeClient(p Proxy) *http.Client {
	client := &http.Client{
		Timeout: 12 * time.Second,
		// **不跟随重定向。**
		//
		// 3xx 本身已经证明请求到达了服务端，跟过去反而会落到别的页面上：
		// auth.openai.com/codex/device 会 302 到一个挂着 Cloudflare 人机校验
		// 的地址，那个地址对任何来源都回 403——于是一台完全正常的机器
		// 被报成「被地区拒绝」。curl 不跟随所以看到的是 302，Go 默认跟随，
		// 两边结论不同正是这么来的。
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if u := strings.TrimSpace(p.URL); u != "" {
		if parsed, err := url.Parse(u); err == nil {
			client.Transport = &http.Transport{Proxy: http.ProxyURL(parsed)}
		}
	}

	if p.Active() {
		if parsed, err := url.Parse(strings.TrimSpace(p.URL)); err == nil {
			client.Transport = &http.Transport{Proxy: http.ProxyURL(parsed)}
		}
	}
	return client
}

func probe(client *http.Client, e Endpoint) ReachResult {
	r := ReachResult{URL: e.URL, Purpose: e.Purpose}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL, nil)
	if err != nil {
		r.Detail = "地址不合法：" + err.Error()
		return r
	}
	resp, err := client.Do(req)
	if err != nil {
		r.Detail = "连不上：" + trimErr(err)
		return r
	}
	defer resp.Body.Close()
	r.Status = resp.StatusCode

	// 401/404/3xx 都算通：我们没带凭据也不跟跳转，能收到这类应答
	// 就说明请求到达了服务端。
	// **要区分出来的是 403**——地区封锁就长这样，而它和「网络不通」
	// 的解决办法完全不同。
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		r.OK = true // 重定向说明服务端收到了请求
	case resp.StatusCode == http.StatusForbidden:
		r.Detail = "返回 403，通常是按出口地区拒绝。请在设置里开启网络代理，指向一个能到目标地区的出口。"
	case resp.StatusCode >= 500:
		r.Detail = fmt.Sprintf("服务端返回 %d", resp.StatusCode)
	default:
		r.OK = true
	}
	return r
}

// trimErr 把 Go 那串又长又重复的网络错误压成一句能看的话。
func trimErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i > 0 && len(s)-i < 60 {
		return s[i+2:]
	}
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

// -- 持久化 -------------------------------------------------------------------
//
// 和 MCP 总开关同一套存法。provider 是**每个 harness 一份**：
// 一台机器上可能 Claude 走官方、Codex 走中转，强行共用一份是错的。

const (
	proxyKey        = "ViPanelProxy"
	proxyEnabledKey = "ViPanelProxyEnabled"
)

var (
	outMu    sync.RWMutex
	curProxy Proxy
)

func CurrentProxy() Proxy {
	outMu.RLock()
	defer outMu.RUnlock()
	return curProxy
}

func SetProxy(p Proxy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	outMu.Lock()
	curProxy = Proxy{Enabled: p.Enabled, URL: strings.TrimSpace(p.URL)}
	outMu.Unlock()
	saveSetting(proxyKey, strings.TrimSpace(p.URL))
	saveSetting(proxyEnabledKey, strconv.FormatBool(p.Enabled))
	return nil
}

// LoadOutbound 启动时读回代理配置。
func LoadOutbound() {
	outMu.Lock()
	defer outMu.Unlock()
	on, _ := strconv.ParseBool(loadSetting(proxyEnabledKey))
	curProxy = Proxy{Enabled: on, URL: loadSetting(proxyKey)}
}

// OutboundEnv 是任何要访问外网的子进程都该带上的环境变量。
//
// **四个地方都要用它**：起会话、登录、查状态、装 harness。
// 漏掉任何一个，症状都是「配了代理但某个环节还是不通」，而且各不相同——
// 漏了登录就是登不进去，漏了查状态就是明明登录了却显示未登录。
func OutboundEnv(string) []string {
	return CurrentProxy().Env()
}

// -- 统一入口 -----------------------------------------------------------------
//
// 出站配置有四个必须覆盖的地方：起会话、登录、查状态、装 harness。
// 逐个调用点打补丁必然漏，所以收成下面两个函数，**所有对外的子进程
// 都必须从它们走**。漏了各有各的怪症状：
//   漏会话 → 能登录但发不出消息
//   漏登录 → 登不进去
//   漏查状态 → 明明登录了却一直显示未登录
//   漏安装 → npm 装不上

// withOutbound 给伪终端规格加上代理和中转配置。
func withOutbound(harnessID string, spec PtySpec) PtySpec {
	spec.Env = append(spec.Env, OutboundEnv(harnessID)...)
	return spec
}

// withProxyOnly 和 withOutbound 目前等价，保留独立名字是因为语义不同：
// 安装跑的是 npm，不是 agent 本身。
func withProxyOnly(spec PtySpec) PtySpec {
	spec.Env = append(spec.Env, CurrentProxy().Env()...)
	return spec
}

// outboundCmd 起一个带出站配置的命令。
//
// harness 里所有对外的 exec 都该用它——AuthStatus 是最容易漏的那个：
// 它继承的是 agent 进程的环境，而 agent 是 systemd 起的，没有代理变量。
func outboundCmd(harnessID, name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), OutboundEnv(harnessID)...)
	return cmd
}

// OutboundSettingData 是给界面看的出站配置。
type OutboundSettingData struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
}

func OutboundSetting() OutboundSettingData {
	p := CurrentProxy()
	return OutboundSettingData{Enabled: p.Enabled, URL: p.URL}
}
