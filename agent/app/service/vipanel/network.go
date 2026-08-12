package vipanel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
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
// 这里的分工：
//   - 代理是**通用的**——HTTPS_PROXY 那套环境变量两个 CLI 都认，
//     所以注入逻辑不进 harness 接口。
//   - 中转端点是**因 harness 而异的**——Claude 用 ANTHROPIC_BASE_URL 环境变量，
//     Codex 根本不认 OPENAI_BASE_URL（在它的二进制里 0 命中），必须走
//     model_providers 配置。所以由各自声明怎么翻译。
//   - 依赖哪些端点也是因 harness 而异的，同样由各自声明。

// Proxy 是面板级的出站代理设置。
type Proxy struct {
	// URL 形如 http://127.0.0.1:7890 或 socks5://127.0.0.1:1080。
	// 空串表示不走代理。
	URL string `json:"url"`
}

// Env 把代理翻成子进程的环境变量。
//
// 大小写两份都给：Go 和 Rust 的 http 客户端惯例不同，有的只看小写。
// 多给几个变量的成本是零，漏一个的代价是「配了但没生效」。
//
// NO_PROXY 里必须有回环：面板自己的 unix socket 走不到代理，但 agent
// 里的工具可能去访问 127.0.0.1 上的服务，把它们也塞进代理是错的。
func (p Proxy) Env() []string {
	u := strings.TrimSpace(p.URL)
	if u == "" {
		return nil
	}
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
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("不支持的代理协议 %q，只支持 http / https / socks5 / socks5h", parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("代理地址缺少主机名")
	}
	return nil
}

// Provider 是一个中转端点。
//
// 用它意味着**放弃官方订阅**（Pro/Max、ChatGPT 套餐都不走这条），
// 改成按 API key 计费，并且请求会经过第三方。这个取舍要让用户在界面上看见。
type Provider struct {
	// BaseURL 是中转的地址。空串表示用官方端点。
	BaseURL string `json:"baseUrl"`
	// APIKey 是中转发的密钥。
	APIKey string `json:"apiKey"`
}

func (p Provider) Enabled() bool {
	return strings.TrimSpace(p.BaseURL) != "" && strings.TrimSpace(p.APIKey) != ""
}

// ProviderTarget 由「能被指到中转端点」的 harness 实现。
//
// 可选接口：shell 没有这个概念。而且**两家的机制根本不同**，
// 不是同一件事的两种写法——所以这里只约定「翻译」这个动作，
// 具体翻成环境变量还是配置项由各自决定。
type ProviderTarget interface {
	// ProviderEnv 返回要追加的环境变量。
	ProviderEnv(p Provider) []string
	// ProviderArgs 返回要追加的命令行参数（Codex 走 -c 内联 TOML）。
	ProviderArgs(p Provider) []string
}

// providerInjection 把一个 provider 翻译成某个 harness 认的形式。
func providerInjection(h Harness, p Provider) (env []string, args []string) {
	if !p.Enabled() {
		return nil, nil
	}
	t, ok := h.(ProviderTarget)
	if !ok {
		return nil, nil
	}
	return t.ProviderEnv(p), t.ProviderArgs(p)
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
	client := &http.Client{Timeout: 12 * time.Second}
	if u := strings.TrimSpace(p.URL); u != "" {
		if parsed, err := url.Parse(u); err == nil {
			client.Transport = &http.Transport{Proxy: http.ProxyURL(parsed)}
		}
	}

	var out []ReachResult
	for _, e := range deps.Endpoints() {
		out = append(out, probe(client, e))
	}
	return out
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

	// 401/404 都算通：我们没带凭据，能收到这类应答说明请求到达了服务端。
	// **要区分出来的是 403**——地区封锁就长这样，而它和「网络不通」
	// 的解决办法完全不同。
	switch {
	case resp.StatusCode == http.StatusForbidden:
		r.Detail = "返回 403，通常是按出口地区拒绝。需要配置出站代理，或改用中转端点。"
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
	proxyKey       = "ViPanelProxy"
	providerKeyPfx = "ViPanelProvider:"
)

var (
	outMu     sync.RWMutex
	curProxy  Proxy
	providers = map[string]Provider{}
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
	curProxy = p
	outMu.Unlock()
	saveSetting(proxyKey, strings.TrimSpace(p.URL))
	return nil
}

func CurrentProvider(harnessID string) Provider {
	outMu.RLock()
	defer outMu.RUnlock()
	return providers[harnessID]
}

func SetProvider(harnessID string, p Provider) error {
	if _, ok := Get(harnessID).(ProviderTarget); !ok {
		return fmt.Errorf("%s 不支持中转端点", Get(harnessID).DisplayName())
	}
	// 只填一半是配错了，而症状是「看起来配好了但请求全失败」。
	if (strings.TrimSpace(p.BaseURL) == "") != (strings.TrimSpace(p.APIKey) == "") {
		return fmt.Errorf("中转地址和密钥要么都填，要么都留空")
	}
	if u := strings.TrimSpace(p.BaseURL); u != "" {
		parsed, err := url.Parse(u)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("中转地址必须是完整的 http(s) 地址")
		}
	}
	outMu.Lock()
	providers[harnessID] = p
	outMu.Unlock()
	raw, _ := json.Marshal(p)
	saveSetting(providerKeyPfx+harnessID, string(raw))
	return nil
}

// LoadOutbound 启动时读回代理与各 harness 的中转配置。
func LoadOutbound() {
	outMu.Lock()
	defer outMu.Unlock()
	curProxy = Proxy{URL: loadSetting(proxyKey)}
	for _, h := range List() {
		raw := loadSetting(providerKeyPfx + h.ID())
		if raw == "" {
			continue
		}
		var p Provider
		if json.Unmarshal([]byte(raw), &p) == nil {
			providers[h.ID()] = p
		}
	}
}

// OutboundEnv 是任何要访问外网的子进程都该带上的环境变量。
//
// **四个地方都要用它**：起会话、登录、查状态、装 harness。
// 漏掉任何一个，症状都是「配了代理但某个环节还是不通」，而且各不相同——
// 漏了登录就是登不进去，漏了查状态就是明明登录了却显示未登录。
func OutboundEnv(harnessID string) []string {
	env := CurrentProxy().Env()
	if pe, _ := providerInjection(Get(harnessID), CurrentProvider(harnessID)); len(pe) > 0 {
		env = append(env, pe...)
	}
	return env
}

// OutboundArgs 是要追加到启动命令里的参数（目前只有 Codex 的 -c 中转配置）。
func OutboundArgs(harnessID string) []string {
	_, args := providerInjection(Get(harnessID), CurrentProvider(harnessID))
	return args
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
	spec.Args = append(spec.Args, OutboundArgs(harnessID)...)
	return spec
}

// withProxyOnly 只加代理，不加中转配置。
//
// 安装用这个：中转的那些参数是给 agent 自己的（比如 codex 的 -c
// model_providers），塞给 npm 只会让它报一句看不懂的错。
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

// OutboundSetting 是给界面看的出站配置全貌。
//
// **密钥只报「配没配」，不回传原文。** 面板里能读到的东西等于面板被攻破时
// 会泄漏的东西；界面需要知道的只是「这里配过了」。
type OutboundSettingData struct {
	Proxy     string                    `json:"proxy"`
	Providers map[string]ProviderStatus `json:"providers"`
}

type ProviderStatus struct {
	Supported bool   `json:"supported"`
	BaseURL   string `json:"baseUrl"`
	HasKey    bool   `json:"hasKey"`
}

func OutboundSetting() OutboundSettingData {
	out := OutboundSettingData{Proxy: CurrentProxy().URL, Providers: map[string]ProviderStatus{}}
	for _, h := range List() {
		_, ok := h.(ProviderTarget)
		p := CurrentProvider(h.ID())
		out.Providers[h.ID()] = ProviderStatus{
			Supported: ok,
			BaseURL:   p.BaseURL,
			HasKey:    strings.TrimSpace(p.APIKey) != "",
		}
	}
	return out
}
