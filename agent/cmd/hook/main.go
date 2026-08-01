// vipanel-hook 是 claude 的 PreToolUse 钩子。
//
// claude 在每次调用工具前用它，把工具名和入参从 stdin 喂进来，并**同步等待**
// 它的输出。我们把请求转给面板、阻塞等浏览器上的人做决定，再按结果输出
// hookSpecificOutput.permissionDecision。
//
// 它必须是一个独立的小程序而不是 agent 里的一个函数：claude 只认
// 「执行一条命令」这一种钩子形态。
//
// 失败一律放行式降级？**不是**。见下面 fallback 的注释。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// 与面板通信只走本机 unix socket。
// 用 TCP 的话这个端点就成了一个「任何本地进程都能替用户做安全决定」的洞。
const sockPath = "/etc/1panel/agent.sock"

// 比面板自己的 120s 决定超时留出余量，但仍远小于 claude 侧 600s 的 fail-OPEN。
const httpTimeout = 150 * time.Second

type hookIn struct {
	SessionID     string          `json:"session_id"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolUseID     string          `json:"tool_use_id"`
	Cwd           string          `json:"cwd"`
	PermissionMod string          `json:"permission_mode"`
}

type verdict struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

func main() {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fallback("读取 hook 输入失败")
		return
	}
	var in hookIn
	if err := json.Unmarshal(raw, &in); err != nil {
		fallback("hook 输入不是合法 JSON")
		return
	}

	body, _ := json.Marshal(map[string]any{
		"sessionId": in.SessionID,
		"tool":      in.ToolName,
		"input":     in.ToolInput,
		"toolUseId": in.ToolUseID,
		"cwd":       in.Cwd,
		"mode":      in.PermissionMod,
	})

	client := &http.Client{
		Timeout: httpTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sockPath)
			},
		},
	}
	resp, err := client.Post("http://unix/api/v2/ai/console/hook/decide",
		"application/json", bytes.NewReader(body))
	if err != nil {
		fallback("面板未响应：" + err.Error())
		return
	}
	defer resp.Body.Close()

	var v verdict
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || v.Decision == "" {
		fallback("面板答复无法解析")
		return
	}
	emit(v.Decision, v.Reason)
}

// fallback 是面板联系不上时的降级。
//
// **降级为 ask，不是 allow。** ask 会让 claude 自己在 TUI 里问，
// 那至少还有一个人能看见并决定；allow 则是在没有任何人参与的情况下
// 放行一次以 root 身份执行的操作。这个面板没有沙箱兜底
// （见 PORT-1PANEL.md §1.2.5），放行式降级等于把唯一的护栏拆了。
func fallback(reason string) {
	emit("ask", "ViPanel 权限代理不可用（"+reason+"），已退回终端确认")
}

func emit(decision, reason string) {
	out, _ := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       decision,
			"permissionDecisionReason": reason,
		},
	})
	fmt.Println(string(out))
}
