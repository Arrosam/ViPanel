package vipanel

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/1Panel-dev/1Panel/agent/global"
)

// 多账号：把 harness 的登录态存成快照，之后可以切回来。
//
// 借的是 cc-switch 的两条核心做法：**切换时写回 live 配置文件**，以及
// **编辑活跃项时从 live 回填**。没借的是它的存储选择——它把一切放进
// 一个 SQLite 库，而我们这里存的是 OAuth token 和 API key，塞进面板的
// 业务库意味着它们会跟着数据库备份到处跑。这里改成文件，0600，
// 放在面板数据目录下自己的位置。
//
// 与 cc-switch 更大的一处不同在抽象的落点：那边每支持一个应用就写一套
// 读写逻辑，这里让 harness **声明**「一个账号由哪些文件、哪些键构成」，
// 搬运机制在下面只实现一次。加一个 harness 时要回答的是一句声明，
// 不是再抄一遍文件搬运。

// AccountArtifact 是构成一个账号的一份东西。
type AccountArtifact struct {
	// Path 是 live 配置的绝对路径。
	Path string
	// JSONKeys 非空时，只接管这个 JSON 文件里的这几个顶层键，其余原样不动。
	//
	// 这条是必需的，不是优化：claude 把账号信息（oauthAccount / userID）
	// 和一大堆无关状态（历史、提示计数、项目列表）塞在同一个 ~/.claude.json 里。
	// 整文件搬运会把无关状态一起换掉——切个账号顺带把历史记录换了，
	// 那是数据损坏，不是功能。
	JSONKeys []string
	// Optional 为真时，文件不存在不算错。
	// codex 的 auth.json 在登录前根本不存在。
	Optional bool
}

// AccountStore 由「能有多个账号并且切换」的 harness 实现。
//
// 可选接口：shell 没有账号这个概念，硬塞进主接口只会逼它写空方法。
// 这和 InstallPlan 的取舍不同——那边「怎么装」对每个 harness 都有答案，
// 而「账号」对 shell 是真的不适用。
type AccountStore interface {
	// AccountArtifacts 声明一个账号由哪些文件/键构成。
	AccountArtifacts() []AccountArtifact
	// DescribeCurrent 从 live 状态里读一个人能认出来的标识（邮箱、组织名）。
	// 读不出返回空串——那时面板会用时间戳兜底，而不是编一个名字。
	DescribeCurrent() string
}

// Account 是一个已保存的账号。
type Account struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Active  bool   `json:"active"`
	AddedAt int64  `json:"addedAt"`
}

// accountsRoot 是快照的存放位置。
//
// 放在面板数据目录下而不是 harness 自己的配置目录里：那些目录归 harness 管，
// 它随时可能重写、清理。目录本身 0700——里面是 OAuth token。
func accountsRoot() string {
	base := global.CONF.Base.InstallDir
	if base == "" {
		base = "/opt"
	}
	return filepath.Join(base, "1panel", "vipanel", "accounts")
}

// writeFileAtomic 是临时文件 + rename。和 writeConfig 同一套做法，
// 区别只是权限位可以指定——快照文件必须 0600。
func writeFileAtomic(path string, raw []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".vipanel-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func hashOfString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func accountDir(harnessID, id string) string {
	return filepath.Join(accountsRoot(), harnessID, id)
}

// Accounts 列出某个 harness 下已保存的账号，并标出哪个和当前 live 状态一致。
func Accounts(harnessID string) ([]Account, error) {
	h := Get(harnessID)
	st, ok := h.(AccountStore)
	if !ok {
		return nil, fmt.Errorf("%s 不支持账号管理", h.DisplayName())
	}
	root := filepath.Join(accountsRoot(), harnessID)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []Account{}, nil
		}
		return nil, err
	}
	live := liveFingerprint(st)
	out := make([]Account, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		a, err := readAccountMeta(harnessID, e.Name())
		if err != nil {
			continue // 坏掉的快照不该让整个列表打不开
		}
		a.Active = live != "" && a.fingerprint == live
		out = append(out, a.Account)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AddedAt < out[j].AddedAt })
	return out, nil
}

type accountMeta struct {
	Account
	fingerprint string
}

type metaFile struct {
	Label       string `json:"label"`
	AddedAt     int64  `json:"addedAt"`
	Fingerprint string `json:"fingerprint"`
}

func readAccountMeta(harnessID, id string) (accountMeta, error) {
	raw, err := os.ReadFile(filepath.Join(accountDir(harnessID, id), "meta.json"))
	if err != nil {
		return accountMeta{}, err
	}
	var m metaFile
	if err := json.Unmarshal(raw, &m); err != nil {
		return accountMeta{}, err
	}
	return accountMeta{
		Account:     Account{ID: id, Label: m.Label, AddedAt: m.AddedAt},
		fingerprint: m.Fingerprint,
	}, nil
}

// CaptureCurrent 把 harness 当前生效的登录状态存成一个账号。
//
// label 为空时用 DescribeCurrent() 读出来的标识；再读不到就用时间戳。
// 绝不编一个像模像样的假名字——用户分不清两个账号时，切换功能就是个陷阱。
func CaptureCurrent(harnessID, label string) (Account, error) {
	h := Get(harnessID)
	st, ok := h.(AccountStore)
	if !ok {
		return Account{}, fmt.Errorf("%s 不支持账号管理", h.DisplayName())
	}
	arts := st.AccountArtifacts()
	blobs, err := readArtifacts(arts)
	if err != nil {
		return Account{}, err
	}
	if len(blobs) == 0 {
		return Account{}, fmt.Errorf("%s 当前没有已登录的账号可保存", h.DisplayName())
	}

	if strings.TrimSpace(label) == "" {
		label = st.DescribeCurrent()
	}
	if strings.TrimSpace(label) == "" {
		label = time.Now().Format("2006-01-02 15:04")
	}

	id := newAccountID()
	dir := accountDir(harnessID, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Account{}, err
	}
	for name, b := range blobs {
		if err := writeFileAtomic(filepath.Join(dir, name), b, 0o600); err != nil {
			return Account{}, err
		}
	}
	m := metaFile{Label: label, AddedAt: time.Now().UnixMilli(), Fingerprint: fingerprintOf(blobs)}
	raw, _ := json.Marshal(m)
	if err := writeFileAtomic(filepath.Join(dir, "meta.json"), raw, 0o600); err != nil {
		return Account{}, err
	}
	return Account{ID: id, Label: label, Active: true, AddedAt: m.AddedAt}, nil
}

// Activate 把某个已保存账号写回 harness 的 live 配置。
//
// **写回之前先把当前 live 状态补存一份**（如果它还没被存过）。
// 否则用户切走之后就再也回不来了——那是丢账号，不是切换。
func Activate(harnessID, id string) error {
	h := Get(harnessID)
	st, ok := h.(AccountStore)
	if !ok {
		return fmt.Errorf("%s 不支持账号管理", h.DisplayName())
	}
	dir := accountDir(harnessID, id)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("没有这个账号: %s", id)
	}
	if err := autosaveLive(harnessID, st); err != nil {
		return err
	}

	arts := st.AccountArtifacts()
	for _, a := range arts {
		blob, err := os.ReadFile(filepath.Join(dir, artifactName(a)))
		if err != nil {
			if a.Optional && os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := restoreArtifact(a, blob); err != nil {
			return err
		}
	}
	return nil
}

// Forget 删掉一个已保存账号。
// 只删快照，不动 live 状态——用户要的是「别再列出它」，不是「把我登出」。
func Forget(harnessID, id string) error {
	if strings.ContainsAny(id, "/\\.") || id == "" {
		return fmt.Errorf("账号 id 不合法")
	}
	return os.RemoveAll(accountDir(harnessID, id))
}

// -- 搬运机制：只在这里实现一次 ----------------------------------------------

// artifactName 把一份 artifact 映射成快照目录里的文件名。
// 用路径的 base 加一段路径哈希，避免两个同名文件（比如两个 auth.json）撞车。
func artifactName(a AccountArtifact) string {
	sum := hashOfString(a.Path)
	return fmt.Sprintf("%s.%s", sum[:8], filepath.Base(a.Path))
}

func readArtifacts(arts []AccountArtifact) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, a := range arts {
		raw, err := os.ReadFile(a.Path)
		if err != nil {
			if os.IsNotExist(err) {
				continue // 缺就跳过；是不是「什么都没有」由调用方判断
			}
			return nil, err
		}
		if len(a.JSONKeys) > 0 {
			raw, err = pickJSONKeys(raw, a.JSONKeys)
			if err != nil {
				return nil, err
			}
			if raw == nil {
				continue
			}
		}
		out[artifactName(a)] = raw
	}
	return out, nil
}

// restoreArtifact 把一份快照写回去。
//
// 带 JSONKeys 的**只合并那几个键**，其余保持目标文件原样——
// 这是不把 claude 的历史记录一起换掉的关键。
func restoreArtifact(a AccountArtifact, blob []byte) error {
	if err := os.MkdirAll(filepath.Dir(a.Path), 0o700); err != nil {
		return err
	}
	if len(a.JSONKeys) == 0 {
		return writeFileAtomic(a.Path, blob, 0o600)
	}
	cur := map[string]any{}
	if raw, err := os.ReadFile(a.Path); err == nil {
		_ = json.Unmarshal(raw, &cur)
	}
	var patch map[string]any
	if err := json.Unmarshal(blob, &patch); err != nil {
		return err
	}
	for _, k := range a.JSONKeys {
		if v, ok := patch[k]; ok {
			cur[k] = v
		} else {
			delete(cur, k)
		}
	}
	merged, err := json.MarshalIndent(cur, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(a.Path, merged, 0o600)
}

func pickJSONKeys(raw []byte, keys []string) ([]byte, error) {
	var all map[string]any
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	sub := map[string]any{}
	for _, k := range keys {
		if v, ok := all[k]; ok {
			sub[k] = v
		}
	}
	if len(sub) == 0 {
		return nil, nil
	}
	return json.Marshal(sub)
}

// autosaveLive 在切换前把当前 live 状态补存一份，除非它已经被存过。
func autosaveLive(harnessID string, st AccountStore) error {
	live := liveFingerprint(st)
	if live == "" {
		return nil // 当前没登录，没什么可存
	}
	list, err := Accounts(harnessID)
	if err != nil {
		return err
	}
	for _, a := range list {
		if a.Active {
			return nil // 已经存过了
		}
	}
	_, err = CaptureCurrent(harnessID, "")
	return err
}

// liveFingerprint 是当前 live 状态的指纹，用来判断哪个已保存账号正生效。
func liveFingerprint(st AccountStore) string {
	blobs, err := readArtifacts(st.AccountArtifacts())
	if err != nil || len(blobs) == 0 {
		return ""
	}
	return fingerprintOf(blobs)
}

func fingerprintOf(blobs map[string][]byte) string {
	names := make([]string, 0, len(blobs))
	for n := range blobs {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf []byte
	for _, n := range names {
		buf = append(buf, n...)
		buf = append(buf, blobs[n]...)
	}
	return hashOfString(string(buf))
}

func newAccountID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
