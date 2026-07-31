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
