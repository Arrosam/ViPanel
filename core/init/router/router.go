package router

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	appauth "github.com/1Panel-dev/1Panel/core/app/auth"
	"github.com/1Panel-dev/1Panel/core/app/service"
	"github.com/1Panel-dev/1Panel/core/cmd/server/docs"
	"github.com/1Panel-dev/1Panel/core/cmd/server/web"
	"github.com/1Panel-dev/1Panel/core/global"
	"github.com/1Panel-dev/1Panel/core/i18n"
	"github.com/1Panel-dev/1Panel/core/init/swagger"
	"github.com/1Panel-dev/1Panel/core/middleware"
	rou "github.com/1Panel-dev/1Panel/core/router"
	"github.com/1Panel-dev/1Panel/core/utils/security"
	"github.com/1Panel-dev/1Panel/core/utils/xpack"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

var (
	Router *gin.Engine
)

func setWebStatic(rootRouter *gin.RouterGroup) {
	rootRouter.StaticFS("/public", http.FS(web.Favicon))
	// /favicon.ico 必须直接返回图标本身。
	//
	// 上游这里用的是 StaticFS，等于把内嵌 FS 当成**目录**挂在这个路径上：
	// 浏览器自动请求 /favicon.ico 时拿到的是一个 301，跟过去是一张列着
	// favicon.png 的目录列表 HTML——不是图标。页面里的 <link> 指向
	// /public/favicon.png 所以看不出来，但任何不读 link 的场景（书签、
	// 部分浏览器的默认请求）都拿不到图标。
	rootRouter.GET("/favicon.ico", func(c *gin.Context) {
		data, err := web.Favicon.ReadFile("favicon.png")
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Cache-Control", "public, max-age=86400")
		c.Data(http.StatusOK, "image/png", data)
	})
	RegisterImages(rootRouter)
	setStaticResource(rootRouter)
	rootRouter.GET("/assets/*filepath", func(c *gin.Context) {
		c.Writer.Header().Set("Cache-Control", "private, max-age=2628000, immutable")
		if c.Request.URL.Path[len(c.Request.URL.Path)-1] == '/' {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		staticServer := http.FileServer(http.FS(web.Assets))
		staticServer.ServeHTTP(c.Writer, c.Request)
	})
	authService := service.NewIAuthService()
	entrance := authService.GetSecurityEntrance()
	if entrance != "" {
		rootRouter.GET("/"+entrance, func(c *gin.Context) {
			currentEntrance := authService.GetSecurityEntrance()
			if currentEntrance != entrance {
				security.HandleNotSecurity(c, "")
				return
			}
			security.ToIndexHtml(c)
		})
	}
	rootRouter.GET("/", func(c *gin.Context) {
		if !security.CheckSecurity(c) {
			return
		}
		entrance = authService.GetSecurityEntrance()
		if entrance != "" {
			appauth.SetSecurityEntranceCookie(c, entrance)
		}
		staticServer := http.FileServer(http.FS(web.IndexHtml))
		staticServer.ServeHTTP(c.Writer, c.Request)
	})
}

func Routers() *gin.Engine {
	Router = gin.New()
	Router.Use(i18n.UseI18n())
	Router.Use(middleware.WhiteAllow())
	Router.Use(middleware.BindDomain())

	swaggerRouter := Router.Group("1panel")
	docs.SwaggerInfo.BasePath = "/api/v2"
	swaggerRouter.Use(middleware.SessionAuth()).GET("/swagger/*any", swagger.SwaggerHandler())

	PublicGroup := Router.Group("")
	{
		PublicGroup.Use(gzip.Gzip(gzip.DefaultCompression))
		setWebStatic(PublicGroup)
	}
	if global.CONF.Base.IsDemo {
		Router.Use(middleware.DemoHandle())
	}

	Router.Use(middleware.FrontendFallback())
	Router.Use(middleware.OperationLog())
	Router.Use(middleware.GlobalLoading())
	Router.Use(xpack.AuthProvider.CoreAPIAuthMiddleware())
	Router.Use(middleware.PasswordExpired())
	Router.Use(middleware.CSRFTokenGuard())
	Router.Use(xpack.AuthProvider.CoreRBACMiddlewares()...)
	Router.Use(Proxy())

	PrivateGroup := Router.Group("/api/v2/core")
	PrivateGroup.Use(middleware.SetPasswordPublicKey())
	for _, router := range rou.RouterGroupApp {
		router.InitRouter(PrivateGroup)
	}

	Router.NoRoute(func(c *gin.Context) {
		if !security.HandleNotRoute(c) {
			return
		}
		security.HandleNotSecurity(c, "")
	})

	return Router
}

func RegisterImages(rootRouter *gin.RouterGroup) {
	staticDir := filepath.Join(global.CONF.Base.InstallDir, "1panel/uploads/theme")
	rootRouter.GET("/api/v2/images/*filename", func(c *gin.Context) {
		fileName := filepath.Base(c.Param("filename"))
		filePath := filepath.Join(staticDir, fileName)
		if !strings.HasPrefix(filePath, staticDir) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		f, err := os.Open(filePath)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		defer f.Close()
		buf := make([]byte, 512)
		n, _ := f.Read(buf)
		content := buf[:n]
		mimeType := http.DetectContentType(buf[:n])
		if strings.Contains(string(content), "<svg") {
			mimeType = "image/svg+xml"
		}
		_, _ = f.Seek(0, io.SeekStart)
		c.Header("Content-Type", mimeType)
		_, _ = io.Copy(c.Writer, f)
	})
}

func setStaticResource(rootRouter *gin.RouterGroup) {
	rootRouter.GET("/api/v2/static/*filename", func(c *gin.Context) {
		c.Writer.Header().Set("Cache-Control", "private, max-age=2628000")
		filename := c.Param("filename")
		filePath := "static" + filename
		data, err := web.Static.ReadFile(filePath)
		if err != nil {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		sum := sha256.Sum256(data)
		etag := `"` + hex.EncodeToString(sum[:]) + `"`
		c.Writer.Header().Set("ETag", etag)
		if c.GetHeader("If-None-Match") == etag {
			c.AbortWithStatus(http.StatusNotModified)
			return
		}
		c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		c.Writer.Write(data)
	})
}
