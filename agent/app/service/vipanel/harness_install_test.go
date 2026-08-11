package vipanel

import (
	"strings"
	"testing"
)

// 每个注册的 harness 都必须给出一个自洽的安装方案。
//
// 这条是 InstallPlan 进主接口的意义所在：加一个新 harness 时，
// 编译器逼你写这个方法，这个测试逼你写得有意义。
func TestEveryHarnessHasCoherentInstallPlan(t *testing.T) {
	for _, h := range List() {
		plan := h.InstallPlan()
		if !plan.Installable {
			// 装不了也要说清为什么，否则界面上就是一个没有解释的灰按钮
			if strings.TrimSpace(plan.Note) == "" {
				t.Errorf("%s: Installable=false 但没给 Note", h.ID())
			}
			if plan.Spec.File != "" {
				t.Errorf("%s: 声明装不了却带了安装命令 %q", h.ID(), plan.Spec.File)
			}
			continue
		}
		if plan.Spec.File == "" {
			t.Errorf("%s: 声明可安装但没有安装命令", h.ID())
		}
		if strings.TrimSpace(plan.Note) == "" {
			t.Errorf("%s: 可安装但没说明装的是什么", h.ID())
		}
		// 安装命令本身也是个可执行文件。它如果不在前置依赖里，
		// 缺它时的表现就是伪终端里一句 "executable file not found"，
		// 而不是面板给出的那句人话。
		found := false
		for _, r := range plan.Requires {
			if r.Binary == plan.Spec.File {
				found = true
			}
			if strings.TrimSpace(r.Hint) == "" {
				t.Errorf("%s: 前置依赖 %s 没有给出补救提示", h.ID(), r.Binary)
			}
		}
		if !found {
			t.Errorf("%s: 安装命令 %q 不在前置依赖里", h.ID(), plan.Spec.File)
		}
	}
}

// shell 是这套抽象的证伪件：它必须回答「不用装」，而不是假装能装。
func TestShellIsNotInstallable(t *testing.T) {
	if Get("shell").InstallPlan().Installable {
		t.Fatal("shell 是系统自带的，不该声明可安装")
	}
}

func TestClaudeAndCodexInstallViaNpm(t *testing.T) {
	for id, pkg := range map[string]string{
		"claude-code": "@anthropic-ai/claude-code",
		"codex":       "@openai/codex",
	} {
		plan := Get(id).InstallPlan()
		if plan.Spec.File != "npm" {
			t.Errorf("%s: 安装命令是 %q，预期 npm", id, plan.Spec.File)
		}
		joined := strings.Join(plan.Spec.Args, " ")
		if !strings.Contains(joined, "-g") || !strings.Contains(joined, pkg) {
			t.Errorf("%s: 参数不对：%s", id, joined)
		}
	}
}

// 已经装好的 harness 不能再走安装流程——重复 npm install 不致命，
// 但它会让「安装」按钮在装完之后仍然可点，界面就在撒谎。
func TestInstallSpecRefusesWhenNotInstallable(t *testing.T) {
	if _, err := InstallSpec("shell"); err == nil {
		t.Error("shell 不该给出安装命令")
	}
	if _, err := InstallSpec("根本不存在的-harness"); err == nil {
		t.Error("未知 harness 应当报错")
	}
}
