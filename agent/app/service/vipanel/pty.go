package vipanel

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/creack/pty"
	"github.com/pkg/errors"
)

// Pty 是一个跑在伪终端里的进程。
//
// 为什么不直接用 agent/utils/terminal 的 LocalCommand：那个把工作目录写死成
// 用户 home，也不接受自定义环境变量。ViPanel 的每个会话都绑在自己的目录上，
// 而且 harness 启动 agent 时需要注入环境变量（例如 hook 回调地址），
// 这两样都得能控制。除此之外行为保持一致，前端仍然用同一套 WsMsg 协议。
type Pty struct {
	cmd *exec.Cmd
	tty *os.File

	// 进程自己退出时关闭。区别于「客户端断开」——后者不该杀掉进程。
	done chan struct{}
}

type PtySpec struct {
	File string
	Args []string
	Cwd  string
	Env  []string // 追加在 os.Environ() 之后，同名覆盖
	Cols int
	Rows int
}

func StartPty(spec PtySpec) (*Pty, error) {
	cmd := exec.Command(spec.File, spec.Args...)

	// 剔除 CLAUDE_CODE_* 再继承环境。
	//
	// 关键的是 CLAUDE_CODE_CHILD_SESSION：claude 检测到自己是另一个 claude
	// 会话的子进程时会**关掉 transcript 写入**，而 ViPanel 的整条聊天链路
	// 完全建立在 transcript 上。生产环境里 agent 是 1panel-agent 的子进程
	// 不会中招，但只要面板本身是从某个 claude 终端里起的（开发时很常见），
	// 聊天区就会静默变空——终端里一切正常，看不出任何错误。
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "CLAUDE_CODE_") {
			continue
		}
		env = append(env, kv)
	}
	if os.Getenv("TERM") == "" {
		env = append(env, "TERM=xterm-256color")
	}
	cmd.Env = append(env, spec.Env...)

	cmd.Dir = spec.Cwd
	if cmd.Dir == "" {
		cmd.Dir, _ = os.UserHomeDir()
	}

	tty, err := pty.Start(cmd)
	if err != nil {
		return nil, errors.Wrapf(err, "启动 %s 失败", spec.File)
	}

	p := &Pty{cmd: cmd, tty: tty, done: make(chan struct{})}
	if spec.Cols > 0 && spec.Rows > 0 {
		_ = p.Resize(spec.Cols, spec.Rows)
	}
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

func (p *Pty) Read(b []byte) (int, error)  { return p.tty.Read(b) }
func (p *Pty) Write(b []byte) (int, error) { return p.tty.Write(b) }

// Pid 是 agent 进程的 pid。MCP 的血缘校验靠它认出连进来的管子属于哪个会话
// （见 ancestry.go）。进程还没起来时返回 0。
func (p *Pty) Pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Done 在底下的进程退出时关闭。
func (p *Pty) Done() <-chan struct{} { return p.done }

func (p *Pty) Alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *Pty) Resize(cols, rows int) error {
	win := struct{ row, col, x, y uint16 }{uint16(rows), uint16(cols), 0, 0}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		p.tty.Fd(),
		syscall.TIOCSWINSZ,
		uintptr(unsafe.Pointer(&win)),
	)
	if errno != 0 {
		return errno
	}
	return nil
}

// Close 终止进程。
//
// 先 SIGTERM 给一个体面退出的机会（TUI 通常要靠它恢复终端状态），
// 等不到就 SIGKILL。不往 tty 里写 ^C/^D/exit —— 那是 shell 的假设，
// 对一个 TUI agent 来说这些字节的含义完全不同。
func (p *Pty) Close() error {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
			_ = p.cmd.Process.Kill()
		}
	}
	return p.tty.Close()
}
