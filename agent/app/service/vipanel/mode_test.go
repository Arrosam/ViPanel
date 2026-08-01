package vipanel

import "testing"

func TestReadModeTakesLastOccurrence(t *testing.T) {
	h := claudeCode{}
	// 屏幕上混着历史：前面出现过 manual，最后才是当前的 plan。
	// 正着扫会读成 manual——这是实际踩过的 bug。
	screen := []byte("⏸ manual mode on · ? for shortcuts\r" +
		"\x1b[2m⏵⏵ accept edits on (shift+tab to cycle)\x1b[0m\r" +
		"⏸ plan mode on (shift+tab to cycle) · ← for agents")
	if got := h.ReadMode(screen); got != "plan" {
		t.Fatalf("期望 plan（最后一条），得到 %q", got)
	}
}

func TestReadModeAcceptEdits(t *testing.T) {
	h := claudeCode{}
	if got := h.ReadMode([]byte("⏵⏵ accept edits on (shift+tab to cycle)")); got != "accept edits" {
		t.Fatalf("期望 accept edits，得到 %q", got)
	}
}

func TestReadModeEmptyWhenAbsent(t *testing.T) {
	h := claudeCode{}
	if got := h.ReadMode([]byte("nothing here")); got != "" {
		t.Fatalf("期望空串，得到 %q", got)
	}
}
