package v2

import (
	"os"
	"strconv"

	"github.com/1Panel-dev/1Panel/agent/app/api/v2/helper"
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
