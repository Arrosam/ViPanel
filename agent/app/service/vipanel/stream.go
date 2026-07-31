package vipanel

import (
	"strings"
	"sync"
)

// 会话的事件订阅。一个会话可以同时被多台设备看着。
type subscriber struct {
	ch chan []Event
}

type stream struct {
	mu    sync.Mutex
	subs  map[*subscriber]struct{}
	tail  *Tailer
	hist  []Event
	ready bool
}

func newStream() *stream {
	return &stream{subs: map[*subscriber]struct{}{}}
}

// ensure 保证 transcript 已被读进来并开始 tail。
//
// 先**同步**把历史读完再从末尾接着 tail，而不是让 tail 从头跑一遍：
// 后者会让「历史」和「新事件」的时序混在一起，前端分不清哪些该一次性渲染、
// 哪些该追加。
func (s *Session) ensureStream() {
	st := s.stream
	st.mu.Lock()
	if st.ready {
		st.mu.Unlock()
		return
	}
	path := ""
	if s.Harness.Capabilities().StructuredEvents {
		path = transcriptPath(s.ID)
	}
	if path == "" {
		st.ready = true // 没有结构化记录的 harness，聊天面板就是空的
		st.mu.Unlock()
		return
	}
	st.hist = ReadTranscript(path)
	st.tail = NewTailer(path, true)
	st.ready = true
	st.mu.Unlock()

	go st.tail.Run(func(evs []Event) { s.onEvents(evs) })
}

// onEvents 是**唯一**改写「在忙 / 未读」的地方。
//
// 状态由事件驱动而不是由计时器驱动：只有真的等到了 assistant 的正文，
// 才认为这一轮结束。thinking 和 tool 都不算——那正是它还在干活的证据。
func (s *Session) onEvents(evs []Event) {
	st := s.stream
	st.mu.Lock()
	st.hist = append(st.hist, evs...)
	subs := make([]*subscriber, 0, len(st.subs))
	for sub := range st.subs {
		subs = append(subs, sub)
	}
	st.mu.Unlock()

	for _, e := range evs {
		if e.Type == EvAssistant {
			s.mu.Lock()
			s.awaitingReply = false
			s.unread = true
			s.mu.Unlock()
		}
	}

	for _, sub := range subs {
		select {
		case sub.ch <- evs:
		default: // 订阅者跟不上就丢，不能让一个慢客户端把整条流卡住
		}
	}
}

// Subscribe 返回历史 + 一个新事件通道。
func (s *Session) Subscribe() ([]Event, *subscriber) {
	s.ensureStream()
	st := s.stream

	st.mu.Lock()
	defer st.mu.Unlock()
	sub := &subscriber{ch: make(chan []Event, 64)}
	st.subs[sub] = struct{}{}
	hist := make([]Event, len(st.hist))
	copy(hist, st.hist)
	return hist, sub
}

func (s *Session) Unsubscribe(sub *subscriber) {
	st := s.stream
	st.mu.Lock()
	delete(st.subs, sub)
	st.mu.Unlock()
	close(sub.ch)
}

// MarkRead 用户看过了，熄灯。
func (s *Session) MarkRead() {
	s.mu.Lock()
	s.unread = false
	s.mu.Unlock()
}

// Send 把一条消息交给 agent。
func (s *Session) Send(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	p := s.pty_()
	if p == nil || !p.Alive() {
		return errNoAgent
	}
	s.Harness.Submit(func(b []byte) { _, _ = p.Write(b) }, text)

	s.mu.Lock()
	// 斜杠命令不产生 assistant 正文，置了 awaitingReply 就永远清不掉，
	// 会话会一直卡在「处理中」。
	if !strings.HasPrefix(strings.TrimLeft(text, " \t"), "/") {
		s.awaitingReply = true
	}
	s.unread = false
	s.touch()
	s.mu.Unlock()
	return nil
}

func (s *Session) Interrupt() error {
	if !s.Harness.Capabilities().Interrupt {
		return errNotSupported
	}
	p := s.pty_()
	if p == nil || !p.Alive() {
		return errNoAgent
	}
	s.Harness.Interrupt(func(b []byte) { _, _ = p.Write(b) })
	s.mu.Lock()
	s.awaitingReply = false
	s.mu.Unlock()
	return nil
}

// stopStream 会话被结束时收掉 tail。
func (s *Session) stopStream() {
	st := s.stream
	st.mu.Lock()
	t := st.tail
	st.tail = nil
	st.ready = false
	st.mu.Unlock()
	if t != nil {
		t.Stop()
	}
}

func (s *subscriber) Ch() <-chan []Event { return s.ch }
