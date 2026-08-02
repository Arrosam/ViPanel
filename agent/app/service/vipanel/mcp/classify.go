package mcp

import "strings"

// Class 是钩子那侧对一个工具名的判定结果。
//
// 判定放在 mcp 包里、决定放在 vipanel 包里，是为了不出现循环依赖：
// mcp 只提供目录和闸门状态（纯数据），vipanel 拥有权限代理并 import mcp。
// 反过来 mcp 不 import vipanel，所以 tools/call 那条路上不需要任何跨包回调——
// 到 MCP 服务端看到调用的时候，钩子早就决定完并把凭据写进台账了。
type Class struct {
	// IsVipanel 为 false 时，这个工具和 MCP 无关，走原来的逻辑。
	IsVipanel bool
	// Meta 是板块入口和内建工具（<板块>_tools / overview / task）。
	// 它们不碰服务器上的任何东西，直接放行、不弹卡片。
	Meta bool
	// Op 是目录里的具体操作。IsVipanel && !Meta 时必定非 nil。
	Op *Op
}

// Classify 判定一个**完整**工具名（带 mcp__vipanel__ 前缀）。
func Classify(fullName string) Class {
	name, ok := strings.CutPrefix(fullName, ToolPrefix)
	if !ok {
		return Class{}
	}
	if name == "vipanel_overview" || name == "vipanel_task" {
		return Class{IsVipanel: true, Meta: true}
	}
	if mod, ok := strings.CutSuffix(name, "_tools"); ok && moduleExists(mod) {
		return Class{IsVipanel: true, Meta: true}
	}
	if op := ByName(name); op != nil {
		return Class{IsVipanel: true, Op: op}
	}
	// 前缀对上了但目录里没有——可能是清单改了而会话还开着。
	// 归为 IsVipanel 但 Op 为 nil，让调用方拒绝，而不是当成普通工具放过去。
	return Class{IsVipanel: true}
}

// ShortName 去掉前缀，用于展示和台账对账。
func ShortName(fullName string) string {
	return strings.TrimPrefix(fullName, ToolPrefix)
}

func ModuleTitle(key string) string {
	for _, m := range Modules {
		if m.Key == key {
			return m.Title
		}
	}
	return key
}

// ModuleStats 给板块授权卡片用：这个板块有多少操作、其中多少是删除类。
func ModuleStats(key string) (total, destructive int) {
	for _, op := range Catalog {
		if op.Module != key {
			continue
		}
		total++
		if op.Risk == "destructive" {
			destructive++
		}
	}
	return
}
