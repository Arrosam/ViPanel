package v2

import (
	"os"
	"strconv"

	"github.com/1Panel-dev/1Panel/agent/app/api/v2/helper"
	"github.com/1Panel-dev/1Panel/agent/app/dto"
	"github.com/1Panel-dev/1Panel/agent/app/service/vipanel"
	"github.com/1Panel-dev/1Panel/agent/global"
	"github.com/gin-gonic/gin"
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
