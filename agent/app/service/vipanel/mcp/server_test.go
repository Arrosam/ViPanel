package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// fakeTransport 把一串预设的请求喂给服务端，并收下它发出的每一条消息。
type fakeTransport struct {
	in  [][]byte
	out [][]byte
}

func (f *fakeTransport) Recv() ([]byte, error) {
	if len(f.in) == 0 {
		return nil, errDone
	}
	m := f.in[0]
	f.in = f.in[1:]
	return m, nil
}

func (f *fakeTransport) Send(b []byte) error {
	cp := make([]byte, len(b))
	copy(cp, b)
	f.out = append(f.out, cp)
	return nil
}

var errDone = &doneErr{}

type doneErr struct{}

func (*doneErr) Error() string { return "done" }

func req(s string) []byte { return []byte(s) }

func run(t *testing.T, sessionID string, msgs ...string) *fakeTransport {
	t.Helper()
	tp := &fakeTransport{}
	for _, m := range msgs {
		tp.in = append(tp.in, req(m))
	}
	NewServer(tp, sessionID).Run()
	return tp
}

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("回应不是合法 JSON: %v\n%s", err, raw)
	}
	return m
}

func TestInitializeDeclaresListChanged(t *testing.T) {
	tp := run(t, "s1", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if len(tp.out) != 1 {
		t.Fatalf("期望 1 条回应，得到 %d", len(tp.out))
	}
	res := decode(t, tp.out[0])["result"].(map[string]any)
	caps := res["capabilities"].(map[string]any)["tools"].(map[string]any)
	if caps["listChanged"] != true {
		t.Error("必须声明 tools.listChanged，否则板块工具注册出去客户端不会重新拉清单")
	}
	if !strings.Contains(res["instructions"].(string), "_tools") {
		t.Error("instructions 里必须说明 <板块>_tools 的用法——它是唯一一段上游加载的散文")
	}
}

// 开局只露板块入口和常驻工具，不露具体操作。
// 这条要是破了，190 个工具会一次性全部涌进模型的上下文。
func TestInitialToolsAreEntriesOnly(t *testing.T) {
	tp := run(t, "s1", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := toolNames(t, tp.out[0])

	for _, n := range tools {
		if strings.HasSuffix(n, "_tools") || strings.HasPrefix(n, "vipanel_") {
			continue
		}
		op := ByName(n)
		if op == nil {
			t.Errorf("开局露出了目录外的工具 %s", n)
			continue
		}
		if !op.Resident {
			t.Errorf("开局露出了非常驻工具 %s（板块 %s）", n, op.Module)
		}
	}
	if len(tools) > 40 {
		t.Errorf("开局露出 %d 个工具，太多了——上游只加载工具名，但这也是成本", len(tools))
	}
}

// 取清单 → 发 list_changed → 再取清单时该板块的工具已在列。
// 实测 Claude Code 会在**同一轮**内重新拉清单并调用新工具（MCP.md §10-3），
// 所以这条链路是整个渐进式披露的地基。
func TestModuleEntryRegistersToolsAndNotifies(t *testing.T) {
	tp := run(t, "s1",
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"website_tools","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
	)
	if len(tp.out) != 4 { // list, call 结果, list_changed 通知, list
		t.Fatalf("期望 4 条消息（含 list_changed 通知），得到 %d", len(tp.out))
	}

	before := toolNames(t, tp.out[0])
	notif := decode(t, tp.out[2])
	if notif["method"] != "notifications/tools/list_changed" {
		t.Fatalf("第三条应该是 list_changed 通知，得到 %v", notif["method"])
	}
	after := toolNames(t, tp.out[3])
	if len(after) <= len(before) {
		t.Fatalf("取过清单后工具数没有增加：%d → %d", len(before), len(after))
	}
	if !contains(after, "website_delete") {
		t.Error("website 板块打开后应该能看到 website_delete")
	}
	if contains(before, "website_delete") {
		t.Error("没打开板块时不该看到 website_delete")
	}

	// 清单本身只给名字和一句话，不给 schema——
	// 65 个工具的完整 schema 一次吐出来，渐进式披露就白做了
	body := textOf(t, tp.out[1])
	if strings.Contains(body, "inputSchema") || strings.Contains(body, "properties") {
		t.Error("板块清单里不该出现 schema")
	}
	if !strings.Contains(body, "website_delete") {
		t.Error("板块清单里应该列出工具名")
	}
}

// 台账里对不上的调用一律不执行。
// 钩子被删掉时，MCP 必须**整体失效而不是整体放行**。
func TestCallWithoutTicketIsRefused(t *testing.T) {
	tp := run(t, "s-no-ticket",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"website_tools","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"website_list","arguments":{}}}`,
	)
	body := textOf(t, tp.out[len(tp.out)-1])
	if !strings.Contains(body, "权限决定记录") {
		t.Errorf("没有台账凭据时应当拒绝执行，实际回应：%s", body)
	}
}

func TestUnknownToolIsRejected(t *testing.T) {
	tp := run(t, "s1",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope_nope","arguments":{}}}`)
	m := decode(t, tp.out[0])
	if m["error"] == nil {
		t.Error("未知工具应当返回错误")
	}
}

// 板块入口不该带任何服务器数据——它是免费的，因为它什么都不碰。
func TestModuleEntryTouchesNothing(t *testing.T) {
	tp := run(t, "s1",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"cert_tools","arguments":{}}}`)
	// 没有台账凭据，但它照样成功——证明它没走对账那条路
	body := textOf(t, tp.out[0])
	if strings.Contains(body, "权限决定记录") {
		t.Error("板块入口不该被对账拦住：它不碰服务器上的任何东西")
	}
	if !strings.Contains(body, "cert_ssl_delete") {
		t.Error("cert 板块清单里应该有 cert_ssl_delete")
	}
}

func toolNames(t *testing.T, raw []byte) []string {
	t.Helper()
	res, ok := decode(t, raw)["result"].(map[string]any)
	if !ok {
		t.Fatalf("不是 tools/list 的回应: %s", raw)
	}
	var out []string
	for _, x := range res["tools"].([]any) {
		out = append(out, x.(map[string]any)["name"].(string))
	}
	return out
}

func textOf(t *testing.T, raw []byte) string {
	t.Helper()
	res, ok := decode(t, raw)["result"].(map[string]any)
	if !ok {
		t.Fatalf("不是工具调用的回应: %s", raw)
	}
	content := res["content"].([]any)
	return content[0].(map[string]any)["text"].(string)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
