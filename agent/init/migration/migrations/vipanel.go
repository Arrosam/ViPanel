package migrations

import (
	"github.com/1Panel-dev/1Panel/agent/app/model"
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

var AddViSessionTable = &gormigrate.Migration{
	ID: "20260801-add-vi-session",
	Migrate: func(tx *gorm.DB) error {
		return tx.AutoMigrate(&model.ViSession{})
	},
}

// 加 title_pinned 列。
// AutoMigrate 只在 AddViSessionTable 那条迁移里跑过一次，新增字段不会自动补上，
// 所以要单开一条——gormigrate 是按 ID 记账的。
var AddViSessionTitlePinned = &gormigrate.Migration{
	ID: "20260810-add-vi-session-title-pinned",
	Migrate: func(tx *gorm.DB) error {
		return tx.AutoMigrate(&model.ViSession{})
	},
}
