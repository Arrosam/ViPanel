package vipanel

import (
	"errors"
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

func newSession(id, title, cwd string, h Harness, lastUsed int64) *Session {
	return &Session{ID: id, Title: title, Cwd: cwd, Harness: h, lastUsed: lastUsed, stream: newStream()}
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

	// 状态是**状态机**，不是时间窗。
	// 早先用「距上次输出不到 3 秒算在忙」判定，agent 想得久一点灯就灭了——
	// 而那恰恰是最需要显示「在忙」的时候。
	awaitingReply bool
	unread        bool
	lastUsed      int64
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

	spec := s.Harness.Spawn(SpawnContext{
		SessionID: s.ID,
		Cwd:       s.Cwd,
		Resume:    resume,
		Cols:      agentCols,
		Rows:      agentRows,
	})
	p, err := StartPty(spec)
	if err != nil {
		return err
	}
	s.pty = p
	s.notes = ""
	s.touch()

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
	spec := s.Harness.Spawn(SpawnContext{
		SessionID: s.ID, Cwd: s.Cwd, Resume: false, Cols: agentCols, Rows: agentRows,
	})
	if np, err := StartPty(spec); err == nil {
		s.mu.Lock()
		s.pty = np
		s.mu.Unlock()
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
