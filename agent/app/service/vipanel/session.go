package vipanel

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/1Panel-dev/1Panel/agent/global"
)

// 固定的 agent 终端尺寸。
//
// agent 的 PTY 尺寸不能跟着浏览器窗口走：同一个会话可能被多台设备同时看着，
// 谁的窗口都不该改写 agent 的排版。给一个固定值，镜像端各自缩放。
const (
	agentCols = 120
	agentRows = 40
)

var (
	errNoAgent      = errors.New("agent 没有在运行")
	errNotSupported = errors.New("当前 harness 不支持这个操作")
)

func newSession(id, title, cwd string, h Harness, lastUsed int64, titlePinned bool) *Session {
	return &Session{
		ID: id, Title: title, Cwd: cwd, Harness: h, lastUsed: lastUsed,
		titlePinned: titlePinned,
		stream:      newStream(), screen: newMirror(),
	}
}

type SessionStatus string

const (
	StatusIdle     SessionStatus = "idle"
	StatusWorking  SessionStatus = "working"
	StatusUnread   SessionStatus = "unread"
	StatusSleeping SessionStatus = "sleeping"
	StatusError    SessionStatus = "error"
)

// Session 是一个会话的运行时对象。
type Session struct {
	ID      string
	Title   string
	Cwd     string
	Harness Harness

	mu     sync.Mutex
	pty    *Pty
	notes  string // 上一次停止的原因，给界面一句人话
	stream *stream
	screen *mirror

	// 状态是**状态机**，不是时间窗。
	// 早先用「距上次输出不到 3 秒算在忙」判定，agent 想得久一点灯就灭了——
	// 而那恰恰是最需要显示「在忙」的时候。
	awaitingReply bool
	unread        bool
	lastUsed      int64
	// titlePinned：标题是人定的，agent 自动起的标题不许覆盖。
	titlePinned bool
}

// applyTitle 收下 agent 自动生成或用户手工设定的标题。
//
// 规则只有一条：**人定的压过自动的，而且一旦人定过就不再被自动的覆盖。**
// 不这样的话，用户刚改完名字，下一轮对话 agent 重新起个标题就把它冲掉了。
//
// 返回 true 表示标题确实变了（调用方据此决定要不要落库和广播）。
func (s *Session) applyTitle(title string, manual bool) bool {
	title = strings.TrimSpace(title)
	if title == "" {
		return false
	}
	if len([]rune(title)) > 80 {
		title = string([]rune(title)[:80])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.titlePinned && !manual {
		return false
	}
	if s.Title == title && s.titlePinned == manual {
		return false
	}
	s.Title = title
	if manual {
		s.titlePinned = true
	}
	return true
}

func (s *Session) Status() SessionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.awaitingReply:
		return StatusWorking
	case s.unread:
		return StatusUnread
	case s.pty == nil || !s.pty.Alive():
		return StatusSleeping
	default:
		return StatusIdle
	}
}

func (s *Session) Alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pty != nil && s.pty.Alive()
}

func (s *Session) LastUsed() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUsed
}

func (s *Session) Notes() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notes
}

func (s *Session) touch() {
	s.lastUsed = time.Now().UnixMilli()
}

// start 拉起 agent 进程。已经活着就什么都不做。
func (s *Session) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pty != nil && s.pty.Alive() {
		s.touch()
		return nil
	}

	// 只有**确实产生过对话**才能 resume。
	// 对一个没发过任何消息的 session id 执行 --resume，claude 会以
	// "No conversation found with session ID: …" 立刻退出。
	// 新建会话还没说话就切走再切回来，一定会撞上这条路径。
	resume := s.Harness.Capabilities().Resume && s.Harness.HasHistory(s.ID, s.Cwd)

	// 出站配置（代理 / 中转端点）必须在这里注入，否则会话根本连不上模型。
	spec := withOutbound(s.Harness.ID(), s.Harness.Spawn(SpawnContext{
		SessionID: s.ID,
		Cwd:       s.Cwd,
		Resume:    resume,
		Cols:      agentCols,
		Rows:      agentRows,
	}))
	p, err := StartPty(spec)
	if err != nil {
		return err
	}
	s.pty = p
	s.notes = ""
	s.touch()

	// 必须立刻有人读 PTY，否则内核缓冲填满后 agent 会写阻塞卡死
	s.screen.reset()
	go s.pumpAgent(p)

	// resume 失败会立刻退出。退回不带 resume 重来一次，不让会话卡死。
	if resume {
		go s.watchResumeFailure(p)
	}
	return nil
}

func (s *Session) watchResumeFailure(p *Pty) {
	started := time.Now()
	<-p.Done()
	if time.Since(started) > 5*time.Second {
		return
	}
	s.mu.Lock()
	if s.pty != p { // 已经被换掉了，不管
		s.mu.Unlock()
		return
	}
	s.pty = nil
	s.notes = "resume 失败，已用新会话重启"
	s.mu.Unlock()

	global.LOG.Infof("vipanel: 会话 %s resume 失败，退回新建", s.ID)
	spec := withOutbound(s.Harness.ID(), s.Harness.Spawn(SpawnContext{
		SessionID: s.ID, Cwd: s.Cwd, Resume: false, Cols: agentCols, Rows: agentRows,
	}))
	if np, err := StartPty(spec); err == nil {
		s.mu.Lock()
		s.pty = np
		s.mu.Unlock()
		s.screen.reset()
		go s.pumpAgent(np)
	}
}

// stop 杀掉进程，会话本身保留。
func (s *Session) stop(reason string) {
	s.mu.Lock()
	p := s.pty
	s.pty = nil
	s.notes = reason
	// 进程都没了，那次回复不会再来。不清就永远停在「处理中」。
	s.awaitingReply = false
	s.mu.Unlock()

	if p != nil {
		_ = p.Close()
	}
}

func (s *Session) pty_() *Pty {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pty
}
