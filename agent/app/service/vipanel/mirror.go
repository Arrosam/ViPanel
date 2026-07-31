package vipanel

import (
	"sync"
)

// 回放缓冲的大小。够装下一屏 TUI 的若干次完整重绘。
const mirrorBufSize = 256 << 10

// mirror 是 agent PTY 的输出扇出。
//
// 它必须存在的第一个理由不是「让用户看见」，而是**必须有人把 PTY 读空**。
// 没有读者时内核缓冲区会填满，届时 agent 的每次重绘都会写阻塞，
// 整个进程就静静地卡死在那里——看上去像「agent 没反应」，极难查。
//
// 第二个理由才是镜像：首次运行的主题选择、目录信任提示、以及任何
// 回合中途的 TUI 交互，都只存在于这块屏幕上，transcript 里一个字都没有。
// 没有镜像，这些提示就是无解的死锁。
type mirror struct {
	mu   sync.Mutex
	buf  []byte // 环形回放缓冲（简单实现：超长就丢弃前半）
	subs map[chan []byte]struct{}
}

func newMirror() *mirror {
	return &mirror{subs: map[chan []byte]struct{}{}}
}

func (m *mirror) write(b []byte) {
	m.mu.Lock()
	m.buf = append(m.buf, b...)
	if len(m.buf) > mirrorBufSize {
		// 丢掉前面一半。从中间截断会把某个转义序列劈成两半，
		// 回放时产生乱码；但 TUI 每次重绘都会重画全屏，
		// 下一帧就自愈了，比维护一个精确的序列边界划算得多。
		m.buf = append([]byte(nil), m.buf[len(m.buf)-mirrorBufSize/2:]...)
	}
	subs := make([]chan []byte, 0, len(m.subs))
	for ch := range m.subs {
		subs = append(subs, ch)
	}
	m.mu.Unlock()

	cp := append([]byte(nil), b...)
	for _, ch := range subs {
		select {
		case ch <- cp:
		default: // 跟不上的镜像端丢帧，不能让它卡住 agent
		}
	}
}

func (m *mirror) attach() ([]byte, chan []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan []byte, 64)
	m.subs[ch] = struct{}{}
	snapshot := append([]byte(nil), m.buf...)
	return snapshot, ch
}

func (m *mirror) detach(ch chan []byte) {
	m.mu.Lock()
	delete(m.subs, ch)
	m.mu.Unlock()
	close(ch)
}

func (m *mirror) reset() {
	m.mu.Lock()
	m.buf = nil
	m.mu.Unlock()
}

// AttachAgent 接上这个会话的 agent 屏幕。
//
// 回放的是**原始字节**，不做任何终端仿真。这一步之所以成立，
// 全靠 agent PTY 的尺寸是固定的（agentCols × agentRows）：
// 所有镜像端看到的都是同一个宽度下画出来的画面，原样重放即可。
// 如果尺寸跟着某个浏览器窗口走，就必须在服务端跑一个终端仿真器
// 重新排版——那是完全不同量级的工程。
func (s *Session) AttachAgent() ([]byte, chan []byte) {
	return s.screen.attach()
}

func (s *Session) DetachAgent(ch chan []byte) {
	s.screen.detach(ch)
}

// WriteAgent 把按键写给 agent，用于回应 TUI 提示。
func (s *Session) WriteAgent(b []byte) error {
	p := s.pty_()
	if p == nil || !p.Alive() {
		return errNoAgent
	}
	_, err := p.Write(b)
	s.mu.Lock()
	s.touch()
	s.mu.Unlock()
	return err
}

// pumpAgent 把 agent PTY 读空并扇出。每次 agent 启动都要跑一个。
func (s *Session) pumpAgent(p *Pty) {
	buf := make([]byte, 8192)
	for {
		n, err := p.Read(buf)
		if n > 0 {
			s.screen.write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}
