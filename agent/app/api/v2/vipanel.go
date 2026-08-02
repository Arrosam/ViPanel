package v2

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strconv"
	"sync"

	"github.com/1Panel-dev/1Panel/agent/app/api/v2/helper"
	"github.com/1Panel-dev/1Panel/agent/app/dto"
	"github.com/1Panel-dev/1Panel/agent/app/service/vipanel"
	"github.com/1Panel-dev/1Panel/agent/app/service/vipanel/mcp"
	"github.com/1Panel-dev/1Panel/agent/global"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
)

// @Tags ViPanel
// @Summary ViPanel 控制台终端
// @Param cols query integer false "cols"
// @Param rows query integer false "rows"
// @Param cwd query string false "工作目录"
// @Success 200
// @Security ApiKeyAuth
// @Security Timestamp
// @Router /ai/console/pty [get]
func (b *BaseApi) WsConsolePty(c *gin.Context) {
	if !websocket.IsWebSocketUpgrade(c.Request) {
		helper.Success(c)
		return
	}
	conn, err := upGrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		global.LOG.Errorf("vipanel: websocket 升级失败, err: %v", err)
		return
	}
	defer conn.Close()

	if global.CONF.Base.IsDemo {
		wshandleError(conn, errors.New("   demo server, prohibit this operation!"))
		return
	}

	cols, _ := strconv.Atoi(c.DefaultQuery("cols", "80"))
	rows, _ := strconv.Atoi(c.DefaultQuery("rows", "40"))

	cwd := c.DefaultQuery("cwd", "")
	if cwd != "" {
		// 目录不在了就退回 home，而不是让整个终端起不来
		if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
			cwd = ""
		}
	}

	pty, err := vipanel.StartPty(vipanel.PtySpec{
		File: vipanel.LoginShell(),
		Cwd:  cwd,
		Cols: cols,
		Rows: rows,
	})
	if wshandleError(conn, errors.WithMessage(err, "终端启动失败")) {
		return
	}
	defer pty.Close()

	vipanel.NewPtyBridge(conn, pty).Run()
}

// ---------------------------------------------------------------------------
// 会话
// ---------------------------------------------------------------------------

// @Tags ViPanel
// @Summary 会话列表
// @Success 200 {array} dto.ViSessionItem
// @Router /ai/console/sessions [post]
func (b *BaseApi) ListViSessions(c *gin.Context) {
	helper.SuccessWithData(c, vipanel.ListItems())
}

// @Tags ViPanel
// @Summary 新建会话
// @Router /ai/console/sessions/create [post]
func (b *BaseApi) CreateViSession(c *gin.Context) {
	var req dto.ViSessionCreate
	if err := helper.CheckBindAndValidate(&req, c); err != nil {
		return
	}
	s, err := vipanel.Create(req.Cwd, req.Title, req.Harness)
	if err != nil {
		helper.BadRequest(c, err)
		return
	}
	helper.SuccessWithData(c, vipanel.ToItem(s))
}

// @Tags ViPanel
// @Summary 重命名会话
// @Router /ai/console/sessions/rename [post]
func (b *BaseApi) RenameViSession(c *gin.Context) {
	var req dto.ViSessionRename
	if err := helper.CheckBindAndValidate(&req, c); err != nil {
		return
	}
	if err := vipanel.Rename(req.ID, req.Title); err != nil {
		helper.BadRequest(c, err)
		return
	}
	helper.Success(c)
}

// @Tags ViPanel
// @Summary 结束会话
// @Router /ai/console/sessions/delete [post]
func (b *BaseApi) DeleteViSession(c *gin.Context) {
	var req dto.ViSessionID
	if err := helper.CheckBindAndValidate(&req, c); err != nil {
		return
	}
	if err := vipanel.Delete(req.ID); err != nil {
		helper.InternalServer(c, err)
		return
	}
	helper.Success(c)
}

// @Tags ViPanel
// @Summary 激活会话（必要时驱逐别的）
// @Router /ai/console/sessions/activate [post]
func (b *BaseApi) ActivateViSession(c *gin.Context) {
	var req dto.ViSessionID
	if err := helper.CheckBindAndValidate(&req, c); err != nil {
		return
	}
	s, err := vipanel.M().Activate(req.ID)
	if err != nil {
		helper.BadRequest(c, err)
		return
	}
	vipanel.Touch(req.ID)
	helper.SuccessWithData(c, vipanel.ToItem(s))
}

// @Tags ViPanel
// @Summary 重启会话的 agent 进程（对话保留）
// @Router /ai/console/sessions/restart [post]
func (b *BaseApi) RestartViSession(c *gin.Context) {
	var req dto.ViSessionID
	if err := helper.CheckBindAndValidate(&req, c); err != nil {
		return
	}
	s, err := vipanel.Restart(req.ID)
	if err != nil {
		helper.BadRequest(c, err)
		return
	}
	helper.SuccessWithData(c, vipanel.ToItem(s))
}

// ---------------------------------------------------------------------------
// harness 与实例池
// ---------------------------------------------------------------------------

// @Tags ViPanel
// @Summary 可用的 harness
// @Router /ai/console/harnesses [get]
func (b *BaseApi) ListViHarnesses(c *gin.Context) {
	helper.SuccessWithData(c, vipanel.Harnesses())
}

// @Tags ViPanel
// @Summary 实例池状态
// @Router /ai/console/pool [get]
func (b *BaseApi) GetViPool(c *gin.Context) {
	helper.SuccessWithData(c, vipanel.PoolInfo())
}

// @Tags ViPanel
// @Summary 设置实例池上限
// @Router /ai/console/pool/update [post]
func (b *BaseApi) UpdateViPool(c *gin.Context) {
	var req dto.ViPoolSize
	if err := helper.CheckBindAndValidate(&req, c); err != nil {
		return
	}
	vipanel.SetPoolSize(req.Size)
	helper.SuccessWithData(c, vipanel.PoolInfo())
}

// ---------------------------------------------------------------------------
// 第二条流：结构化事件
// ---------------------------------------------------------------------------

type viEventIn struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// @Tags ViPanel
// @Summary 会话事件流
// @Param id query string true "会话 id"
// @Router /ai/console/events [get]
func (b *BaseApi) WsConsoleEvents(c *gin.Context) {
	if !websocket.IsWebSocketUpgrade(c.Request) {
		helper.Success(c)
		return
	}
	conn, err := upGrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		global.LOG.Errorf("vipanel: websocket 升级失败, err: %v", err)
		return
	}
	defer conn.Close()

	id := c.Query("id")
	s, ok := vipanel.M().Get(id)
	if !ok {
		wshandleError(conn, errors.New("会话不存在"))
		return
	}

	hist, sub := s.Subscribe()
	defer s.Unsubscribe(sub)
	s.MarkRead()

	var wmu sync.Mutex
	send := func(v any) error {
		wmu.Lock()
		defer wmu.Unlock()
		return conn.WriteJSON(v)
	}
	if err := send(gin.H{"type": "history", "events": hist}); err != nil {
		return
	}
	// 补发这个会话上还没被决定的权限请求：
	// 新设备接上来时不该漏掉一个正在等人的确认
	for _, p := range vipanel.Broker().PendingFor(id) {
		if err := send(gin.H{"type": "permission_request", "request": p}); err != nil {
			return
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var in viEventIn
			if json.Unmarshal(raw, &in) != nil {
				continue
			}
			switch in.Type {
			case "message":
				// 发消息前先确保 agent 活着——会话可能刚被实例池挤下去
				if _, err := vipanel.M().Activate(id); err != nil {
					_ = send(gin.H{"type": "error", "message": err.Error()})
					continue
				}
				if err := s.Send(in.Text); err != nil {
					_ = send(gin.H{"type": "error", "message": err.Error()})
				}
			case "interrupt":
				if err := s.Interrupt(); err != nil {
					_ = send(gin.H{"type": "error", "message": err.Error()})
				}
			case "read":
				s.MarkRead()
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		case evs, ok := <-sub.Ch():
			if !ok {
				return
			}
			if err := send(gin.H{"type": "events", "events": evs}); err != nil {
				return
			}
		case msg, ok := <-sub.Raw():
			if !ok {
				return
			}
			if err := send(msg); err != nil {
				return
			}
		}
	}
}

// @Tags ViPanel
// @Summary 磁盘上还没收录的历史对话
// @Router /ai/console/history [get]
func (b *BaseApi) ListViHistory(c *gin.Context) {
	helper.SuccessWithData(c, vipanel.History(vipanel.DefaultHarness, 60))
}

// @Tags ViPanel
// @Summary 打开一段历史对话
// @Router /ai/console/history/open [post]
func (b *BaseApi) OpenViHistory(c *gin.Context) {
	var req struct {
		ID    string `json:"id"`
		Cwd   string `json:"cwd"`
		Title string `json:"title"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		helper.BadRequest(c, err)
		return
	}
	s, err := vipanel.OpenHistory(req.ID, req.Cwd, req.Title)
	if err != nil {
		helper.BadRequest(c, err)
		return
	}
	helper.SuccessWithData(c, vipanel.ToItem(s))
}

// ---------------------------------------------------------------------------
// harness 自己的登录
// ---------------------------------------------------------------------------

// @Tags ViPanel
// @Summary agent 登录状态
// @Router /ai/console/auth/status [get]
func (b *BaseApi) GetViAuthStatus(c *gin.Context) {
	helper.SuccessWithData(c, vipanel.AuthStatus(c.DefaultQuery("harness", vipanel.DefaultHarness)))
}

// @Tags ViPanel
// @Summary agent 登出
// @Router /ai/console/auth/logout [post]
func (b *BaseApi) ViAuthLogout(c *gin.Context) {
	if err := vipanel.AuthLogout(c.DefaultQuery("harness", vipanel.DefaultHarness)); err != nil {
		helper.InternalServer(c, err)
		return
	}
	helper.Success(c)
}

// @Tags ViPanel
// @Summary agent 登录（WebSocket）
// @Router /ai/console/auth/login [get]
func (b *BaseApi) WsViAuthLogin(c *gin.Context) {
	if !websocket.IsWebSocketUpgrade(c.Request) {
		helper.Success(c)
		return
	}
	conn, err := upGrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		global.LOG.Errorf("vipanel: websocket 升级失败, err: %v", err)
		return
	}
	defer conn.Close()

	spec, err := vipanel.LoginSpec(
		c.DefaultQuery("harness", vipanel.DefaultHarness),
		c.DefaultQuery("mode", "claudeai"),
	)
	if wshandleError(conn, err) {
		return
	}
	pty, err := vipanel.StartPty(spec)
	if wshandleError(conn, errors.WithMessage(err, "登录进程启动失败")) {
		return
	}
	defer pty.Close()

	bridge := vipanel.NewPtyBridge(conn, pty)
	// 把授权链接从原始输出里挑出来单独推给前端。
	// 服务器上没人看屏幕，链接必须送到用户自己的设备上去打开。
	seen := map[string]bool{}
	bridge.Tap(func(chunk []byte) {
		for _, u := range vipanel.ExtractURLs(chunk) {
			if seen[u] {
				continue
			}
			seen[u] = true
			_ = bridge.Send(gin.H{"type": "auth_url", "url": u})
		}
	})
	bridge.Run()

	_ = bridge.Send(gin.H{"type": "auth_done", "state": vipanel.AuthStatus(
		c.DefaultQuery("harness", vipanel.DefaultHarness))})
}

// @Tags ViPanel
// @Summary 镜像会话的 agent 屏幕
// @Param id query string true "会话 id"
// @Router /ai/console/agent [get]
func (b *BaseApi) WsConsoleAgent(c *gin.Context) {
	if !websocket.IsWebSocketUpgrade(c.Request) {
		helper.Success(c)
		return
	}
	conn, err := upGrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		global.LOG.Errorf("vipanel: websocket 升级失败, err: %v", err)
		return
	}
	defer conn.Close()

	s, ok := vipanel.M().Get(c.Query("id"))
	if !ok {
		wshandleError(conn, errors.New("会话不存在"))
		return
	}
	// 会话可能正休眠着。既然用户要看屏幕，就把它叫醒。
	if _, err := vipanel.M().Activate(s.ID); err != nil {
		wshandleError(conn, err)
		return
	}

	snapshot, ch := s.AttachAgent()
	defer s.DetachAgent(ch)

	var wmu sync.Mutex
	sendCmd := func(b []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		return conn.WriteJSON(gin.H{
			"type": "cmd", "data": base64.StdEncoding.EncodeToString(b),
		})
	}
	if len(snapshot) > 0 {
		if err := sendCmd(snapshot); err != nil {
			return
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var m struct {
				Type string `json:"type"`
				Data string `json:"data"`
			}
			if json.Unmarshal(raw, &m) != nil || m.Type != "cmd" {
				continue // resize 一律忽略：agent 的尺寸是固定的，见 mirror.go
			}
			data, err := base64.StdEncoding.DecodeString(m.Data)
			if err != nil {
				continue
			}
			_ = s.WriteAgent(data)
		}
	}()

	for {
		select {
		case <-done:
			return
		case chunk, ok := <-ch:
			if !ok {
				return
			}
			if err := sendCmd(chunk); err != nil {
				return
			}
		}
	}
}

// @Tags ViPanel
// @Summary PreToolUse hook 的决定入口（仅本机 unix socket）
// @Router /ai/console/hook/decide [post]
func (b *BaseApi) ViHookDecide(c *gin.Context) {
	var req vipanel.PermRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		helper.BadRequest(c, err)
		return
	}
	req.ID = uuid.NewString()

	// 面板操作（mcp__vipanel__*）走自己那套判定：板块授权 + 三档风险 + 台账 + 审计。
	// 不是面板操作时 handled 为 false，行为和以前完全一样。
	if v, handled := vipanel.DecideMCP(req); handled {
		c.JSON(200, v)
		return
	}

	// 这里会阻塞到有人决定或超时。hook 那边是同步等着的。
	c.JSON(200, vipanel.Broker().Ask(req))
}

// @Tags ViPanel
// @Summary MCP 服务端（仅本机 unix socket，且必须是本面板起的会话的后代）
// @Router /ai/console/mcp/ws [get]
func (b *BaseApi) WsConsoleMCP(c *gin.Context) {
	if !websocket.IsWebSocketUpgrade(c.Request) {
		helper.Success(c)
		return
	}
	if !vipanel.MCPEnabled() {
		helper.ErrorWithDetail(c, 403, "ErrForbidden", errors.New("面板操作能力已在设置中关闭"))
		return
	}

	// 血缘校验。**在升级之前**做——升级完再关连接的话，
	// 管子那侧只会看到一个没有理由的断线。
	pid, _ := strconv.Atoi(c.GetHeader("X-VP-Pipe-Pid"))
	sess := vipanel.M().SessionByDescendant(pid)
	if sess == nil {
		global.LOG.Infof("vipanel: 拒绝一个非本面板会话后代的 MCP 连接, pid=%d", pid)
		helper.ErrorWithDetail(c, 403, "ErrForbidden",
			errors.New("这个进程不属于任何 ViPanel 会话"))
		return
	}

	conn, err := upGrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		global.LOG.Errorf("vipanel: MCP websocket 升级失败, err: %v", err)
		return
	}
	defer conn.Close()

	mcp.NewServer(&wsTransport{conn: conn}, sess.ID).Run()
}

// wsTransport 把 gorilla 的连接适配成 MCP 服务端要的收发接口。
//
// 写必须加锁：服务端在回应之外还会**主动**推 notifications/tools/list_changed，
// 两个 goroutine 同时写同一个 websocket 会直接 panic。
type wsTransport struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (t *wsTransport) Recv() ([]byte, error) {
	_, msg, err := t.conn.ReadMessage()
	return msg, err
}

func (t *wsTransport) Send(b []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.conn.WriteMessage(websocket.TextMessage, b)
}

// @Tags ViPanel
// @Summary 面板操作能力（MCP）总开关
// @Router /ai/console/mcp/setting [get]
func (b *BaseApi) GetViMCPSetting(c *gin.Context) {
	helper.SuccessWithData(c, gin.H{
		"enabled":   vipanel.MCPEnabled(),
		"installed": vipanel.MCPInstalled(),
		"modules":   mcp.Modules,
		"opCount":   len(mcp.Catalog),
	})
}

// @Tags ViPanel
// @Summary 开关面板操作能力
// @Router /ai/console/mcp/setting/update [post]
func (b *BaseApi) UpdateViMCPSetting(c *gin.Context) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		helper.BadRequest(c, err)
		return
	}
	vipanel.SetMCPEnabled(req.Enabled)
	helper.SuccessWithData(c, gin.H{"enabled": vipanel.MCPEnabled()})
}

// @Tags ViPanel
// @Summary 浏览器提交权限决定
// @Router /ai/console/permission/resolve [post]
func (b *BaseApi) ViPermissionResolve(c *gin.Context) {
	var req struct {
		ID       string `json:"id"`
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		helper.BadRequest(c, err)
		return
	}
	ok := vipanel.Broker().Resolve(req.ID, vipanel.Verdict{
		Decision: vipanel.Decision(req.Decision), Reason: req.Reason,
	})
	// ok=false 表示这个请求已经被别的设备决定过了。这不是错误——
	// 多设备同时看着时本来就该是「谁先点算谁」。
	helper.SuccessWithData(c, gin.H{"applied": ok})
}

// @Tags ViPanel
// @Summary 调整会话的运行时行为（模式 / 模型 / effort）
// @Router /ai/console/sessions/control [post]
func (b *BaseApi) ControlViSession(c *gin.Context) {
	var req struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		helper.BadRequest(c, err)
		return
	}
	s, ok := vipanel.M().Get(req.ID)
	if !ok {
		helper.BadRequest(c, errors.New("会话不存在"))
		return
	}
	if err := s.Control(req.Kind, req.Value); err != nil {
		helper.BadRequest(c, err)
		return
	}
	helper.Success(c)
}
