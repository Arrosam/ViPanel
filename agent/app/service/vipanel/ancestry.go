package vipanel

import (
	"os"
	"strconv"
	"strings"
)

// 血缘校验：一个连进来的 MCP 管子，必须是我们自己 spawn 的某个会话的后代。
//
// **这条不是防攻击者的**——agent.sock 是 root-only，能连上来的进程已经是 root 了，
// root 已经赢了，伪造 pid 得不到它本来得不到的东西。它做的是另外两件事：
//
//  1. 认出这是**哪个会话**。不用发 token、不用往配置里塞 session id，
//     没有轮转问题，也没有「多会话共用一份 user scope 配置该写谁的 token」这个死结。
//  2. 挡住误连。~/.claude.json 里那条 mcpServers 配置是 user scope 的，
//     用户自己在终端里跑的 claude 也会读到它——但那个进程不是面板的后代，拒掉。
//     于是我们可以放心用没有批准摩擦的 user scope。见 MCP.md §3.2 / §7。
//
// 实测（claude 2.1.220）MCP 子进程是 claude 的**直接**子进程，
// 没有双 fork、没有 setsid 脱离，祖先链走得通：
//
//	python3(mcp) <- claude <- ...
const ancestryMaxDepth = 24

// parentOf 读 /proc/<pid>/stat 取 PPID。
//
// 不能按空格切分整行：第二个字段是 comm，形如 (nginx: worker)，**本身带空格和括号**。
// 必须从最后一个 ')' 之后开始数。这是 /proc 解析的经典坑。
func parentOf(pid int) (int, bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	s := string(raw)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return 0, false
	}
	fields := strings.Fields(s[i+2:])
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, false
	}
	return ppid, true
}

// SessionByDescendant 沿 pid 的祖先链往上走，找出它属于哪个会话。
// 找不到返回 nil —— 调用方必须当成「拒绝」，不能当成「未知但放行」。
func (m *Manager) SessionByDescendant(pid int) *Session {
	if pid <= 1 {
		return nil
	}

	// 先把当前活着的 agent 进程 pid 收集成表，再走祖先链，
	// 避免每上溯一层就锁一次 manager
	owners := map[int]*Session{}
	m.mu.Lock()
	for _, s := range m.sessions {
		if p := s.pty_(); p != nil && p.Alive() {
			if apid := p.Pid(); apid > 0 {
				owners[apid] = s
			}
		}
	}
	m.mu.Unlock()
	if len(owners) == 0 {
		return nil
	}

	for depth := 0; depth < ancestryMaxDepth && pid > 1; depth++ {
		if s, ok := owners[pid]; ok {
			return s
		}
		next, ok := parentOf(pid)
		if !ok || next == pid {
			return nil
		}
		pid = next
	}
	return nil
}
