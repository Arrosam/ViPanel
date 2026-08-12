package vipanel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/1Panel-dev/1Panel/agent/app/model"
	"github.com/1Panel-dev/1Panel/agent/global"
	"github.com/google/uuid"
)

// Restore 面板启动时把磁盘上的会话读回来。
// **只重建条目，不启动任何进程** —— 起不起由实例池按 LRU 决定。
func Restore() {
	var rows []model.ViSession
	if err := global.DB.Find(&rows).Error; err != nil {
		global.LOG.Errorf("vipanel: 读取会话失败, err: %v", err)
		return
	}
	n := 0
	for _, r := range rows {
		// 目录已删，这个条目也没意义了
		if st, err := os.Stat(r.Cwd); err != nil || !st.IsDir() {
			continue
		}
		M().Put(newSession(r.SessionID, r.Title, r.Cwd, Get(r.Harness), r.LastUsed, r.TitlePinned))
		n++
	}
	if n > 0 {
		global.LOG.Infof("vipanel: 恢复 %d 个会话", n)
	}
}

func Create(cwd, title, harnessID string) (*Session, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil, errors.New("工作目录不能为空")
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return nil, errors.New("工作目录不存在")
	}
	if title = strings.TrimSpace(title); title == "" {
		title = filepath.Base(cwd)
	}
	h := Get(harnessID)

	// 新建时的标题**不算人定的**：它默认就是目录名，是个占位。
	// agent 起了更贴切的标题就该替换掉——这正是用户要的「自动更新」。
	// 只有走 Rename 才算人定。
	s := newSession(uuid.NewString(), title, cwd, h, time.Now().UnixMilli(), false)
	row := model.ViSession{
		SessionID: s.ID, Title: s.Title, Cwd: s.Cwd,
		Harness: h.ID(), LastUsed: s.lastUsed,
	}
	if err := global.DB.Create(&row).Error; err != nil {
		return nil, err
	}
	M().Put(s)
	return s, nil
}

func Rename(id, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("标题不能为空")
	}
	if len([]rune(title)) > 80 {
		title = string([]rune(title)[:80])
	}
	s, ok := M().Get(id)
	if !ok {
		return errors.New("会话不存在")
	}
	// 手工改名一律算人定：从此 agent 自动起的标题不再覆盖它
	s.applyTitle(title, true)
	return persistTitle(id, title, true)
}

// persistTitle 落库。标题和「是不是人定的」必须一起写：
// 只写标题的话，重启后 pinned 丢失，下一个自动标题就把用户改的名字冲掉了。
func persistTitle(id, title string, pinned bool) error {
	return global.DB.Model(&model.ViSession{}).
		Where("session_id = ?", id).
		Updates(map[string]any{"title": title, "title_pinned": pinned}).Error
}

func Delete(id string) error {
	M().Remove(id)
	return global.DB.Where("session_id = ?", id).Delete(&model.ViSession{}).Error
}

// Touch 把 lastUsed 落盘，让 LRU 顺序能跨重启保留。
func Touch(id string) {
	now := time.Now().UnixMilli()
	_ = global.DB.Model(&model.ViSession{}).
		Where("session_id = ?", id).Update("last_used", now).Error
}

// Restart 换掉底下的 agent 进程，会话本身（id / cwd / 标题 / 对话）全留着。
//
// 有对话时新进程会用 resume 接回原 transcript，上下文不丢。
// 不能实现成「删掉会话、用同样的 cwd 建一个新的」—— 新 id 对应新 transcript，
// 界面上看就是整个会话被抹了，而用户点重启要的是进程重来，不是丢上下文。
func Restart(id string) (*Session, error) {
	s, ok := M().Get(id)
	if !ok {
		return nil, errors.New("会话不存在")
	}
	s.stop("")
	return M().Activate(id)
}

// SetPoolSize 调整实例池上限并落盘。
func SetPoolSize(n int) {
	M().SetPoolSize(n)
	saveSetting(poolSizeKey, strconv.Itoa(M().PoolSize()))
}

// LoadPoolSize 启动时读回上限。
func LoadPoolSize() {
	if v := loadSetting(poolSizeKey); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			M().SetPoolSize(n)
		}
	}
}

const poolSizeKey = "ViPanelPoolSize"

func loadSetting(key string) string {
	var s model.Setting
	if err := global.DB.Where("key = ?", key).First(&s).Error; err != nil {
		return ""
	}
	return s.Value
}

func saveSetting(key, value string) {
	var s model.Setting
	if err := global.DB.Where("key = ?", key).First(&s).Error; err != nil {
		_ = global.DB.Create(&model.Setting{Key: key, Value: value}).Error
		return
	}
	_ = global.DB.Model(&model.Setting{}).Where("key = ?", key).Update("value", value).Error
}

// History 列出磁盘上还没被面板收录的历史对话。
func History(harnessID string, limit int) []Discovered {
	d, ok := Get(harnessID).(Discoverer)
	if !ok {
		return []Discovered{}
	}
	known := map[string]bool{}
	for _, s := range M().All() {
		known[s.ID] = true
	}
	out := d.Discover(known, limit)
	if out == nil {
		return []Discovered{}
	}
	return out
}

// OpenHistory 把一段磁盘上的对话收进面板，**沿用原 session id**。
//
// 沿用 id 是整件事的关键：新建一个 id 等于开一段全新对话，原来的记录还在磁盘上
// 但面板再也指不到它了，用户看到的就是「点开之后是空的」。
func OpenHistory(id, cwd, title string) (*Session, error) {
	if id == "" || cwd == "" {
		return nil, errors.New("缺少 id 或工作目录")
	}
	if s, ok := M().Get(id); ok {
		return s, nil // 已经收录过了，直接返回
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return nil, errors.New("原工作目录已不存在")
	}
	if title = strings.TrimSpace(title); title == "" {
		title = filepath.Base(cwd)
	}
	h := Get(DefaultHarness)
	s := newSession(id, title, cwd, h, time.Now().UnixMilli(), false)
	row := model.ViSession{
		SessionID: id, Title: title, Cwd: cwd, Harness: h.ID(), LastUsed: s.lastUsed,
	}
	if err := global.DB.Create(&row).Error; err != nil {
		return nil, err
	}
	M().Put(s)
	return s, nil
}

// ---------------------------------------------------------------------------
// harness 自己的登录
// ---------------------------------------------------------------------------

// InstallSpec 返回安装某个 harness 的命令，同时把「不该装」的情况挡在这里。
//
// 挡在服务端而不是只靠界面隐藏按钮：界面是可以绕过的，而这条路的终点是
// 在这台机器上以 root 跑一条命令。
func InstallSpec(harnessID string) (PtySpec, error) {
	h := Get(harnessID)
	if h.ID() != harnessID {
		return PtySpec{}, fmt.Errorf("没有这个 harness: %s", harnessID)
	}
	plan := h.InstallPlan()
	if !plan.Installable {
		return PtySpec{}, fmt.Errorf("%s 不支持从面板安装：%s", h.DisplayName(), plan.Note)
	}
	if Installed(h) {
		return PtySpec{}, fmt.Errorf("%s 已经装好了", h.DisplayName())
	}
	if miss := MissingPrereqs(h); len(miss) > 0 {
		return PtySpec{}, fmt.Errorf("缺少 %s：%s", miss[0].Binary, miss[0].Hint)
	}
	// 安装只加代理：中转那几个参数是给 agent 的，塞给 npm 会让它报错。
	return withProxyOnly(plan.Spec), nil
}

func AuthStatus(harnessID string) AuthState {
	a, ok := Get(harnessID).(Authenticator)
	if !ok {
		// 没有登录体系的 harness 归一成「不支持但可用」，
		// 下游就不必到处判空——demo 阶段第一天就是漏判这里炸的。
		return AuthState{Supported: false, LoggedIn: true, HookInstalled: HookInstalled()}
	}
	st := a.AuthStatus()
	st.HookInstalled = HookInstalled()
	return st
}

func AuthLogout(harnessID string) error {
	a, ok := Get(harnessID).(Authenticator)
	if !ok {
		return errors.New("当前 harness 没有登录体系")
	}
	return a.Logout()
}

func LoginSpec(harnessID, mode string) (PtySpec, error) {
	a, ok := Get(harnessID).(Authenticator)
	if !ok {
		return PtySpec{}, errors.New("当前 harness 没有登录体系")
	}
	return withOutbound(harnessID, a.LoginSpec(mode)), nil
}

// urlPattern 只认 http(s)，并且在任何控制字符处断开。
//
// 断在控制字符上是关键：claude 用 OSC-8 超链接序列输出登录地址，
// 形如 ESC ] 8 ; ; <URL> BEL。URL 就藏在这个序列**里面**，
// 所以不能先把 OSC 整段剥掉再找——那样会把地址一起删掉，
// 而这是整个登录流程唯一的出口。改成让 URL 自己终止在 BEL 上。
var urlPattern = regexp.MustCompile(`https?://[^\x00-\x20\x7f"'` + "`" + `<>()\[\]]+`)

// csiPattern 只剥 CSI（上色、光标移动）。OSC 故意不剥，理由见上。
var csiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// ExtractURLs 从一段原始终端输出里挑出链接。
func ExtractURLs(chunk []byte) []string {
	clean := csiPattern.ReplaceAll(chunk, nil)
	var out []string
	for _, m := range urlPattern.FindAll(clean, -1) {
		// 句尾标点不属于地址。中文输出里跟的是全角标点，一并去掉。
		out = append(out, strings.TrimRight(string(m), ".,;:。，、；："))
	}
	return out
}

// -- MCP 总开关 --------------------------------------------------------------

const mcpEnabledKey = "ViPanelMCPEnabled"

var mcpEnabled atomic.Bool

// MCPEnabled 是「允许 agent 操作面板」的总开关。
//
// 默认**开**：这是 ViPanel 相对于一个网页版 Claude Code 的唯一实质差别，
// 默认关等于默认没有。关掉时 ensureMCP 会把 harness 配置里的那条删掉，
// 新会话里 mcp__vipanel__* 一个都不剩。
func MCPEnabled() bool { return mcpEnabled.Load() }

func SetMCPEnabled(v bool) {
	mcpEnabled.Store(v)
	saveSetting(mcpEnabledKey, strconv.FormatBool(v))
}

// LoadMCPEnabled 启动时读回开关。没有记录时默认开。
func LoadMCPEnabled() {
	v := loadSetting(mcpEnabledKey)
	if v == "" {
		mcpEnabled.Store(true)
		return
	}
	b, err := strconv.ParseBool(v)
	mcpEnabled.Store(err != nil || b)
}
