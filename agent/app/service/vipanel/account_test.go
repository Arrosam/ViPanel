package vipanel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 带 JSONKeys 的 artifact 只能动声明的那几个键。
//
// 这是整套账号功能里最危险的一处：claude 把账号信息和历史记录塞在同一个
// ~/.claude.json 里，多动一个键就是把用户的历史换掉。
func TestRestoreOnlyTouchesDeclaredKeys(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "claude.json")

	// live 里有账号信息，也有一堆无关状态
	orig := map[string]any{
		"oauthAccount":   map[string]any{"emailAddress": "old@example.com"},
		"userID":         "old-user",
		"projects":       map[string]any{"/root": "一堆历史"},
		"promptCounter":  float64(42),
		"someOtherState": "别动我",
	}
	raw, _ := json.Marshal(orig)
	if err := os.WriteFile(live, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	art := AccountArtifact{Path: live, JSONKeys: []string{"oauthAccount", "userID"}}
	snapshot, _ := json.Marshal(map[string]any{
		"oauthAccount": map[string]any{"emailAddress": "new@example.com"},
		"userID":       "new-user",
	})
	if err := restoreArtifact(art, snapshot); err != nil {
		t.Fatal(err)
	}

	after := map[string]any{}
	b, _ := os.ReadFile(live)
	if err := json.Unmarshal(b, &after); err != nil {
		t.Fatal(err)
	}
	if got := after["oauthAccount"].(map[string]any)["emailAddress"]; got != "new@example.com" {
		t.Errorf("账号没切过去：%v", got)
	}
	if after["userID"] != "new-user" {
		t.Errorf("userID 没切：%v", after["userID"])
	}
	// 关键断言：无关状态必须原样还在
	for _, k := range []string{"projects", "promptCounter", "someOtherState"} {
		if _, ok := after[k]; !ok {
			t.Errorf("无关的键 %q 被搬运弄丢了", k)
		}
	}
	if after["someOtherState"] != "别动我" {
		t.Errorf("无关的键被改了：%v", after["someOtherState"])
	}
}

// 快照里没有某个键时，写回要把 live 里那个键删掉。
// 否则从「有组织」的账号切到「个人」账号，组织信息会留在原地。
func TestRestoreDropsKeysAbsentFromSnapshot(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "c.json")
	raw, _ := json.Marshal(map[string]any{"userID": "u", "oauthAccount": map[string]any{"a": 1}, "keep": true})
	_ = os.WriteFile(live, raw, 0o600)

	art := AccountArtifact{Path: live, JSONKeys: []string{"oauthAccount", "userID"}}
	snap, _ := json.Marshal(map[string]any{"userID": "u2"}) // 没有 oauthAccount
	if err := restoreArtifact(art, snap); err != nil {
		t.Fatal(err)
	}
	after := map[string]any{}
	b, _ := os.ReadFile(live)
	_ = json.Unmarshal(b, &after)
	if _, ok := after["oauthAccount"]; ok {
		t.Error("快照里没有的键应当被删掉，否则会残留上一个账号的信息")
	}
	if after["keep"] != true {
		t.Error("没声明的键不该被动")
	}
}

// 整文件型 artifact（没有 JSONKeys）要整份替换，权限 0600。
func TestRestoreWholeFileKeepsPerm(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "creds.json")
	_ = os.WriteFile(live, []byte(`{"old":true}`), 0o600)

	if err := restoreArtifact(AccountArtifact{Path: live}, []byte(`{"new":true}`)); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(live)
	if string(b) != `{"new":true}` {
		t.Errorf("内容不对：%s", b)
	}
	st, _ := os.Stat(live)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("凭据文件权限是 %o，必须是 600", st.Mode().Perm())
	}
}

// 指纹要能区分不同内容、且与文件顺序无关。
// 判错会让「哪个账号正生效」显示错误，用户以为切了其实没切。
func TestFingerprintDistinguishesContent(t *testing.T) {
	a := map[string][]byte{"x": []byte("1"), "y": []byte("2")}
	b := map[string][]byte{"y": []byte("2"), "x": []byte("1")} // 同内容不同插入顺序
	c := map[string][]byte{"x": []byte("1"), "y": []byte("3")}
	if fingerprintOf(a) != fingerprintOf(b) {
		t.Error("同样的内容必须得到同样的指纹")
	}
	if fingerprintOf(a) == fingerprintOf(c) {
		t.Error("不同的内容必须得到不同的指纹")
	}
}

// 不支持账号管理的 harness 要明确报错，而不是返回空列表让界面显示一个空面板。
func TestShellHasNoAccounts(t *testing.T) {
	if _, ok := Get("shell").(AccountStore); ok {
		t.Fatal("shell 不该实现 AccountStore")
	}
	if _, err := Accounts("shell"); err == nil {
		t.Error("对 shell 列账号应当报错")
	}
}

// claude 和 codex 的声明本身要自洽：路径非空、绝对路径。
func TestAccountArtifactsAreSane(t *testing.T) {
	for _, id := range []string{"claude-code", "codex"} {
		st, ok := Get(id).(AccountStore)
		if !ok {
			t.Fatalf("%s 应当实现 AccountStore", id)
		}
		arts := st.AccountArtifacts()
		if len(arts) == 0 {
			t.Errorf("%s: 没有声明任何 artifact", id)
		}
		seen := map[string]bool{}
		for _, a := range arts {
			if !filepath.IsAbs(a.Path) {
				t.Errorf("%s: artifact 路径不是绝对路径：%s", id, a.Path)
			}
			// 两份 artifact 映射到同一个快照文件名就会互相覆盖
			n := artifactName(a)
			if seen[n] {
				t.Errorf("%s: 两份 artifact 撞了同一个快照文件名 %s", id, n)
			}
			seen[n] = true
		}
	}
}

// id 里带路径分隔符会让 Forget 删到快照目录之外。
func TestForgetRejectsPathTraversal(t *testing.T) {
	for _, bad := range []string{"../../etc", "a/b", "..", ""} {
		if err := Forget("claude-code", bad); err == nil {
			t.Errorf("id %q 应当被拒绝", bad)
		}
	}
}

// 登出之后的空壳不能被存成账号。
//
// claude 登出时不会删掉 .credentials.json，只把 expiresAt 置 0，
// 而 ~/.claude.json 里的 oauthAccount 还留着上一个账号的邮箱。
// 只看文件在不在的话，会存出一个名字像模像样、实际是登出态的「账号」——
// 用户以后切过去只会把自己登出。真机上就是这么撞出来的。
func TestCaptureRefusesWhenLoggedOut(t *testing.T) {
	h := Get("claude-code")
	a, ok := h.(Authenticator)
	if !ok {
		t.Skip("claude 没实现 Authenticator")
	}
	if a.AuthStatus().LoggedIn {
		t.Skip("本机 claude 已登录，这条断言在登出态才有意义")
	}
	if _, err := CaptureCurrent("claude-code", ""); err == nil {
		t.Error("登出状态下不该允许保存账号")
	}
}
