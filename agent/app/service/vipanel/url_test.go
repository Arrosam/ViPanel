package vipanel

import "testing"

func TestExtractURLsFromOSC8(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"OSC-8 超链接（URL 在序列内部，BEL 结尾）",
			"visit: \x1b]8;;https://claude.com/oauth?a=1&state=xyz\x07点这里\x1b]8;;\x07\r\n",
			"https://claude.com/oauth?a=1&state=xyz"},
		{"带 CSI 上色",
			"\x1b[36mhttps://example.com/p?q=1\x1b[0m\r\n",
			"https://example.com/p?q=1"},
		{"句尾标点不吞",
			"打开 https://example.com/x。\r\n",
			"https://example.com/x"},
	}
	for _, c := range cases {
		got := ExtractURLs([]byte(c.in))
		if len(got) == 0 {
			t.Fatalf("%s: 一个都没抓到", c.name)
		}
		if got[0] != c.want {
			t.Errorf("%s:\n  得到 %q\n  期望 %q", c.name, got[0], c.want)
		}
	}
}
