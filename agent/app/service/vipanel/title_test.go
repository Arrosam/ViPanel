package vipanel

import "testing"

// 字段名是这个 bug 的根：claude 写的是 aiTitle，而通用代码猜成了 title。
// 用真机上抓下来的原始行做样本，别再靠类推。
func TestClaudeTitleOfUsesRealFieldNames(t *testing.T) {
	c := claudeCode{}

	// 真机样本（vpfresh 上的 transcript）
	real := `{"type":"ai-title","aiTitle":"查询当前effort和模型配置","sessionId":"1dd4695b"}`
	got, manual := c.TitleOf([]byte(real))
	if got != "查询当前effort和模型配置" {
		t.Errorf("ai-title 取不到标题，得到 %q —— 字段名又对不上了", got)
	}
	if manual {
		t.Error("ai-title 是自动生成的，manual 必须为 false")
	}

	// custom-title 手头没有真实样本，所以两个键都得认
	for _, line := range []string{
		`{"type":"custom-title","customTitle":"我起的名字"}`,
		`{"type":"custom-title","title":"我起的名字"}`,
	} {
		got, manual := c.TitleOf([]byte(line))
		if got != "我起的名字" || !manual {
			t.Errorf("custom-title 解析失败: %s -> %q manual=%v", line, got, manual)
		}
	}

	// 不是标题行的一律不产出
	for _, line := range []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"user"}`,
		`不是 JSON`,
	} {
		if got, _ := c.TitleOf([]byte(line)); got != "" {
			t.Errorf("%s 不该产出标题，得到 %q", line, got)
		}
	}
}

// parseLine 必须把标题事件产出来——它以前一条都产不出，而且静默。
func TestParseLineEmitsTitleEvent(t *testing.T) {
	evs := parseLine([]byte(`{"type":"ai-title","aiTitle":"新标题"}`), claudeCode{})
	if len(evs) != 1 || evs[0].Type != EvTitle || evs[0].Text != "新标题" {
		t.Fatalf("期望一条 title 事件，得到 %+v", evs)
	}
}

// 人定的标题压过自动的，且此后不再被自动的覆盖。
// 不这样的话用户刚改完名，下一轮对话就被 agent 冲掉了。
func TestManualTitleWinsAndSticks(t *testing.T) {
	s := newSession("t", "占位", "/tmp", claudeCode{}, 0, false)

	if !s.applyTitle("AI 起的", false) || s.Title != "AI 起的" {
		t.Fatal("未固定时应当接受自动标题")
	}
	if !s.applyTitle("人起的", true) || s.Title != "人起的" {
		t.Fatal("人定的应当压过自动的")
	}
	if s.applyTitle("AI 又起了一个", false) {
		t.Fatal("固定之后不该再被自动标题覆盖")
	}
	if s.Title != "人起的" {
		t.Errorf("标题被冲掉了：%q", s.Title)
	}
	// 人可以再次改名
	if !s.applyTitle("人又改了", true) || s.Title != "人又改了" {
		t.Error("人定的标题应当能被人再次修改")
	}
}

func TestApplyTitleIgnoresEmptyAndTrims(t *testing.T) {
	s := newSession("t", "原标题", "/tmp", claudeCode{}, 0, false)
	if s.applyTitle("   ", false) {
		t.Error("空白标题不该被接受")
	}
	if s.Title != "原标题" {
		t.Error("空白标题不该改动原值")
	}
	long := make([]rune, 200)
	for i := range long {
		long[i] = '标'
	}
	s.applyTitle(string(long), false)
	if len([]rune(s.Title)) != 80 {
		t.Errorf("超长标题应当截到 80 字，得到 %d", len([]rune(s.Title)))
	}
}
