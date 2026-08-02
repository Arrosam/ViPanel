package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func rows(n int) any {
	var out []any
	for i := 0; i < n; i++ {
		out = append(out, map[string]any{
			"id":   float64(i),
			"name": "site-" + string(rune('a'+i%26)),
			"note": strings.Repeat("x", 50),
		})
	}
	return map[string]any{"items": out, "total": float64(n)}
}

func TestShapePagesAndTellsHowToContinue(t *testing.T) {
	got, note := Shape(rows(55), Shaper{Page: 1, PageSize: 20}, []string{"id", "name"})
	list := got.([]any)
	if len(list) != 20 {
		t.Fatalf("期望一页 20 条，得到 %d", len(list))
	}
	// 模型必须知道还有更多，以及怎么拿——
	// 不告诉它的话它会以为这就是全部，然后照着残缺数据做决定
	if !strings.Contains(note, "还有 35 条") || !strings.Contains(note, "vpPage=2") {
		t.Errorf("提示没说清怎么继续翻页：%s", note)
	}
}

func TestShapeProjectsToDefaultFields(t *testing.T) {
	got, _ := Shape(rows(3), Shaper{Page: 1, PageSize: 20}, []string{"id", "name"})
	first := got.([]any)[0].(map[string]any)
	if _, ok := first["note"]; ok {
		t.Error("默认字段集之外的字段不该出现")
	}
	if _, ok := first["name"]; !ok {
		t.Error("默认字段集里的字段必须出现")
	}
}

func TestShapeStarGivesEverything(t *testing.T) {
	got, note := Shape(rows(3), Shaper{Page: 1, PageSize: 20, Fields: []string{"*"}}, []string{"id"})
	first := got.([]any)[0].(map[string]any)
	if _, ok := first["note"]; !ok {
		t.Error(`vpFields="*" 必须能拿到全部字段——否则信息就不可达了`)
	}
	if strings.Contains(note, "只显示了部分字段") {
		t.Error("已经是全字段了，不该再提示")
	}
}

// grep 在**完整行**上匹配，不受 fields 影响。
// 否则「按一个没显示的字段过滤」就做不到，而那恰恰是缩小范围最有用的方式。
func TestGrepMatchesHiddenFields(t *testing.T) {
	data := map[string]any{"items": []any{
		map[string]any{"id": float64(1), "name": "alpha", "secretTag": "keepme"},
		map[string]any{"id": float64(2), "name": "beta", "secretTag": "other"},
	}}
	got, note := Shape(data, Shaper{Page: 1, PageSize: 20, Grep: "keepme"}, []string{"id", "name"})
	list := got.([]any)
	if len(list) != 1 {
		t.Fatalf("期望 grep 命中 1 条，得到 %d", len(list))
	}
	if list[0].(map[string]any)["name"] != "alpha" {
		t.Error("命中的不是预期那条")
	}
	if !strings.Contains(note, "grep 命中 1 条") {
		t.Errorf("提示里应报告命中数：%s", note)
	}
}

func TestBadGrepReportsInsteadOfCrashing(t *testing.T) {
	_, note := Shape(rows(3), Shaper{Page: 1, PageSize: 20, Grep: "("}, nil)
	if !strings.Contains(note, "不是合法正则") {
		t.Errorf("非法正则应当给出可读提示，得到：%s", note)
	}
}

// 单条记录里的超长字段要截断。
// 应用商店的 readMe 能有几十 KB，一页 20 条就足以把整个输出预算吃光，
// 而截断是**静默**的——模型会照着残缺的 JSON 做决定。
func TestLongStringsAreTruncated(t *testing.T) {
	data := map[string]any{"items": []any{
		map[string]any{"id": float64(1), "readMe": strings.Repeat("A", 50000)},
	}}
	got, _ := Shape(data, Shaper{Page: 1, PageSize: 20, Fields: []string{"*"}}, nil)
	raw, _ := json.Marshal(got)
	if len(raw) > 2000 {
		t.Errorf("超长字段没被截断，输出 %d 字节", len(raw))
	}
	if !strings.Contains(string(raw), "已截断") {
		t.Error("截断必须显式标注，否则模型不知道自己看到的是残缺的")
	}
}

func TestTakeShaperRemovesItsOwnKeys(t *testing.T) {
	args := map[string]any{"id": float64(3), "vpPage": float64(2), "vpFields": "a,b"}
	sh := TakeShaper(args)
	if sh.Page != 2 || len(sh.Fields) != 2 {
		t.Fatalf("整形参数没解析对：%+v", sh)
	}
	// 整形参数是我们加的，绝不能跟着发给面板——面板不认识它们
	for k := range args {
		if strings.HasPrefix(k, "vp") {
			t.Errorf("整形参数 %s 泄漏进了发给面板的请求体", k)
		}
	}
	if args["id"] != float64(3) {
		t.Error("真正的参数被误删了")
	}
}

// fields 一个都没对上时返回原对象，而不是空壳。
// 空壳会让模型以为这条记录本身是空的——那比多给几个字段糟得多。
func TestProjectFallsBackWhenNothingMatches(t *testing.T) {
	got, _ := Shape(rows(1), Shaper{Page: 1, PageSize: 20}, []string{"fieldRenamedUpstream"})
	first := got.([]any)[0].(map[string]any)
	if len(first) == 0 {
		t.Fatal("字段全对不上时不该返回空对象")
	}
}
