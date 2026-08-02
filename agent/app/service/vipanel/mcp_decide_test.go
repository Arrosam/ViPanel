package vipanel

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/1Panel-dev/1Panel/agent/app/service/vipanel/mcp"
)

// 权限卡片上是「删除网站 example.com」还是 POST /websites/del {"id":3}，
// 决定了权限代理是真护栏还是摆设——人看不懂就会一路点允许。
// 这几条测的就是那句话怎么拼出来。
func TestRenderCardFillsPlaceholders(t *testing.T) {
	op := &mcp.Op{
		Name:     "website_delete",
		FormatZH: "删除网站 [domain]",
	}
	// resolveNames 在没有 DB 时直接返回，所以这里直接给出解析后的值
	got := renderCard(op, json.RawMessage(`{"id":3,"domain":"example.com"}`))
	if got != "删除网站 example.com" {
		t.Errorf("得到 %q", got)
	}
}

// 占位符填不上时**保留原样**，不能把它抹掉。
// 抹掉的话「删除网站 」看着像一句完整的话，人会以为自己看懂了。
func TestRenderCardKeepsUnresolvedPlaceholder(t *testing.T) {
	op := &mcp.Op{FormatZH: "删除网站 [domain]"}
	got := renderCard(op, json.RawMessage(`{"id":3}`))
	if !strings.Contains(got, "[domain]") {
		t.Errorf("填不上的占位符应当原样留着，得到 %q", got)
	}
}

// 拆出来的动作工具，卡片上要显示的是**固定后**的动作，
// 不是模型传了什么。container_remove 的卡片必须说 remove。
func TestRenderCardUsesFixedParams(t *testing.T) {
	op := &mcp.Op{
		FormatZH: "容器 [names] 执行 [operation]",
		Fixed:    `{"operation":"remove"}`,
	}
	got := renderCard(op, json.RawMessage(`{"names":["web"],"operation":"restart"}`))
	if !strings.Contains(got, "remove") {
		t.Errorf("固定参数必须覆盖模型传的值，得到 %q", got)
	}
	if strings.Contains(got, "restart") {
		t.Errorf("卡片上不该出现被覆盖掉的值，得到 %q", got)
	}
}

func TestRenderCardFallsBackToDesc(t *testing.T) {
	op := &mcp.Op{Name: "x_y", DescZH: "干一件事"}
	if got := renderCard(op, json.RawMessage(`{}`)); got != "干一件事" {
		t.Errorf("没有 formatZH 时应退回 desc.zh，得到 %q", got)
	}
	op2 := &mcp.Op{Name: "x_y"}
	if got := renderCard(op2, json.RawMessage(`{}`)); got != "x_y" {
		t.Errorf("什么都没有时至少给工具名，得到 %q", got)
	}
}

// 目录里所有的解析配方都得是安全标识符——它们会被拼进 SQL。
// 上游哪天在 x-panel-log 里写了个带引号的表名，这条会立刻炸出来。
func TestAllResolversUseSafeIdentifiers(t *testing.T) {
	for _, op := range mcp.Catalog {
		for _, r := range op.Resolvers {
			if !safeIdent(r.DB) || !safeIdent(r.InputColumn) || !safeIdent(r.OutputColumn) {
				t.Errorf("%s 的解析配方含不安全标识符: %+v", op.Name, r)
			}
		}
	}
}

// 非面板工具必须原样走老路径，一个字节都不该被改动。
func TestDecideMCPIgnoresNormalTools(t *testing.T) {
	if _, handled := DecideMCP(PermRequest{Tool: "Bash", Input: json.RawMessage(`{}`)}); handled {
		t.Error("Bash 不该被 MCP 判定接管")
	}
}

// 目录外但带 vipanel 前缀的工具必须被拒绝，而不是漏过去当普通工具。
func TestDecideMCPRejectsUnknownVipanelTool(t *testing.T) {
	v, handled := DecideMCP(PermRequest{
		Tool:  mcp.ToolPrefix + "definitely_not_a_tool",
		Input: json.RawMessage(`{}`),
	})
	if !handled {
		t.Fatal("带前缀的工具必须由 MCP 判定接管")
	}
	if v.Decision != DecideDeny {
		t.Errorf("目录外的工具应当拒绝，得到 %v", v.Decision)
	}
}

// 面板操作不能降级成 ask。
//
// 对普通工具来说「退回终端」是合理降级——TUI 那边还有个人能看见。
// 但面板操作就算在终端里被允许，也没有台账凭据，MCP 服务端会拒绝执行，
// 而它给出的理由是「权限代理不可用」，指向完全错误的方向。
func TestMCPNeverDegradesToAsk(t *testing.T) {
	src, err := os.ReadFile("mcp_decide.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "case DecideAsk:") {
		t.Error("DecideMCP 必须显式处理 DecideAsk（超时/退回终端），不能让它落到默认分支")
	}
}

// 常驻工具绕过板块授权，直接放行，不弹卡片。
// 这条要是破了，开局那四个「看一眼」的工具每个都要先弹一次窗。
func TestResidentReadIsAllowedWithoutModuleAuth(t *testing.T) {
	var resident *mcp.Op
	for i := range mcp.Catalog {
		if mcp.Catalog[i].Resident {
			resident = &mcp.Catalog[i]
			break
		}
	}
	if resident == nil {
		t.Skip("目录里没有常驻工具")
	}
	mcp.Gate().Forget("res-test")
	v, handled := DecideMCP(PermRequest{
		SessionID: "res-test",
		Tool:      mcp.ToolPrefix + resident.Name,
		Input:     json.RawMessage(`{}`),
	})
	if !handled || v.Decision != DecideAllow {
		t.Fatalf("常驻只读工具应当直接放行，得到 handled=%v decision=%v", handled, v.Decision)
	}
	// 而且不能顺带把整个板块授权掉——它只是自己免检
	if mcp.Gate().ModuleAuthorized("res-test", resident.Module) {
		t.Error("常驻工具不该顺带授权整个板块")
	}
}
