package vipanel

import (
	"errors"
	"sort"
	"sync"

	"github.com/1Panel-dev/1Panel/agent/app/service/vipanel/mcp"
	"github.com/1Panel-dev/1Panel/agent/global"
)

// Manager 管全部会话，并按 LRU 限制同时活着的 agent 进程数。
//
// 会话数量不设上限，进程数设。每个 agent 都占着模型上下文和内存，
// 开十个会话就跑十个 claude 是不可接受的。
//
// 驱逐是**无损**的：对话记录在 harness 自己的 transcript 里，
// 重新激活时用 resume 接回去，历史不丢，代价只是一次启动延迟。
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	poolSize int
}

var (
	mgr     *Manager
	mgrOnce sync.Once
)

func M() *Manager {
	mgrOnce.Do(func() {
		mgr = &Manager{sessions: map[string]*Session{}, poolSize: 2}
	})
	return mgr
}

func (m *Manager) PoolSize() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.poolSize
}

func (m *Manager) SetPoolSize(n int) {
	if n < 1 {
		n = 1
	}
	if n > 16 {
		n = 16
	}
	m.mu.Lock()
	m.poolSize = n
	victims := m.overflowLocked()
	m.mu.Unlock()

	// 缩小后立刻把超出的挤掉，否则上限形同虚设
	for _, s := range victims {
		s.stop("实例池上限调小，已休眠")
	}
}

// Put 把一个会话登记进来（不启动进程）。
func (m *Manager) Put(s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.ID] = s
}

func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *Manager) Remove(id string) {
	m.mu.Lock()
	s := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if s != nil {
		// 板块授权和决定台账跟着会话一起消失。
		// 不清的话，同一个 id 被重新建出来会**继承上一次的授权**——
		// 那就等于跨会话记忆了，正是我们不要的。
		mcp.Gate().Forget(id)
		s.stopStream()
		s.stop("会话已结束")
	}
}

func (m *Manager) All() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastUsed() > out[j].LastUsed() })
	return out
}

// Activate 保证这个会话有一个活着的 agent，必要时驱逐别的。
func (m *Manager) Activate(id string) (*Session, error) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return nil, errors.New("会话不存在")
	}
	var victims []*Session
	if !s.Alive() {
		victims = m.makeRoomLocked(s)
	}
	m.mu.Unlock()

	for _, v := range victims {
		global.LOG.Infof("vipanel: 驱逐会话 %s（%s）", v.ID, v.Title)
		v.stop("被实例池驱逐，重新打开会自动接回对话")
	}
	if err := s.start(); err != nil {
		return nil, err
	}
	return s, nil
}

// makeRoomLocked 腾位置。调用方必须持锁。
func (m *Manager) makeRoomLocked(want *Session) []*Session {
	alive := m.aliveLocked()
	if len(alive) < m.poolSize {
		return nil
	}
	var out []*Session
	for len(alive)-len(out) >= m.poolSize {
		v := pickVictim(alive, out, want)
		if v == nil {
			break
		}
		out = append(out, v)
	}
	return out
}

func (m *Manager) overflowLocked() []*Session {
	alive := m.aliveLocked()
	var out []*Session
	for len(alive)-len(out) > m.poolSize {
		v := pickVictim(alive, out, nil)
		if v == nil {
			break
		}
		out = append(out, v)
	}
	return out
}

func (m *Manager) aliveLocked() []*Session {
	var out []*Session
	for _, s := range m.sessions {
		if s.Alive() {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastUsed() < out[j].LastUsed() })
	return out
}

// pickVictim 从最久未用的一头挑。
//
// 偏离严格 LRU 的一点：**优先跳过正在生成的会话**。严格队尾驱逐会把一个跑到
// 一半的回答直接杀掉，那次生成的 token 就白花了。全都在忙时才退回严格 LRU。
func pickVictim(alive, taken []*Session, except *Session) *Session {
	skip := func(s *Session) bool {
		if except != nil && s.ID == except.ID {
			return true
		}
		for _, t := range taken {
			if t.ID == s.ID {
				return true
			}
		}
		return false
	}
	for _, s := range alive {
		if skip(s) || s.Status() == StatusWorking {
			continue
		}
		return s
	}
	for _, s := range alive { // 全在忙，只能动最久未用的
		if skip(s) {
			continue
		}
		return s
	}
	return nil
}
