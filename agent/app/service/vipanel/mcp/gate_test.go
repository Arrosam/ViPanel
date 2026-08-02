package mcp

import (
	"strings"
	"testing"
)

// 凭据必须**一次性**消费。
// 连着两次一模一样的 container_list，一次批准不能盖住两次调用——
// 否则「允许一次」实际上变成了「允许到过期为止」。
func TestTicketIsSingleUse(t *testing.T) {
	g := Gate()
	g.Forget("t1")
	g.Record("t1", "website_list", "abc", "tu-1")

	if !g.Consume("t1", "website_list", "abc", "tu-1") {
		t.Fatal("第一次应该消费成功")
	}
	if g.Consume("t1", "website_list", "abc", "tu-1") {
		t.Fatal("第二次不该还能消费——一次批准只能盖一次调用")
	}
}

// 对账主键是「工具名 + 入参指纹」，两侧本来就都有，任何 harness 都能对。
// toolUseID 只是更精确的键，缺了它不能因此对不上。
func TestConsumeWorksWithoutToolUseID(t *testing.T) {
	g := Gate()
	g.Forget("t2")
	g.Record("t2", "app_install", "hash1", "") // 非 Claude harness 没有 toolUseID

	if !g.Consume("t2", "app_install", "hash1", "") {
		t.Fatal("没有 toolUseID 时也应该能对上")
	}
}

func TestConsumeRejectsDifferentInput(t *testing.T) {
	g := Gate()
	g.Forget("t3")
	g.Record("t3", "website_delete", "hash-of-site-A", "tu")

	// 批准的是删 A，来的是删 B——不能放行
	if g.Consume("t3", "website_delete", "hash-of-site-B", "tu") {
		t.Fatal("入参不同的调用不该复用别人的凭据")
	}
}

func TestConsumeIsSessionScoped(t *testing.T) {
	g := Gate()
	g.Forget("a")
	g.Forget("b")
	g.Record("a", "website_delete", "h", "tu")
	if g.Consume("b", "website_delete", "h", "tu") {
		t.Fatal("一个会话的批准不能被另一个会话用掉")
	}
}

// 板块授权只在本会话内有效。同一个 id 被删掉重建后必须重新申请——
// 不然就等于跨会话记忆了，而「上次我允许它碰数据库」不等于「这次也该允许」。
func TestModuleAuthIsForgotten(t *testing.T) {
	g := Gate()
	g.Forget("m1")
	if g.ModuleAuthorized("m1", "database") {
		t.Fatal("默认不该有授权")
	}
	g.AuthorizeModule("m1", "database")
	if !g.ModuleAuthorized("m1", "database") {
		t.Fatal("授权后应该记住")
	}
	if g.ModuleAuthorized("m1", "website") {
		t.Fatal("授权一个板块不该顺带授权另一个")
	}
	g.Forget("m1")
	if g.ModuleAuthorized("m1", "database") {
		t.Fatal("会话消失后授权必须一起消失")
	}
}

func TestInputHashIsStable(t *testing.T) {
	a := InputHash([]byte(`{"id":3}`))
	b := InputHash([]byte(`{"id":3}`))
	c := InputHash([]byte(`{"id":4}`))
	if a != b {
		t.Fatal("同样的入参必须得到同样的指纹")
	}
	if a == c {
		t.Fatal("不同入参不该撞指纹")
	}
}

// Classify 是钩子那侧唯一的判定入口，走错一档就等于开了个口子。
func TestClassify(t *testing.T) {
	if c := Classify("Bash"); c.IsVipanel {
		t.Error("普通工具不该被认成面板操作")
	}
	if c := Classify(ToolPrefix + "website_tools"); !c.IsVipanel || !c.Meta {
		t.Error("板块入口应当是 Meta")
	}
	if c := Classify(ToolPrefix + "vipanel_overview"); !c.IsVipanel || !c.Meta {
		t.Error("内建工具应当是 Meta")
	}
	c := Classify(ToolPrefix + "website_delete")
	if !c.IsVipanel || c.Meta || c.Op == nil || c.Op.Risk != "destructive" {
		t.Errorf("website_delete 应当是 destructive 的具体操作，得到 %+v", c)
	}
	// 前缀对上但目录里没有：必须归为 IsVipanel 且 Op 为 nil，让调用方拒绝，
	// 而不是当成普通工具放过去。
	if c := Classify(ToolPrefix + "not_in_catalog"); !c.IsVipanel || c.Op != nil {
		t.Error("目录外的 vipanel 工具应当被认出来并拒绝，而不是漏过")
	}
}

func TestInstructionsWithinBudget(t *testing.T) {
	s := Instructions()
	if len(s) > 2000 {
		t.Errorf("instructions %d 字节，Claude Code 在 2KB 处截断", len(s))
	}
	if !strings.Contains(s, "本服务只提供面板独有的能力") {
		t.Error("缺少防重复那句——不写模型会拿 MCP 当文件工具用")
	}
}

// 对账两侧算的指纹必须一致。
//
// 钩子那侧拿到的是 harness 发来的**原始 JSON 字节**（键序随意、可能带空格、
// 还带着我们自己加的 vp* 整形参数），MCP 服务端那侧拿到的是解析后又摘掉整形
// 参数的 map。两边不归一化的话每一次面板操作都会被自己的对账拦死，
// 而且报出来的是「权限代理没生效」这种指向完全错误的信息。
func TestInputHashAgreesAcrossBothSides(t *testing.T) {
	// 钩子看到的原始字节：键序和服务端 map 序列化后不同，还多了整形参数
	rawFromHook := []byte(`{"vpPageSize": 50, "name":"web", "id": 3, "vpGrep":"x"}`)

	// 服务端解析后的参数（TakeShaper 已经摘掉 vp*）
	args := map[string]any{"id": float64(3), "name": "web"}

	if got, want := InputHashOfArgs(args), InputHash(rawFromHook); got != want {
		t.Fatalf("两侧指纹不一致：服务端 %s vs 钩子 %s", got, want)
	}
}

func TestInputHashIgnoresKeyOrderAndWhitespace(t *testing.T) {
	a := InputHash([]byte(`{"a":1,"b":2}`))
	b := InputHash([]byte(`{ "b" : 2 , "a" : 1 }`))
	if a != b {
		t.Fatal("键序和空格不该影响指纹")
	}
	if a == InputHash([]byte(`{"a":1,"b":3}`)) {
		t.Fatal("值变了指纹必须跟着变")
	}
}

// 走一遍钩子记账 → 服务端消费的完整链路，用两侧各自真实的输入形态。
func TestLedgerRoundTripWithShaperParams(t *testing.T) {
	g := Gate()
	g.Forget("rt")

	// 钩子侧：原始字节，带整形参数
	g.Record("rt", "container_list", InputHash([]byte(`{"vpPage":2,"state":"running"}`)), "tu-9")

	// 服务端侧：TakeShaper 摘掉 vp* 之后的 map
	args := map[string]any{"vpPage": float64(2), "state": "running"}
	_ = TakeShaper(args)

	if !g.Consume("rt", "container_list", InputHashOfArgs(args), "tu-9") {
		t.Fatal("同一次调用两侧应当对得上")
	}
}
