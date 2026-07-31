package vipanel

import (
	"encoding/base64"
	"encoding/json"
	"sync"

	"github.com/1Panel-dev/1Panel/agent/global"
	"github.com/gorilla/websocket"
)

// 线上协议与上游终端保持一致，这样前端 components/terminal 可以原样复用。
// 见 agent/utils/terminal/ws_session.go 的 WsMsg。
const (
	msgCmd       = "cmd"
	msgResize    = "resize"
	msgHeartbeat = "heartbeat"
)

type wsMsg struct {
	Type      string `json:"type"`
	Data      string `json:"data,omitempty"`
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
	Timestamp int    `json:"timestamp,omitempty"`
}

// PtyBridge 把一个 WebSocket 和一个 Pty 接起来。
//
// 刻意不接上游的 AI 输入拦截器：那个东西会在用户敲回车时改写命令行，
// 对着一个 TUI agent 干这件事只会打乱它自己的输入状态机。
type PtyBridge struct {
	conn  *websocket.Conn
	pty   *Pty
	wmu   sync.Mutex
	close sync.Once
	done  chan struct{}

	// 旁路：登录流程要在原始输出里抓授权链接，但又不想为此另写一套 IO
	onOut func([]byte)
}

func NewPtyBridge(conn *websocket.Conn, p *Pty) *PtyBridge {
	return &PtyBridge{conn: conn, pty: p, done: make(chan struct{})}
}

// Tap 注册一个旁路回调，能看到 pty 的全部输出。
func (b *PtyBridge) Tap(fn func([]byte)) { b.onOut = fn }

// Send 让外部往 ws 上写一条自定义消息（与 pty 输出共用写锁）。
func (b *PtyBridge) Send(v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b.wmu.Lock()
	defer b.wmu.Unlock()
	return b.conn.WriteMessage(websocket.TextMessage, payload)
}

// Run 阻塞到任一侧结束：进程退出、客户端断开、或读写出错。
func (b *PtyBridge) Run() {
	go b.pumpOut()
	go b.pumpIn()

	select {
	case <-b.done:
	case <-b.pty.Done():
	}
	b.stop()
}

func (b *PtyBridge) stop() {
	b.close.Do(func() { close(b.done) })
}

// pty → ws
func (b *PtyBridge) pumpOut() {
	defer b.stop()
	buf := make([]byte, 4096)
	for {
		n, err := b.pty.Read(buf)
		if n > 0 {
			if b.onOut != nil {
				b.onOut(buf[:n])
			}
			if err := b.send(wsMsg{
				Type: msgCmd,
				Data: base64.StdEncoding.EncodeToString(buf[:n]),
			}); err != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// ws → pty
func (b *PtyBridge) pumpIn() {
	defer b.stop()
	for {
		_, raw, err := b.conn.ReadMessage()
		if err != nil {
			return
		}
		var m wsMsg
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		switch m.Type {
		case msgCmd:
			data, err := base64.StdEncoding.DecodeString(m.Data)
			if err != nil {
				global.LOG.Errorf("vipanel: ws cmd base64 解码失败, err: %v", err)
				continue
			}
			if _, err := b.pty.Write(data); err != nil {
				return
			}
		case msgResize:
			if m.Cols > 0 && m.Rows > 0 {
				_ = b.pty.Resize(m.Cols, m.Rows)
			}
		case msgHeartbeat:
			// 原样回弹，前端据此算延迟
			b.wmu.Lock()
			_ = b.conn.WriteMessage(websocket.TextMessage, raw)
			b.wmu.Unlock()
		}
	}
}

func (b *PtyBridge) send(m wsMsg) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b.wmu.Lock()
	defer b.wmu.Unlock()
	return b.conn.WriteMessage(websocket.TextMessage, payload)
}
