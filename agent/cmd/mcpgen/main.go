// mcpgen 把 1Panel 自己的接口清单编译成 MCP 工具目录。
//
// 输入三份：
//   - core/cmd/server/docs/swagger.json   路径 / 方法 / 请求体 JSON Schema / x-panel-log 文案
//   - agent/cmd/server/docs/x-log.json    补 swagger 里缺的中英文操作文案
//   - .../vipanel/mcp/manifest.yaml       **唯一的人工评审面**：收哪些、归哪个板块、什么风险档
//
// 输出一份 catalog.gen.go，提交进仓库，构建时不需要再跑生成器。
//
// -check 模式给 CI 用。它的判据是**fail-closed**：一个上游新加的、
// 没人在 manifest 里看过的端点，绝不能自动获得「AI 可以以 root 调用」的地位。
// 见 MCP.md §5。
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// -- swagger ----------------------------------------------------------------

type swaggerDoc struct {
	Paths       map[string]map[string]swaggerOp `json:"paths"`
	Definitions map[string]json.RawMessage      `json:"definitions"`
}

type swaggerOp struct {
	Summary    string           `json:"summary"`
	Tags       []string         `json:"tags"`
	Parameters []swaggerParam   `json:"parameters"`
	PanelLog   *swaggerPanelLog `json:"x-panel-log"`
}

type swaggerParam struct {
	In     string          `json:"in"`
	Name   string          `json:"name"`
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
}

// swaggerPanelLog 是 1Panel 用来生成操作日志的元数据。
// 对我们来说它是**权限卡片的现成文案**，以及 id→名字的解析配方。
type swaggerPanelLog struct {
	BodyKeys        []string         `json:"bodyKeys"`
	BeforeFunctions []beforeFunction `json:"BeforeFunctions"`
	FormatZH        string           `json:"formatZH"`
	FormatEN        string           `json:"formatEN"`
}

type beforeFunction struct {
	DB           string `json:"db"`
	InputColumn  string `json:"input_column"`
	InputValue   string `json:"input_value"`
	IsList       bool   `json:"isList"`
	OutputColumn string `json:"output_column"`
	OutputValue  string `json:"output_value"`
}

// x-log.json 的条目，字段名和 swagger 里的 x-panel-log 略有出入（BeforeFunctions 小写开头）
type xlogEntry struct {
	BodyKeys        []string         `json:"bodyKeys"`
	BeforeFunctions []beforeFunction `json:"beforeFunctions"`
	FormatZH        string           `json:"formatZH"`
	FormatEN        string           `json:"formatEN"`
}

// -- manifest ---------------------------------------------------------------

type manifest struct {
	Modules  []manifestModule `yaml:"modules"`
	Excluded []exclusion      `yaml:"excluded"`
	Ops      []manifestOp     `yaml:"ops"`
}

type manifestModule struct {
	Key    string `yaml:"key"`
	Title  string `yaml:"title"`
	Desc   string `yaml:"desc"`   // 进 server instructions 的关键词索引
	Status string `yaml:"status"` // done | pending
}

type exclusion struct {
	Prefix string   `yaml:"prefix"`
	Paths  []string `yaml:"paths"`
	Reason string   `yaml:"reason"`
}

type manifestOp struct {
	Op       string   `yaml:"op"`   // app.install → 工具名 app_install
	Path     string   `yaml:"path"` // method 从 swagger 取，不在这里重复
	Module   string   `yaml:"module"`
	Risk     string   `yaml:"risk"`     // read | write | destructive
	Resident bool     `yaml:"resident"` // 常驻 Tier 0，不需要先开板块
	Desc     descI18n `yaml:"desc"`
	Fields   []string `yaml:"fields"`   // 列表类响应的默认展示字段
	NoAlways bool     `yaml:"noAlways"` // 禁用「总是允许」（比如会花钱的操作）

	// Fixed 把一个多路复用端点拆成若干固定参数的工具。
	//
	// /containers/operate 的 operation 是 oneof=up start stop restart kill pause
	// unpause remove——**remove 会删容器**。一个静态的 risk 字段表达不了
	// 「档位取决于入参」，所以改成一个动作一个工具：container.remove 固定
	// operation=remove 并标 destructive，container.restart 固定 restart 标 write。
	// 附带的好处是模型不用再猜枚举值。
	Fixed map[string]any `yaml:"fixed"`
}

type descI18n struct {
	ZH string `yaml:"zh"`
	EN string `yaml:"en"`
}

// -- 产物 -------------------------------------------------------------------

type catalogOp struct {
	Name        string
	Module      string
	Path        string
	Method      string
	Risk        string
	Resident    bool
	DescZH      string
	DescEN      string
	InputSchema string
	Fields      []string
	PathParams  []string
	QueryParams []string
	NoAlways    bool
	Fixed       string // JSON 对象，运行时合并进请求体
	FormatZH    string
	FormatEN    string
	Resolvers   []beforeFunction
}

const consolePrefix = "/ai/console"

// truncated 记录有多少个 schema 在递归点被截断，生成完报一次，别让它悄悄发生。
var truncated int

func main() {
	var (
		root  = flag.String("root", ".", "仓库根目录")
		out   = flag.String("out", "", "输出文件，默认 agent/app/service/vipanel/mcp/catalog.gen.go")
		check = flag.Bool("check", false, "只检查不写文件，有未分类的 op 就退出 1")
		sugg  = flag.String("suggest", "", "为指定板块打印待补的 manifest 片段（板块 key，或 all）")
	)
	flag.Parse()

	swPath := filepath.Join(*root, "core/cmd/server/docs/swagger.json")
	xlPath := filepath.Join(*root, "agent/cmd/server/docs/x-log.json")
	mfPath := filepath.Join(*root, "agent/app/service/vipanel/mcp/manifest.yaml")
	if *out == "" {
		*out = filepath.Join(*root, "agent/app/service/vipanel/mcp/catalog.gen.go")
	}

	sw, err := loadSwagger(swPath)
	die(err)
	xl, err := loadXLog(xlPath)
	die(err)
	mf, err := loadManifest(mfPath)
	die(err)

	ops, problems := build(sw, xl, mf)

	if *sugg != "" {
		printSuggestions(sw, mf, *sugg)
		return
	}

	if len(problems.errors) > 0 {
		for _, e := range problems.errors {
			fmt.Fprintln(os.Stderr, "错误: "+e)
		}
	}
	if len(problems.pending) > 0 {
		fmt.Fprintf(os.Stderr, "待分类: %d 个 op 尚未进 manifest（板块 status: pending，暂不算失败）\n",
			len(problems.pending))
	}
	if len(problems.errors) > 0 {
		os.Exit(1)
	}

	if *check {
		fmt.Printf("检查通过：%d 个 op 已收录，%d 个显式排除，%d 个待分类\n",
			len(ops), problems.excluded, len(problems.pending))
		return
	}

	src, err := render(mf, ops)
	die(err)
	die(os.WriteFile(*out, src, 0o644))
	fmt.Printf("已生成 %s：%d 个 op\n", *out, len(ops))
	if truncated > 0 {
		fmt.Printf("其中 %d 处递归 schema 被截断成不透明对象\n", truncated)
	}
}

// -- 加载 -------------------------------------------------------------------

func loadSwagger(p string) (*swaggerDoc, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var d swaggerDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("解析 swagger: %w", err)
	}
	return &d, nil
}

func loadXLog(p string) (map[string]xlogEntry, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	m := map[string]xlogEntry{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("解析 x-log: %w", err)
	}
	return m, nil
}

func loadManifest(p string) (*manifest, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("解析 manifest: %w", err)
	}
	return &m, nil
}

// -- 构建 -------------------------------------------------------------------

type report struct {
	errors   []string
	pending  []string
	excluded int
}

func build(sw *swaggerDoc, xl map[string]xlogEntry, mf *manifest) ([]catalogOp, report) {
	var rep report

	modStatus := map[string]string{}
	for _, m := range mf.Modules {
		modStatus[m.Key] = m.Status
	}

	claimed := map[string]bool{} // path 被 manifest 认领
	var ops []catalogOp

	for _, mo := range mf.Ops {
		methods, ok := sw.Paths[mo.Path]
		if !ok {
			rep.errors = append(rep.errors,
				fmt.Sprintf("%s: manifest 里的 path %s 在 swagger 里不存在（上游改了？）", mo.Op, mo.Path))
			continue
		}
		if _, ok := modStatus[mo.Module]; !ok {
			rep.errors = append(rep.errors,
				fmt.Sprintf("%s: 板块 %s 未在 manifest.modules 里声明", mo.Op, mo.Module))
			continue
		}
		if mo.Risk != "read" && mo.Risk != "write" && mo.Risk != "destructive" {
			rep.errors = append(rep.errors,
				fmt.Sprintf("%s: risk 必须是 read/write/destructive，得到 %q", mo.Op, mo.Risk))
			continue
		}
		if strings.HasPrefix(mo.Path, consolePrefix) {
			// 第一层防御。第二层在运行时按实际要发的 path 再查一次，见 MCP.md §1.1
			rep.errors = append(rep.errors,
				fmt.Sprintf("%s: %s 属于会话自身的控制面，收录它等于让 agent 批准自己的请求", mo.Op, mo.Path))
			continue
		}
		claimed[mo.Path] = true

		method, sop := pickMethod(methods)
		op := catalogOp{
			Name:     strings.ReplaceAll(mo.Op, ".", "_"),
			Module:   mo.Module,
			Path:     mo.Path,
			Method:   strings.ToUpper(method),
			Risk:     mo.Risk,
			Resident: mo.Resident,
			DescZH:   mo.Desc.ZH,
			DescEN:   mo.Desc.EN,
			Fields:   mo.Fields,
			NoAlways: mo.NoAlways,
		}
		if op.DescEN == "" {
			op.DescEN = sop.Summary
		}
		// 权限卡片文案：swagger 内嵌优先，退回 x-log.json
		if sop.PanelLog != nil {
			op.FormatZH, op.FormatEN = sop.PanelLog.FormatZH, sop.PanelLog.FormatEN
			op.Resolvers = sop.PanelLog.BeforeFunctions
		} else if e, ok := xl[mo.Path]; ok {
			op.FormatZH, op.FormatEN = e.FormatZH, e.FormatEN
			op.Resolvers = e.BeforeFunctions
		}
		// 上游没给操作日志文案的（约 1/3 的变更类端点），退回 manifest 里手写的 desc.zh。
		// 卡片上宁可显示一句略笼统的话，也不能显示原始 JSON——人看不懂就会一路点允许。
		if op.FormatZH == "" {
			op.FormatZH, op.FormatEN = op.DescZH, op.DescEN
		}
		if op.FormatZH == "" && mo.Risk != "read" {
			rep.errors = append(rep.errors,
				fmt.Sprintf("%s: 变更类操作没有任何可读文案，请在 manifest 里补 desc.zh", mo.Op))
			continue
		}

		if len(mo.Fixed) > 0 {
			raw, err := json.Marshal(mo.Fixed)
			if err != nil {
				rep.errors = append(rep.errors, fmt.Sprintf("%s: fixed 无法序列化: %v", mo.Op, err))
				continue
			}
			op.Fixed = string(raw)
		}

		schema, pathP, queryP, err := buildSchema(sw, mo.Path, sop, mo.Fixed)
		if err != nil {
			rep.errors = append(rep.errors, fmt.Sprintf("%s: %v", mo.Op, err))
			continue
		}
		op.InputSchema, op.PathParams, op.QueryParams = schema, pathP, queryP
		ops = append(ops, op)
	}

	// 覆盖率：swagger 里每个 op 都必须被 manifest 收录、或被显式排除，
	// 否则就是「没人看过」——除非它所属板块还标着 pending
	for p := range sw.Paths {
		if claimed[p] {
			continue
		}
		if reason := matchExclusion(mf.Excluded, p); reason != "" {
			rep.excluded++
			continue
		}
		if strings.HasPrefix(p, consolePrefix) {
			rep.excluded++ // 无条件丢弃，不需要在 manifest 里写一遍
			continue
		}
		if doneModule(mf, p) {
			rep.errors = append(rep.errors,
				fmt.Sprintf("%s 未分类：所属板块已标 done，新端点必须先在 manifest 里过一遍", p))
		} else {
			rep.pending = append(rep.pending, p)
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Name < ops[j].Name })
	sort.Strings(rep.pending)
	sort.Strings(rep.errors)
	return ops, rep
}

// pickMethod 挑一个方法。同一 path 有多个方法时优先 post（1Panel 绝大多数写操作是 post）。
func pickMethod(methods map[string]swaggerOp) (string, swaggerOp) {
	for _, m := range []string{"post", "put", "get", "delete"} {
		if op, ok := methods[m]; ok {
			return m, op
		}
	}
	for m, op := range methods {
		return m, op
	}
	return "post", swaggerOp{}
}

func matchExclusion(list []exclusion, path string) string {
	for _, e := range list {
		if e.Prefix != "" && strings.HasPrefix(path, e.Prefix) {
			return e.Reason
		}
		for _, p := range e.Paths {
			if p == path {
				return e.Reason
			}
		}
	}
	return ""
}

// doneModule 判断一个未分类的 path 是否落在某个已标 done 的板块里。
// 用 manifest 里同板块已收录 op 的公共路径前缀来判定——没有别的可靠信号，
// swagger 的 tag 和我们的板块划分不是一一对应的。
func doneModule(mf *manifest, path string) bool {
	done := map[string]bool{}
	for _, m := range mf.Modules {
		if m.Status == "done" {
			done[m.Key] = true
		}
	}
	for _, o := range mf.Ops {
		if !done[o.Module] {
			continue
		}
		if seg := firstSegment(o.Path); seg != "" && firstSegment(path) == seg {
			return true
		}
	}
	return false
}

func firstSegment(p string) string {
	p = strings.TrimPrefix(p, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return p
}

// -- schema 展平 -------------------------------------------------------------

// 1Panel 的 swagger 路径用的是 gin 的 :name 风格，不是 OpenAPI 的 {name}。
// 运行时替换也按这个风格来。两种都认，免得上游哪天改了。
var pathParamRe = regexp.MustCompile(`[:{]([A-Za-z_][A-Za-z0-9_]*)\}?`)

// buildSchema 把 swagger 的 body $ref 展平成自包含的 JSON Schema，
// 并把 path / query 参数并进同一个对象——MCP 的 inputSchema 只有一个平面。
//
// 已验证 request.* 最深 2 层、零循环引用（MCP.md §2），所以这里不需要断环，
// 但仍留了深度上限当保险丝。
func buildSchema(sw *swaggerDoc, path string, op swaggerOp, fixed map[string]any) (string, []string, []string, error) {
	props := map[string]any{}
	var required []string
	var pathParams, queryParams []string

	for _, p := range op.Parameters {
		switch p.In {
		case "body":
			if len(p.Schema) == 0 {
				continue
			}
			node, err := resolve(sw, p.Schema, nil)
			if err != nil {
				return "", nil, nil, err
			}
			obj, _ := node.(map[string]any)
			if sub, ok := obj["properties"].(map[string]any); ok {
				for k, v := range sub {
					props[k] = v
				}
			}
			if req, ok := obj["required"].([]any); ok {
				for _, r := range req {
					if s, ok := r.(string); ok {
						required = append(required, s)
					}
				}
			}
		case "path":
			props[p.Name] = map[string]any{"type": scalarType(p.Type)}
			required = append(required, p.Name)
			pathParams = append(pathParams, p.Name)
		case "query":
			props[p.Name] = map[string]any{"type": scalarType(p.Type)}
			queryParams = append(queryParams, p.Name)
		}
	}
	// swagger 偶尔漏声明 path 参数，从路径模板兜一遍
	for _, m := range pathParamRe.FindAllStringSubmatch(path, -1) {
		if _, ok := props[m[1]]; !ok {
			props[m[1]] = map[string]any{"type": "string"}
			required = append(required, m[1])
			pathParams = append(pathParams, m[1])
		}
	}

	// 固定参数由运行时注入，不该出现在给模型看的 schema 里——
	// 露出来模型就会去填，填错了还得报错回去
	for k := range fixed {
		delete(props, k)
	}
	var kept []string
	for _, r := range required {
		if _, isFixed := fixed[r]; !isFixed {
			kept = append(kept, r)
		}
	}
	required = kept

	sort.Strings(required)
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	raw, err := json.Marshal(schema)
	return string(raw), pathParams, queryParams, err
}

func scalarType(t string) string {
	switch t {
	case "integer", "number", "boolean", "array":
		return t
	default:
		return "string"
	}
}

// resolve 把 $ref 就地展开成自包含的 schema。
//
// 深度只在**跟 $ref 的时候**算，普通的 properties/items 嵌套不算——
// 早先两种都计数，结果 dto.CronjobImport 这种嵌套深但完全没有环的 schema
// 被误判成循环引用。判环要看的是「这条引用链上有没有重复的定义名」。
func resolve(sw *swaggerDoc, raw json.RawMessage, chain []string) (any, error) {
	var node map[string]any
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil, err
	}
	if ref, ok := node["$ref"].(string); ok {
		name := ref[strings.LastIndexByte(ref, '/')+1:]
		for _, prev := range chain {
			if prev == name {
				// 成环了（dto.DataTree 这种自递归的树结构）。MCP 的 inputSchema
				// 必须自包含，展不开又不能无限展开，所以在递归点截断成一个不透明对象
				// 并**明说**它被截断了——不标注的话模型会以为这里只能填空对象。
				truncated++
				return map[string]any{
					"type":        "object",
					"description": "嵌套结构（" + name + "），递归定义已截断，按面板文档填写",
				}, nil
			}
		}
		def, ok := sw.Definitions[name]
		if !ok {
			return nil, fmt.Errorf("definitions 里没有 %s", name)
		}
		return resolve(sw, def, append(chain, name))
	}
	for k, v := range node {
		switch k {
		case "properties":
			sub, ok := v.(map[string]any)
			if !ok {
				continue
			}
			for pk, pv := range sub {
				b, _ := json.Marshal(pv)
				r, err := resolve(sw, b, chain)
				if err != nil {
					return nil, err
				}
				sub[pk] = r
			}
		case "items":
			b, _ := json.Marshal(v)
			r, err := resolve(sw, b, chain)
			if err != nil {
				return nil, err
			}
			node[k] = r
		}
	}
	return node, nil
}

// -- 输出 -------------------------------------------------------------------

func render(mf *manifest, ops []catalogOp) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("// Code generated by mcpgen. DO NOT EDIT.\n")
	b.WriteString("// 改工具目录请改 manifest.yaml，然后 go run ./agent/cmd/mcpgen\n\n")
	b.WriteString("package mcp\n\n")

	b.WriteString("var Modules = []Module{\n")
	for _, m := range mf.Modules {
		fmt.Fprintf(&b, "\t{Key: %q, Title: %q, Desc: %q},\n", m.Key, m.Title, m.Desc)
	}
	b.WriteString("}\n\n")

	b.WriteString("var Catalog = []Op{\n")
	for _, o := range ops {
		fmt.Fprintf(&b, "\t{\n")
		fmt.Fprintf(&b, "\t\tName: %q, Module: %q, Path: %q, Method: %q,\n", o.Name, o.Module, o.Path, o.Method)
		fmt.Fprintf(&b, "\t\tRisk: %q, Resident: %v, NoAlways: %v,\n", o.Risk, o.Resident, o.NoAlways)
		fmt.Fprintf(&b, "\t\tDescZH: %q,\n\t\tDescEN: %q,\n", o.DescZH, o.DescEN)
		fmt.Fprintf(&b, "\t\tFormatZH: %q,\n\t\tFormatEN: %q,\n", o.FormatZH, o.FormatEN)
		fmt.Fprintf(&b, "\t\tInputSchema: %q,\n", o.InputSchema)
		if o.Fixed != "" {
			fmt.Fprintf(&b, "\t\tFixed: %q,\n", o.Fixed)
		}
		fmt.Fprintf(&b, "\t\tFields: %s,\n", goStrings(o.Fields))
		fmt.Fprintf(&b, "\t\tPathParams: %s, QueryParams: %s,\n", goStrings(o.PathParams), goStrings(o.QueryParams))
		if len(o.Resolvers) > 0 {
			b.WriteString("\t\tResolvers: []Resolver{\n")
			for _, r := range o.Resolvers {
				fmt.Fprintf(&b, "\t\t\t{DB: %q, InputColumn: %q, InputValue: %q, IsList: %v, OutputColumn: %q, OutputValue: %q},\n",
					r.DB, r.InputColumn, r.InputValue, r.IsList, r.OutputColumn, r.OutputValue)
			}
			b.WriteString("\t\t},\n")
		}
		fmt.Fprintf(&b, "\t},\n")
	}
	b.WriteString("}\n")

	return format.Source(b.Bytes())
}

func goStrings(v []string) string {
	if len(v) == 0 {
		return "nil"
	}
	parts := make([]string, len(v))
	for i, s := range v {
		parts[i] = fmt.Sprintf("%q", s)
	}
	return "[]string{" + strings.Join(parts, ", ") + "}"
}

// -- 辅助：把待补的 op 打成 manifest 片段，人工评审时照着改 --------------------

func printSuggestions(sw *swaggerDoc, mf *manifest, module string) {
	claimed := map[string]bool{}
	for _, o := range mf.Ops {
		claimed[o.Path] = true
	}
	var paths []string
	for p := range sw.Paths {
		if claimed[p] || strings.HasPrefix(p, consolePrefix) {
			continue
		}
		if matchExclusion(mf.Excluded, p) != "" {
			continue
		}
		if module != "all" && firstSegment(p) != module {
			continue
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		_, sop := pickMethod(sw.Paths[p])
		zh := ""
		if sop.PanelLog != nil {
			zh = sop.PanelLog.FormatZH
		}
		fmt.Printf("  - op: %s\n    path: %s\n    module: %s\n    risk: %s\n    desc:\n      zh: %q\n      en: %q\n",
			suggestName(p), p, module, suggestRisk(p, sw.Paths[p]), zh, sop.Summary)
	}
	fmt.Fprintf(os.Stderr, "\n%d 条待人工确认\n", len(paths))
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

func suggestName(p string) string {
	s := nonWord.ReplaceAllString(strings.ToLower(strings.TrimPrefix(p, "/")), "_")
	return strings.Trim(s, "_")
}

var (
	readRe = regexp.MustCompile(`/(search|list|load|get|detail|check|options|preview|status|count|tree|size|info)$`)
	destRe = regexp.MustCompile(`(del|delete|uninstall|clean|reset|restore|force|remove|destroy)`)
)

// suggestRisk 只是**提议**。manifest 里的值才作数，见 MCP.md §5。
func suggestRisk(p string, methods map[string]swaggerOp) string {
	if _, only := methods["get"]; only && len(methods) == 1 {
		return "read"
	}
	if readRe.MatchString(p) {
		return "read"
	}
	if destRe.MatchString(p) {
		return "destructive"
	}
	return "write"
}

func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
