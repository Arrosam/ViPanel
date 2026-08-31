package global

import "os"

// ViPanel 自己的版本与发布源。
//
// 存在的原因是一个真实的危险：上游的「检查更新」查的是飞致云的发布服务器，
// 点下去会下载 1panel-<ver>-linux-<arch>.tar.gz 覆盖二进制——
// 那等于把 ViPanel 整个换回上游 1Panel，控制台、MCP、钩子、去品牌化全部消失。
// 版本号显示错只是这件事露出来的那一角。

// Version 是 ViPanel 的版本，发布时用 -ldflags "-X ...global.Version=v0.1.0" 注入。
//
// 默认 "dev"：**没有注入版本就说自己是 dev**，不要冒充一个像模像样的版本号。
// 自称 v2.0.0 的开发构建会让更新检查得出毫无意义的结论。
//
// 注意别用 `git describe` 去生成它：这个仓库是 1Panel 的分支，还没有自己的
// tag 时它会落回上游的 v2.2.4，等于又把 ViPanel 说成一个 1Panel 版本。
// 没发版之前一律 dev-<sha>，判断时按 "dev" 前缀识别。
var Version = "dev"

// Repo 是发布仓库，和 scripts/get.sh 里的 REPO 必须是同一个值。
// 允许环境变量覆盖，方便在 fork 或私有部署里改。
func Repo() string {
	if v := os.Getenv("VIPANEL_REPO"); v != "" {
		return v
	}
	return "Arrosam/ViPanel"
}

// ReleasesAPI 是查最新版本用的地址。
func ReleasesAPI() string {
	if v := os.Getenv("VIPANEL_API_URL"); v != "" {
		return v
	}
	return "https://api.github.com/repos/" + Repo() + "/releases/latest"
}

// ReleasesPage 是给人看的发布页。
func ReleasesPage() string {
	return "https://github.com/" + Repo() + "/releases"
}
