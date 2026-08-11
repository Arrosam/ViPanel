package vipanel

import (
	"path/filepath"

	"github.com/1Panel-dev/1Panel/agent/app/dto"
)

func toCaps(c Capabilities) dto.ViCaps {
	// 前端拿到 null 会在 v-for 上炸，统一给空数组
	models := c.Models
	if models == nil {
		models = []string{}
	}
	efforts := c.EffortLevels
	if efforts == nil {
		efforts = []string{}
	}
	cmds := c.Commands
	if cmds == nil {
		cmds = []Command{}
	}
	modes := c.LoginModes
	if modes == nil {
		modes = []LoginMode{}
	}
	return dto.ViCaps{
		StructuredEvents: c.StructuredEvents,
		Resume:           c.Resume,
		Interrupt:        c.Interrupt,
		Auth:             c.Auth,
		Models:           models,
		EffortLevels:     efforts,
		Commands:         cmds,
		LoginModes:       modes,
	}
}

func ToItem(s *Session) dto.ViSessionItem {
	dir := filepath.Base(s.Cwd)
	if dir == "" || dir == "." {
		dir = s.Cwd
	}
	return dto.ViSessionItem{
		ID:           s.ID,
		Title:        s.Title,
		Cwd:          s.Cwd,
		Dir:          dir,
		Harness:      s.Harness.ID(),
		Status:       string(s.Status()),
		Alive:        s.Alive(),
		LastUsed:     s.LastUsed(),
		Notes:        s.Notes(),
		Mode:         s.Mode(),
		Capabilities: toCaps(s.Harness.Capabilities()),
	}
}

func ListItems() []dto.ViSessionItem {
	all := M().All()
	out := make([]dto.ViSessionItem, 0, len(all))
	for _, s := range all {
		out = append(out, ToItem(s))
	}
	return out
}

func Harnesses() []dto.ViHarnessItem {
	hs := List()
	out := make([]dto.ViHarnessItem, 0, len(hs))
	for _, h := range hs {
		plan := h.InstallPlan()
		info := dto.ViInstallInfo{Installable: plan.Installable, Note: plan.Note}
		// 只在「装不了」的时候才去查前置依赖：已经装好的 harness
		// 报缺 npm 是纯噪音，而且每次都要跑几次 LookPath。
		if plan.Installable && !Installed(h) {
			for _, r := range MissingPrereqs(h) {
				info.Missing = append(info.Missing, dto.ViPrereq{Binary: r.Binary, Hint: r.Hint})
			}
		}
		out = append(out, dto.ViHarnessItem{
			ID: h.ID(), DisplayName: h.DisplayName(),
			Installed: Installed(h), Install: info, Caps: toCaps(h.Capabilities()),
		})
	}
	return out
}

func PoolInfo() dto.ViPoolInfo {
	all := M().All()
	order := []string{}
	for _, s := range all {
		if s.Alive() {
			order = append(order, s.ID)
		}
	}
	return dto.ViPoolInfo{Size: M().PoolSize(), Active: len(order), Order: order}
}
