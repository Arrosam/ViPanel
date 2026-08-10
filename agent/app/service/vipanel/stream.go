package vipanel

import (
	"github.com/1Panel-dev/1Panel/agent/global"
	"strings"
	"sync"
	"time"
)

// 会话的事件订阅。一个会话可以同时被多台设备看着。
type subscriber struct {
	ch  chan []Event
	raw chan map[string]any // 事件之外的消息（权限请求等）
}

type stream struct {
	mu      sync.Mutex
	subs    map[*subscriber]struct{}
	tail    *Tailer
	hist    []Event
	ready   bool
	waiting bool // 已经有一个 goroutine 在等 transcript 出现
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
	if !s.Harness.Capabilities().StructuredEvents {
		st.ready = true // 没有结构化记录的 harness，聊天面板就是空的
		st.mu.Unlock()
		return
	}

	path := transcriptPath(s.ID)
	if path == "" {
		// 全新会话的 transcript 要等第一条消息才被创建。
		// **这里不能置 ready** —— 置了这条流就永久失效：
		// 之后文件出现了也没人去接，聊天面板会一直是空的，
		// 而 agent 其实回得好好的。这个 bug 真实发生过。
		if !st.waiting {
			st.waiting = true
			go s.waitTranscript()
		}
		st.mu.Unlock()
		return
	}
	// 先同步读完历史再从末尾接着 tail
	st.hist = ReadTranscript(path, s.Harness)
	st.tail = NewTailer(path, true, s.Harness)
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
		switch e.Type {
		case EvAssistant:
			s.mu.Lock()
			s.awaitingReply = false
			s.unread = true
			s.mu.Unlock()
		case EvTitle:
			// 这里以前是空的：标题事件被解析出来、广播出去，然后没人管。
			// 加上「谁都不消费」和上游字段名读错，是「会话标题不会自动更新」的全部原因。
			if s.applyTitle(e.Text, e.Manual) {
				if err := persistTitle(s.ID, s.Title, e.Manual); err != nil {
					global.LOG.Warnf("vipanel: 落库会话标题失败: %v", err)
				}
			}
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
	sub := &subscriber{ch: make(chan []Event, 64), raw: make(chan map[string]any, 32)}
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
	close(sub.raw)
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
	// awaitingReply 只在**有东西能把它清掉**的时候才置位。
	// 清它的唯一信号是 assistant 正文事件，所以：
	//   - harness 不产生结构化事件（shell 就是）→ 永远等不到，别置
	//   - 斜杠命令不产生 assistant 正文 → 同理别置
	// 两者都会让会话永久卡在「处理中」。这条是 shell harness 测出来的。
	if s.Harness.Capabilities().StructuredEvents &&
		!strings.HasPrefix(strings.TrimLeft(text, " \t"), "/") {
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

func (s *subscriber) Ch() <-chan []Event         { return s.ch }
func (s *subscriber) Raw() <-chan map[string]any { return s.raw }

// waitTranscript 等 transcript 文件出现。
//
// 新会话在说第一句话之前是没有这个文件的，而订阅往往发生在那之前
// （用户点开会话就连上了事件流）。轮询到文件出现后补上历史并开始 tail，
// 已经连着的订阅者会立刻收到这批事件。
func (s *Session) waitTranscript() {
	st := s.stream
	for i := 0; i < 1200; i++ { // 最多等 10 分钟，之后认定这个会话不会说话了
		time.Sleep(500 * time.Millisecond)

		st.mu.Lock()
		if st.ready { // 别处已经接上了
			st.waiting = false
			st.mu.Unlock()
			return
		}
		st.mu.Unlock()

		path := transcriptPath(s.ID)
		if path == "" {
			continue
		}

		hist := ReadTranscript(path, s.Harness)
		tail := NewTailer(path, true, s.Harness)

		st.mu.Lock()
		st.hist = hist
		st.tail = tail
		st.ready = true
		st.waiting = false
		subs := make([]*subscriber, 0, len(st.subs))
		for sub := range st.subs {
			subs = append(subs, sub)
		}
		st.mu.Unlock()

		// 补发给已经连着的订阅者：他们当初收到的是一个空历史
		for _, sub := range subs {
			select {
			case sub.ch <- hist:
			default:
			}
		}
		go tail.Run(func(evs []Event) { s.onEvents(evs) })
		return
	}
	st.mu.Lock()
	st.waiting = false
	st.mu.Unlock()
}

// 权限请求也走事件流：它在概念上就是对话的一部分，
// 单开一条通道只会让前端多维护一个连接和一套重连逻辑。
func (s *Session) broadcastPermission(req PermRequest) {
	s.fanout(map[string]any{"type": "permission_request", "request": req})
}

func (s *Session) broadcastPermissionResolved(id string, d Decision) {
	s.fanout(map[string]any{"type": "permission_resolved", "id": id, "decision": d})
}

// fanout 把一条任意消息推给所有订阅者。
func (s *Session) fanout(msg map[string]any) {
	st := s.stream
	st.mu.Lock()
	subs := make([]*subscriber, 0, len(st.subs))
	for sub := range st.subs {
		subs = append(subs, sub)
	}
	st.mu.Unlock()
	for _, sub := range subs {
		select {
		case sub.raw <- msg:
		default:
		}
	}
}

// Control 调整会话的运行时行为（模式 / 模型 / effort）。
func (s *Session) Control(kind, value string) error {
	c, ok := s.Harness.(Controller)
	if !ok {
		return errNotSupported
	}
	p := s.pty_()
	if p == nil || !p.Alive() {
		return errNoAgent
	}
	w := func(b []byte) { _, _ = p.Write(b) }
	switch kind {
	case "mode":
		c.CycleMode(w)
	case "model":
		c.SetModel(w, value)
	case "effort":
		c.SetEffort(w, value)
	default:
		return errNotSupported
	}
	s.mu.Lock()
	s.touch()
	s.mu.Unlock()
	return nil
}

// Mode 返回 agent 屏幕上读到的当前模式。
func (s *Session) Mode() string {
	c, ok := s.Harness.(Controller)
	if !ok {
		return ""
	}
	return c.ReadMode(s.Snapshot())
}
