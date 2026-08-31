package service

import (
	"strings"
	"testing"

	"github.com/1Panel-dev/1Panel/core/app/dto"
	"github.com/1Panel-dev/1Panel/core/global"
)

// 更新相关的地址**一个都不能指向上游**。
//
// 这条是真实危险的护栏：上游的 Upgrade 会下载 1panel-<ver>-linux-<arch>.tar.gz
// 覆盖二进制，等于把 ViPanel 换回 1Panel，控制台、MCP、钩子、去品牌化全没。
func TestUpdateSourcesAreNotUpstream(t *testing.T) {
	for _, u := range []string{global.ReleasesAPI(), global.ReleasesPage()} {
		low := strings.ToLower(u)
		for _, bad := range []string{"fit2cloud.com", "1panel.cn", "1panel.pro", "resource.1panel"} {
			if strings.Contains(low, bad) {
				t.Errorf("更新地址仍指向上游 %q: %s", bad, u)
			}
		}
		if !strings.Contains(low, strings.ToLower(global.Repo())) {
			t.Errorf("更新地址没有指向本项目仓库: %s", u)
		}
	}
}

// 原地升级必须明确拒绝，并给出可照着敲的替代命令。
// 静默失败或半成功的后果是面板起不来。
func TestUpgradeRefusesInPlace(t *testing.T) {
	err := NewIUpgradeService().Upgrade(dto.Upgrade{Version: "v0.0.1"})
	if err == nil {
		t.Fatal("原地升级必须被拒绝")
	}
	if !strings.Contains(err.Error(), "get.sh") {
		t.Errorf("拒绝时要告诉用户怎么升级，实际: %v", err)
	}
	if strings.Contains(err.Error(), "1panel-") {
		t.Errorf("提示里不该出现上游的产物名: %v", err)
	}
}

// 开发构建不该自称一个像模像样的版本号。
func TestDefaultVersionIsDev(t *testing.T) {
	if global.Version != "dev" && !strings.HasPrefix(global.Version, "v") {
		t.Errorf("版本要么是 dev，要么是 v 开头的发布版，实际: %q", global.Version)
	}
}
