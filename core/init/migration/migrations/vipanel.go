package migrations

import (
	"github.com/1Panel-dev/1Panel/core/app/dto"
	"github.com/1Panel-dev/1Panel/core/init/migration/helper"
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// ViPanel 的会话面板。
//
// 侧边栏不是直接从前端路由表渲染的：可见性由 settings 表里 `HideMenu` 存的那棵菜单树
// 决定（Sidebar/index.vue 的 buildVisibleMenu 要求 showSet.has(name)）。
// 新增路由必须同时往那棵树里塞一条，否则页面能直接访问但菜单里看不见。
var AddViPanelConsoleMenu = &gormigrate.Migration{
	ID: "20260801-add-vipanel-console-menu",
	Migrate: func(tx *gorm.DB) error {
		return helper.UpsertChildMenuByLabel(tx, "AI-Menu", dto.ShowMenu{
			ID:       "60",
			Disabled: false,
			Title:    "aiTools.console.console",
			IsShow:   true,
			Label:    "Console",
			Path:     "/ai/console",
			Sort:     40,
		}, "")
	},
}

// 面板名默认改成 ViPanel。
//
// 这个值是运行时设置（settings.PanelName），标签页标题和界面上的产品名都读它。
// 默认值由上游的 init.go 播种成 "1Panel"——那是上游文件，改它会多一处 rebase 冲突，
// 而且用户自己改过名的实例也会被覆盖。
//
// 所以走一条自己的迁移，并且**只在它还是上游默认值时才动**：
// 用户重命名过的实例原样保留。
//
// 这不只是审美问题：飞致云的社区协议禁止未经书面许可使用其产品名，
// 一个分发版顶着「1Panel」这个名字对外提供服务，正是那条禁令指向的情形。
var RenamePanelToViPanel = &gormigrate.Migration{
	ID: "20260810-rename-panel-to-vipanel",
	Migrate: func(tx *gorm.DB) error {
		return tx.Exec(
			"UPDATE settings SET value = ? WHERE key = ? AND value = ?",
			"ViPanel", "PanelName", "1Panel",
		).Error
	},
}
