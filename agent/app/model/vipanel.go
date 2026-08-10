package model

// ViSession 是一个 ViPanel 会话在磁盘上的那一份。
//
// 只存「重建这个会话所需的最小信息」。运行时状态（进程是否活着、有没有未读、
// 是否在等回复）一律不落盘 —— 面板重启后那些状态本来就该是新的。
//
// SessionID 是 harness 认的那个 id（Claude Code 就是 `--session-id` 的 uuid），
// 不是数据库主键。对话记录归 harness 自己管，我们只记住这个 id 才能接回去。
type ViSession struct {
	BaseModel
	SessionID string `gorm:"uniqueIndex" json:"sessionId"`
	Title     string `json:"title"`
	Cwd       string `json:"cwd"`
	Harness   string `json:"harness"`
	LastUsed  int64  `json:"lastUsed"` // 毫秒时间戳，决定实例池的 LRU 顺序
	// TitlePinned 为真表示标题是人定的，agent 自动生成的标题不得覆盖它。
	// 必须落库：只放内存里的话，重启一次用户改的名字就被下一个 ai-title 冲掉了。
	TitlePinned bool `json:"titlePinned"`
}

func (ViSession) TableName() string {
	return "vi_sessions"
}
