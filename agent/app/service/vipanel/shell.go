package vipanel

import (
	"os"
	"os/exec"
)

// LoginShell 挑一个能用的交互式 shell。
//
// 顺序：$SHELL → bash → sh。最小镜像里常常只有 sh，直接写死 bash 会让
// 终端起不来，而这个失败在前端看只是「连不上」，很难查。
func LoginShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		if _, err := os.Stat(s); err == nil {
			return s
		}
	}
	for _, name := range []string{"bash", "sh"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return "/bin/sh"
}
