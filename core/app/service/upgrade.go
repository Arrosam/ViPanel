package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/1Panel-dev/1Panel/core/app/dto"
	"github.com/1Panel-dev/1Panel/core/app/repo"
	"github.com/1Panel-dev/1Panel/core/buserr"
	"github.com/1Panel-dev/1Panel/core/constant"
	"github.com/1Panel-dev/1Panel/core/global"
	"github.com/1Panel-dev/1Panel/core/utils/cmd"
	"github.com/1Panel-dev/1Panel/core/utils/common"
	"github.com/1Panel-dev/1Panel/core/utils/controller"
	"github.com/1Panel-dev/1Panel/core/utils/files"
	"github.com/1Panel-dev/1Panel/core/utils/req_helper"
	upgradeUtil "github.com/1Panel-dev/1Panel/core/utils/upgrade"
)

type serviceInfo struct {
	basePath     string
	coreName     string
	agentName    string
	selCoreName  string
	selAgentName string
}

const minUpgradeFreeSpace = 500 << 20 // 500MB

func loadServiceInfo() (serviceInfo, error) {
	basePath, err := controller.GetServicePath("")
	if err != nil {
		global.LOG.Errorf("get service path failed: %v", err)
		return serviceInfo{}, err
	}
	coreName, err := controller.LoadServiceName("1panel-core")
	if err != nil {
		global.LOG.Errorf("load core service name failed: %v", err)
		return serviceInfo{}, err
	}
	agentName, err := controller.LoadServiceName("1panel-agent")
	if err != nil {
		global.LOG.Errorf("load agent service name failed: %v", err)
		return serviceInfo{}, err
	}
	selCoreName, err := controller.SelectInitScript("1panel-core")
	if err != nil {
		global.LOG.Errorf("select core init script failed: %v", err)
		return serviceInfo{}, err
	}
	selAgentName, err := controller.SelectInitScript("1panel-agent")
	if err != nil {
		global.LOG.Errorf("select agent init script failed: %v", err)
		return serviceInfo{}, err
	}
	return serviceInfo{
		basePath:     basePath,
		coreName:     coreName,
		agentName:    agentName,
		selCoreName:  selCoreName,
		selAgentName: selAgentName,
	}, nil
}

type UpgradeService struct{}

type IUpgradeService interface {
	Upgrade(req dto.Upgrade) error
	Rollback(req dto.OperateByID) error
	LoadNotes(req dto.Upgrade) (string, error)
	SearchUpgrade() (*dto.UpgradeInfo, error)
	LoadRelease() ([]dto.ReleasesNotes, error)
}

func NewIUpgradeService() IUpgradeService {
	return &UpgradeService{}
}

// SearchUpgrade 查 **ViPanel 自己**的最新版本。
//
// 上游这里查的是飞致云的发布服务器，比较的是 1Panel 的版本线。对一个改动版
// 来说那个结论没有意义，而且它引出的「更新」动作会把 ViPanel 覆盖成 1Panel
// （见下面的 Upgrade）。所以这里换成查我们自己的发布仓库。
//
// 查不到时返回空而不是报错：仓库还没发过版、机器连不上 GitHub、离线部署，
// 这些都不是故障，只是「没有可更新的版本」。把它们变成红色报错只会让人
// 以为面板坏了。
func (u *UpgradeService) SearchUpgrade() (*dto.UpgradeInfo, error) {
	var upgrade dto.UpgradeInfo
	if global.CONF.Base.IsOffline {
		return &upgrade, nil
	}

	latest, notes := loadViPanelLatest()
	if latest == "" || latest == global.Version {
		return &upgrade, nil
	}
	// 开发构建不参与版本比较：dev 和任何发布版比都是「有新版」，那是噪音。
	if global.Version == "dev" {
		return &upgrade, nil
	}
	upgrade.LatestVersion = latest
	upgrade.ReleaseNote = notes
	return &upgrade, nil
}

// loadViPanelLatest 从发布仓库读最新的 tag 和发布说明。
func loadViPanelLatest() (string, string) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, global.ReleasesAPI(), nil)
	if err != nil {
		return "", ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		global.LOG.Debugf("vipanel: 查更新失败（不影响使用）: %v", err)
		return "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ""
	}
	var r struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
		Draft   bool   `json:"draft"`
		Pre     bool   `json:"prerelease"`
	}
	if json.NewDecoder(resp.Body).Decode(&r) != nil || r.Draft || r.Pre {
		return "", ""
	}
	return r.TagName, r.Body
}

// LoadNotes 直接返回发布仓库里那份说明。
// 不再去拼 1panel-<ver>-release-notes 那个地址——那是上游的发布物。
func (u *UpgradeService) LoadNotes(dto.Upgrade) (string, error) {
	_, notes := loadViPanelLatest()
	return notes, nil
}

// Upgrade 在 ViPanel 里**有意不做原地升级**。
//
// 上游这个实现会从飞致云的服务器下载 1panel-<ver>-linux-<arch>.tar.gz
// 并替换二进制。对 ViPanel 来说那不是升级，是把整个改动版覆盖掉——
// 控制台、MCP、权限钩子、去品牌化全部消失，而用户点的是一个写着「更新」的按钮。
//
// 我们自己的产物布局和它不同（vipanel-<ver>-linux-<arch>.tar.gz），
// 而且已经有一条**验证过的**安装路径：scripts/get.sh，带 sha256 校验。
// 与其现写一套没测过的原地替换（失败的后果是面板起不来），
// 不如如实告诉用户走那条路。
func (u *UpgradeService) Upgrade(req dto.Upgrade) error {
	return fmt.Errorf("ViPanel 不支持面板内原地升级。请在服务器上执行：curl -fsSL https://raw.githubusercontent.com/%s/main/scripts/get.sh | sh  （发布页：%s）",
		global.Repo(), global.ReleasesPage())
}

func (u *UpgradeService) Rollback(req dto.OperateByID) error {
	log, _ := upgradeLogRepo.Get(repo.WithByID(req.ID))
	if log.ID == 0 {
		return buserr.New("ErrRecordNotFound")
	}
	svcInfo, err := loadServiceInfo()
	if err != nil {
		return err
	}
	u.handleRollback(log.BackupFile, 3, svcInfo)
	return nil
}

type noteHelper struct {
	Docs []noteDetailHelper `json:"docs"`
}
type noteDetailHelper struct {
	Location string `json:"location"`
	Text     string `json:"text"`
	Title    string `json:"title"`
}

func (u *UpgradeService) LoadRelease() ([]dto.ReleasesNotes, error) {
	docSource, _ := settingRepo.GetValueByKey("DocSource")
	lang, _ := settingRepo.GetValueByKey("Language")
	var notes []dto.ReleasesNotes
	url := "https://1panel.cn/docs/v2/search/search_index.json"
	useIntlDocs := false
	lang = strings.ToLower(strings.TrimSpace(lang))
	if docSource == "withByRegion" {
		useIntlDocs = global.CONF.Base.Edition == "intl"
	} else {
		useIntlDocs = lang != "zh"
	}
	if useIntlDocs {
		url = "https://docs.1panel.pro/v2/search/search_index.json"
	}
	resp, err := req_helper.HandleGet(url)
	if err != nil {
		return notes, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return notes, err
	}
	var nodeItem noteHelper
	if err := json.Unmarshal(body, &nodeItem); err != nil {
		return notes, err
	}
	for _, item := range nodeItem.Docs {
		if !strings.HasPrefix(item.Location, "changelog/#v") {
			continue
		}
		itemNote := analyzeDoc(item.Title, item.Text)
		if len(itemNote.CreatedAt) != 0 {
			notes = append(notes, analyzeDoc(item.Title, item.Text))
		}
	}

	return notes, nil
}

func analyzeDoc(version, content string) dto.ReleasesNotes {
	var item dto.ReleasesNotes
	parts := strings.Split(content, "<p>")
	if len(parts) < 3 {
		return item
	}
	item.CreatedAt = strings.ReplaceAll(strings.TrimSpace(parts[1]), "</p>", "")
	for i := 1; i < len(parts); i++ {
		if strings.Contains(parts[i], "问题修复") || strings.Contains(parts[i], "Bug Fixes") {
			item.FixCount = strings.Count(parts[i], "<li>")
		}
		if strings.Contains(parts[i], "新增功能") || strings.Contains(parts[i], "New Features") {
			item.NewCount = strings.Count(parts[i], "<li>")
		}
		if strings.Contains(parts[i], "功能优化") || strings.Contains(parts[i], "Improvements") {
			item.OptimizationCount = strings.Count(parts[i], "<li>")
		}
	}
	item.Content = strings.Replace(content, fmt.Sprintf("<p>%s</p>", item.CreatedAt), "", 1)
	item.Version = version
	return item
}

func checkUpgradeSpace() error {
	dir := global.CONF.Base.InstallDir
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return err
	}
	avail := stat.Bavail * uint64(stat.Bsize)
	if avail < minUpgradeFreeSpace {
		return fmt.Errorf("available space of %s is %d MB, less than required 500MB", dir, avail>>20)
	}
	return nil
}

func (u *UpgradeService) handleBackup(originalDir string, svcInfo serviceInfo) error {
	if err := files.CopyItem(false, true, "/usr/local/bin/1panel-core", originalDir); err != nil {
		return err
	}
	if err := files.CopyItem(false, true, "/usr/local/bin/1panel-agent", originalDir); err != nil {
		return err
	}
	if err := files.CopyItem(false, true, "/usr/local/bin/1pctl", originalDir); err != nil {
		return err
	}
	if err := files.CopyItem(true, true, "/usr/local/bin/lang", originalDir); err != nil {
		return err
	}
	if err := files.CopyItem(false, true, path.Join(svcInfo.basePath, svcInfo.coreName), originalDir); err != nil {
		return err
	}
	if err := files.CopyItem(false, true, path.Join(svcInfo.basePath, svcInfo.agentName), originalDir); err != nil {
		return err
	}
	if err := files.CopyItem(true, true, path.Join(global.CONF.Base.InstallDir, "1panel/db"), originalDir); err != nil {
		return err
	}
	if err := files.CopyItem(false, true, path.Join(global.CONF.Base.InstallDir, "1panel/geo/GeoIP.mmdb"), originalDir); err != nil {
		return err
	}
	return nil
}

func (u *UpgradeService) handleRollback(originalDir string, errStep int, svcInfo serviceInfo) {
	_ = settingRepo.Update("SystemStatus", "Free")
	dbPath := path.Join(global.CONF.Base.InstallDir, "1panel")
	if _, err := os.Stat(path.Join(originalDir, "db")); err == nil {
		if err := files.CopyItem(true, true, path.Join(originalDir, "db"), dbPath); err != nil {
			global.LOG.Errorf("rollback 1panel db failed, err: %v", err)
		}
	}
	if err := files.CopyFileWithRename(path.Join(originalDir, "1panel-core"), "/usr/local/bin/1panel-core"); err != nil {
		global.LOG.Errorf("rollback 1panel-core failed, err: %v", err)
	}
	if err := files.CopyFileWithRename(path.Join(originalDir, "1panel-agent"), "/usr/local/bin/1panel-agent"); err != nil {
		global.LOG.Errorf("rollback 1panel-agent failed, err: %v", err)
	}
	if errStep == 1 {
		return
	}
	if err := files.CopyFileWithRename(path.Join(originalDir, "1pctl"), "/usr/local/bin/1pctl"); err != nil {
		global.LOG.Errorf("rollback 1pctl failed, err: %v", err)
	}
	if errStep == 2 {
		return
	}
	if err := files.CopyFileWithRename(path.Join(originalDir, svcInfo.coreName), path.Join(svcInfo.basePath, svcInfo.coreName)); err != nil {
		global.LOG.Errorf("rollback %s failed, err: %v", svcInfo.coreName, err)
	}
	if err := files.CopyFileWithRename(path.Join(originalDir, svcInfo.agentName), path.Join(svcInfo.basePath, svcInfo.agentName)); err != nil {
		global.LOG.Errorf("rollback %s failed, err: %v", svcInfo.agentName, err)
	}
	if errStep == 3 {
		return
	}
	if err := files.CopyItem(true, true, path.Join(originalDir, "lang"), "/usr/local/bin"); err != nil {
		global.LOG.Errorf("rollback language files failed, err: %v", err)
	}
	if err := files.CopyFileWithRename(path.Join(originalDir, "GeoIP.mmdb"), path.Join(global.CONF.Base.InstallDir, "1panel/geo/GeoIP.mmdb")); err != nil {
		global.LOG.Errorf("rollback GeoIP database failed, err: %v", err)
	}
}

func (u *UpgradeService) loadVersionByMode(developer, currentVersion string) (string, string, string) {
	var current, latest string
	if global.CONF.Base.Mode == "dev" {
		devVersionLatest := u.loadVersion(true, currentVersion, "dev")
		return devVersionLatest, "", ""
	}

	betaVersionLatest := ""
	latest = u.loadVersion(true, currentVersion, "stable")
	current = u.loadVersion(false, currentVersion, "stable")
	if developer == constant.StatusEnable {
		betaVersionLatest = u.loadVersion(true, currentVersion, "beta")
	}
	if current != latest {
		return betaVersionLatest, current, latest
	}

	versionPart := strings.Split(current, ".")
	if len(versionPart) < 3 {
		return betaVersionLatest, "", latest
	}
	num, _ := strconv.Atoi(versionPart[1])
	if num == 0 {
		return betaVersionLatest, "", latest
	}
	if num >= 10 {
		if current[:6] == currentVersion[:6] {
			return betaVersionLatest, current, ""
		}
		return betaVersionLatest, "", latest
	}
	if current[:5] == currentVersion[:5] {
		return betaVersionLatest, "", ""
	}
	return betaVersionLatest, "", latest
}

func (u *UpgradeService) loadVersion(isLatest bool, currentVersion, mode string) string {
	path := fmt.Sprintf("%s/%s/latest", global.RepoURL(), mode)
	if !isLatest {
		path = fmt.Sprintf("%s/%s/latest.current", global.RepoURL(), mode)
	}
	_, latestVersionRes, err := req_helper.HandleRequestWithProxy(path, http.MethodGet, constant.TimeOut20s)
	if err != nil {
		global.LOG.Errorf("load latest version from oss failed, err: %v", err)
		return ""
	}
	version := string(latestVersionRes)
	if strings.Contains(version, "<") {
		global.LOG.Errorf("load latest version from oss failed, err: %v", version)
		return ""
	}
	if isLatest {
		return u.checkVersion(version, currentVersion)
	}

	versionMap := make(map[string]string)
	if err := json.Unmarshal(latestVersionRes, &versionMap); err != nil {
		global.LOG.Errorf("load latest version from oss failed (error unmarshal), err: %v", err)
		return ""
	}

	versionPart := strings.Split(currentVersion, ".")
	if len(versionPart) < 3 {
		global.LOG.Errorf("current version is error format: %s", currentVersion)
		return ""
	}
	num, _ := strconv.Atoi(versionPart[1])
	if num >= 10 {
		if version, ok := versionMap[currentVersion[0:5]]; ok {
			return u.checkVersion(version, currentVersion)
		}
		return ""
	}
	if version, ok := versionMap[currentVersion[0:4]]; ok {
		return u.checkVersion(version, currentVersion)
	}
	return ""
}

func (u *UpgradeService) checkVersion(v2, v1 string) string {
	addSuffix := false
	if !strings.Contains(v1, "-") {
		v1 = v1 + "-lts"
	}
	if !strings.Contains(v2, "-") {
		addSuffix = true
		v2 = v2 + "-lts"
	}
	if common.ComparePanelVersion(v2, v1) {
		if addSuffix {
			return strings.TrimSuffix(v2, "-lts")
		}
		return v2
	}
	return ""
}

func (u *UpgradeService) loadReleaseNotes(path string) (string, error) {
	_, releaseNotes, err := req_helper.HandleRequestWithProxy(path, http.MethodGet, constant.TimeOut20s)
	if err != nil {
		return "", err
	}
	return string(releaseNotes), nil
}

func loadArch() (string, error) {
	std, err := cmd.NewCommandMgr().RunWithStdout("uname", "-a")
	if err != nil {
		return "", fmt.Errorf("std: %s, err: %s", std, err.Error())
	}
	if strings.Contains(std, "x86_64") {
		return "amd64", nil
	}
	if strings.Contains(std, "arm64") || strings.Contains(std, "aarch64") {
		return "arm64", nil
	}
	if strings.Contains(std, "armv7l") {
		return "armv7", nil
	}
	if strings.Contains(std, "ppc64le") {
		return "ppc64le", nil
	}
	if strings.Contains(std, "s390x") {
		return "s390x", nil
	}
	if strings.Contains(std, "riscv64") {
		return "riscv64", nil
	}
	return "", fmt.Errorf("unsupported such arch: %s", std)
}

func dropBackupCopies() {
	backupCopies, _ := settingRepo.GetValueByKey("UpgradeBackupCopies")
	if err := upgradeUtil.DropBackupCopies(global.CONF.Base.InstallDir, backupCopies); err != nil {
		global.LOG.Errorf("read upgrade dir failed, err: %v", err)
	}
}
