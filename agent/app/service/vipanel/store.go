package vipanel

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
		M().Put(&Session{
			ID:       r.SessionID,
			Title:    r.Title,
			Cwd:      r.Cwd,
			Harness:  Get(r.Harness),
			lastUsed: r.LastUsed,
		})
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

	s := &Session{
		ID:       uuid.NewString(),
		Title:    title,
		Cwd:      cwd,
		Harness:  h,
		lastUsed: time.Now().UnixMilli(),
	}
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
	s.Title = title
	return global.DB.Model(&model.ViSession{}).
		Where("session_id = ?", id).Update("title", title).Error
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
