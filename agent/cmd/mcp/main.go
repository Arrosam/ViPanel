// vipanel-mcp 是 claude 那侧启动的 MCP 服务，但它自己**什么都不做**。
//
// 它是一根管子：stdin 的 JSON-RPC 原样送进面板，面板的回应原样写回 stdout。
// 真正的 MCP 服务端跑在 1panel-agent 进程里——目录、板块状态、权限、审计
// 只有一份，面板改了设置立刻生效，不用等这个进程重启。
//
// 和 vipanel-hook 同构：只走本机 unix socket，不开端口，不带凭据。
// 面板靠**进程血缘**认出这根管子属于哪个会话（见 agent/app/service/vipanel/ancestry.go）。
//
// 一条铁律：**绝不能往 stdout 写除 JSON-RPC 之外的任何东西**。
// 一行 print 就能毁掉整条协议流，而且症状是「MCP 莫名其妙连不上」，极难查。
// 所有日志走 stderr。
package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"

	"github.com/gorilla/websocket"
)

const sockPath = "/etc/1panel/agent.sock"

// 用 websocket 而不是 HTTP：notifications/tools/list_changed 是服务端**主动推**的，
// 请求/响应模型接不住。板块工具注册完要立刻通知客户端重新拉清单。
const endpoint = "ws://unix/api/v2/ai/console/mcp/ws"

func main() {
	dialer := websocket.Dialer{
		NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sockPath)
		},
	}
	// 面板据此沿 /proc 往上走，确认这根管子确实是它自己 spawn 的某个会话的后代。
	// 能连上这个 socket 的进程已经是 root 了，所以伪造 pid 得不到额外的东西；
	// 这条校验的作用是**认出是哪个会话**，以及挡住用户自己终端里那个 claude 误连。
	hdr := http.Header{}
	hdr.Set("X-VP-Pipe-Pid", strconv.Itoa(os.Getpid()))

	conn, _, err := dialer.Dial(endpoint, hdr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "vipanel-mcp: 连不上面板:", err)
		os.Exit(1)
	}
	defer conn.Close()

	go func() {
		// stdin → 面板。MCP 的 stdio 传输是**按行分隔**的 JSON，不是 LSP 的
		// Content-Length 分帧，别搞混。
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			if err := conn.WriteMessage(websocket.TextMessage, line); err != nil {
				break
			}
		}
		_ = conn.Close()
	}()

	out := bufio.NewWriter(os.Stdout)
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		out.Write(msg)
		out.WriteByte('\n')
		if err := out.Flush(); err != nil {
			return
		}
	}
}
