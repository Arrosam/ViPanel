package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// 调用怎么落地：**把请求重放进 agent 自己的路由**，而不是直接调 service 层。
//
// 走同一条 handler 链就自动拿到了参数校验、i18n 错误文案，以及和 UI
// 完全一致的行为——「MCP 能做的 = UI 能做的」由构造保证，而不是靠纪律维持。
// 直接调 service 则要为 190 个 op 各写一个函数绑定，还会绕开校验。
//
// 走本机 unix socket 而不是进程内假请求，是为了不 import init/router
// （那会造成 api → service → router → api 的循环依赖）。代价是一次本机往返。
const sockPath = "/etc/1panel/agent.sock"

// 转发超时。要**小于** harness 那侧的 MCP 工具超时（我们在配置里写的是 600s），
// 又要大到不会在面板还在正常干活时就先喊「未响应」——那会让 agent 以为失败并重试，
// 而实际操作已经在跑，结果是装了两遍。真正的长任务走 taskID 异步，不占这个预算。
const dispatchTimeout = 480 * time.Second

var httpClient = &http.Client{
	Timeout: dispatchTimeout,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sockPath)
		},
	},
}

// Call 执行一个 op。args 是模型给的参数，已通过权限闸门。
func Call(op *Op, args map[string]any) (any, error) {
	if strings.HasPrefix(op.Path, "/ai/console") {
		// 第二层防御。第一层在生成器里，但目录是生成出来的，
		// 万一哪天前缀变了或有人手改了 catalog.gen.go，这里还能兜住。
		// 见 MCP.md §1.1。
		return nil, fmt.Errorf("拒绝：%s 属于会话自身的控制面", op.Path)
	}

	body := map[string]any{}
	for k, v := range args {
		body[k] = v
	}
	// 固定参数最后合并，**覆盖**模型传的同名值。
	// 顺序反了的话，给 container_restart 传 operation=remove 就成了删容器。
	if op.Fixed != "" {
		var fixed map[string]any
		if err := json.Unmarshal([]byte(op.Fixed), &fixed); err == nil {
			for k, v := range fixed {
				body[k] = v
			}
		}
	}

	path := op.Path
	for _, p := range op.PathParams {
		v, ok := body[p]
		if !ok {
			return nil, fmt.Errorf("缺少路径参数 %s", p)
		}
		path = strings.ReplaceAll(path, ":"+p, fmt.Sprint(v))
		path = strings.ReplaceAll(path, "{"+p+"}", fmt.Sprint(v))
		delete(body, p)
	}

	query := url.Values{}
	for _, q := range op.QueryParams {
		if v, ok := body[q]; ok {
			query.Set(q, fmt.Sprint(v))
			delete(body, q)
		}
	}

	target := "http://unix/api/v2" + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	var req *http.Request
	var err error
	if op.Method == "GET" {
		req, err = http.NewRequest("GET", target, nil)
	} else {
		raw, _ := json.Marshal(body)
		req, err = http.NewRequest(op.Method, target, bytes.NewReader(raw))
		if req != nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if err != nil {
		return nil, err
	}
	// 让面板按中文出错误文案。agent 的错误是 i18n key（ErrAppNameExist），
	// 不带这个头模型会拿到一串没人看得懂的标识符。
	req.Header.Set("Accept-Language", "zh")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("面板未响应：%w", err)
	}
	defer resp.Body.Close()

	var env struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("面板答复无法解析（HTTP %d）", resp.StatusCode)
	}
	if env.Code != http.StatusOK {
		if env.Message == "" {
			env.Message = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("%s", env.Message)
	}

	var data any
	if len(env.Data) > 0 {
		_ = json.Unmarshal(env.Data, &data)
	}
	return data, nil
}

// -- 大输出整形 --------------------------------------------------------------
//
// Claude Code 对 MCP 输出**超 10k token 警告、25k 截断**。截断发生时模型拿到的是
// 残缺但看起来完整的 JSON，它会照着残缺数据做决定——这比报错危险得多。
//
// 解法不是把字段砍死（砍狠了模型看不到它需要的信息），而是让它自己缩小范围：
// 翻页 + 服务端 grep + fields 逃生口。默认给一个紧凑字段集，
// 因为**第一次调用必须默认安全**——agent 还不知道响应有多大，没机会先 grep。

// Shaper 是每个列表类工具都会额外接受的四个参数。
type Shaper struct {
	Page     int
	PageSize int
	Grep     string
	Fields   []string // 传 ["*"] 拿全字段
}

const (
	defaultPageSize = 20
	maxPageSize     = 200
	// 单个字符串字段超过这个长度就截断。应用商店的 readMe 能有几十 KB，
	// 一页 20 条就足以把整个预算吃光。
	maxStringLen = 600
)

var shaperKeys = map[string]bool{
	"vpPage": true, "vpPageSize": true, "vpGrep": true, "vpFields": true,
}

// TakeShaper 从模型给的参数里摘出整形参数，剩下的才是真正要发给面板的。
// 用 vp 前缀是为了不和面板自己的 page/pageSize 撞——那两个是分页给面板用的，
// 而我们要在**拿到结果之后**再裁一次。
func TakeShaper(args map[string]any) Shaper {
	s := Shaper{Page: 1, PageSize: defaultPageSize}
	if v, ok := args["vpPage"]; ok {
		if n := toInt(v); n > 0 {
			s.Page = n
		}
	}
	if v, ok := args["vpPageSize"]; ok {
		if n := toInt(v); n > 0 {
			s.PageSize = min(n, maxPageSize)
		}
	}
	if v, ok := args["vpGrep"]; ok {
		s.Grep, _ = v.(string)
	}
	if v, ok := args["vpFields"]; ok {
		switch t := v.(type) {
		case string:
			for _, f := range strings.Split(t, ",") {
				if f = strings.TrimSpace(f); f != "" {
					s.Fields = append(s.Fields, f)
				}
			}
		case []any:
			for _, f := range t {
				if str, ok := f.(string); ok {
					s.Fields = append(s.Fields, str)
				}
			}
		}
	}
	for k := range shaperKeys {
		delete(args, k)
	}
	return s
}

// Shape 裁剪一份响应。返回裁剪后的数据和一句给模型看的提示。
func Shape(data any, sh Shaper, defaults []string) (any, string) {
	items, total, isList := listOf(data)
	if !isList {
		return truncateDeep(data, sh.Fields), ""
	}

	if sh.Grep != "" {
		re, err := regexp.Compile("(?i)" + sh.Grep)
		if err != nil {
			return nil, "grep 不是合法正则：" + err.Error()
		}
		var kept []any
		for _, it := range items {
			// **在完整行上匹配**，不受 fields 影响——
			// 否则「按一个没显示的字段过滤」就做不到了
			raw, _ := json.Marshal(it)
			if re.Match(raw) {
				kept = append(kept, it)
			}
		}
		items = kept
	}

	matched := len(items)
	start := (sh.Page - 1) * sh.PageSize
	if start > matched {
		start = matched
	}
	end := min(start+sh.PageSize, matched)
	page := items[start:end]

	fields := sh.Fields
	if len(fields) == 0 {
		fields = defaults
	}
	out := make([]any, 0, len(page))
	for _, it := range page {
		out = append(out, truncateDeep(project(it, fields), nil))
	}

	note := fmt.Sprintf("第 %d 页，本页 %d 条", sh.Page, len(out))
	if sh.Grep != "" {
		note += fmt.Sprintf("，grep 命中 %d 条", matched)
	}
	if total > 0 && total != matched {
		note += fmt.Sprintf("，服务端共 %d 条", total)
	}
	if end < matched {
		note += fmt.Sprintf("，还有 %d 条，用 vpPage=%d 继续", matched-end, sh.Page+1)
	}
	if len(fields) > 0 && !hasStar(fields) {
		note += "。只显示了部分字段，要全部字段传 vpFields=\"*\""
	}
	return out, note
}

func hasStar(f []string) bool {
	for _, s := range f {
		if s == "*" {
			return true
		}
	}
	return false
}

// listOf 把面板的响应拆成条目数组。1Panel 的分页返回是 {items, total}，
// 也有直接返回数组的。
func listOf(data any) ([]any, int, bool) {
	switch t := data.(type) {
	case []any:
		return t, 0, true
	case map[string]any:
		for _, key := range []string{"items", "list", "data"} {
			if arr, ok := t[key].([]any); ok {
				return arr, toInt(t["total"]), true
			}
		}
	}
	return nil, 0, false
}

func project(item any, fields []string) any {
	obj, ok := item.(map[string]any)
	if !ok || len(fields) == 0 || hasStar(fields) {
		return item
	}
	out := map[string]any{}
	for _, f := range fields {
		if v, ok := obj[f]; ok {
			out[f] = v
		}
	}
	// 一个字段都没对上说明 fields 写错了（上游改了字段名？）。
	// 这时返回原对象比返回一个空壳强——空壳会让模型以为这条记录是空的。
	if len(out) == 0 {
		return item
	}
	return out
}

// truncateDeep 砍掉过长的字符串字段。
// 应用商店的 readMe / description / icon 是最典型的：单条就能吃掉整个预算。
func truncateDeep(v any, fields []string) any {
	switch t := v.(type) {
	case string:
		if len(t) > maxStringLen {
			return t[:maxStringLen] + fmt.Sprintf("…（已截断，原长 %d 字符）", len(t))
		}
		return t
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = truncateDeep(e, nil)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if len(fields) > 0 && !hasStar(fields) {
				found := false
				for _, f := range fields {
					if f == k {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}
			out[k] = truncateDeep(t[k], nil)
		}
		return out
	}
	return v
}

func toInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	}
	return 0
}
