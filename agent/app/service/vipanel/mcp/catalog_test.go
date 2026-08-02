package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// 这些不变量的共同点是：**破了不会报错，只会静默降级**。
// 生成器改一行、manifest 手滑一个字段，都能让它们悄悄失效，所以钉在测试里。

// 会话自身的控制面绝不能出现在目录里。
// /ai/console/permission/resolve 的入参只有 {id, decision}，没有任何东西
// 把它绑定到一个人——它一旦成为工具，agent 就能批准自己的请求，
// 权限代理归零。生成器有一道过滤，这是第二道。见 MCP.md §1.1。
func TestNoConsoleOpsInCatalog(t *testing.T) {
	for _, op := range Catalog {
		if strings.HasPrefix(op.Path, "/ai/console") {
			t.Fatalf("%s 指向 %s：会话自身的控制面不能暴露给 agent", op.Name, op.Path)
		}
	}
}

func TestToolNamesUnique(t *testing.T) {
	seen := map[string]string{}
	for _, op := range Catalog {
		if prev, dup := seen[op.Name]; dup {
			t.Errorf("工具名 %s 重复：%s 和 %s", op.Name, prev, op.Path)
		}
		seen[op.Name] = op.Path
	}
}

func TestRiskIsOneOfThree(t *testing.T) {
	for _, op := range Catalog {
		switch op.Risk {
		case "read", "write", "destructive":
		default:
			t.Errorf("%s 的 risk 是 %q，只允许 read/write/destructive", op.Name, op.Risk)
		}
	}
}

// 常驻工具绕过板块授权，所以只能是只读的。
// 一个常驻的变更类工具意味着 agent 开局就能改东西而不经过任何一次板块确认。
func TestResidentOpsAreReadOnly(t *testing.T) {
	for _, op := range Catalog {
		if op.Resident && op.Risk != "read" {
			t.Errorf("%s 是常驻工具却标了 %s：常驻绕过板块授权，只能给只读", op.Name, op.Risk)
		}
	}
}

// 变更类操作必须有中文文案，否则权限卡片上只能显示原始 JSON。
// 人看不懂就会一路点允许——那时权限代理是摆设不是护栏。见 MCP.md §6.5。
func TestMutatingOpsHaveReadableText(t *testing.T) {
	for _, op := range Catalog {
		if op.Risk == "read" {
			continue
		}
		if strings.TrimSpace(op.FormatZH) == "" {
			t.Errorf("%s 是变更类操作却没有中文文案", op.Name)
		}
	}
}

func TestInputSchemaIsValidObject(t *testing.T) {
	for _, op := range Catalog {
		var s map[string]any
		if err := json.Unmarshal([]byte(op.InputSchema), &s); err != nil {
			t.Errorf("%s 的 InputSchema 不是合法 JSON: %v", op.Name, err)
			continue
		}
		if s["type"] != "object" {
			t.Errorf("%s 的 InputSchema 顶层 type 不是 object", op.Name)
		}
		if _, ok := s["properties"].(map[string]any); !ok {
			t.Errorf("%s 的 InputSchema 缺 properties", op.Name)
		}
	}
}

// 固定参数由运行时注入，绝不能出现在给模型看的 schema 里。
// 露出来模型就会去填，填了要么被覆盖（困惑）要么覆盖我们的值（危险：
// container_restart 传 operation=remove 就成了删容器）。
func TestFixedParamsNotExposedInSchema(t *testing.T) {
	for _, op := range Catalog {
		if op.Fixed == "" {
			continue
		}
		var fixed map[string]any
		if err := json.Unmarshal([]byte(op.Fixed), &fixed); err != nil {
			t.Errorf("%s 的 Fixed 不是合法 JSON: %v", op.Name, err)
			continue
		}
		var s struct {
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
		}
		_ = json.Unmarshal([]byte(op.InputSchema), &s)
		for k := range fixed {
			if _, leaked := s.Properties[k]; leaked {
				t.Errorf("%s 的固定参数 %s 泄漏进了 InputSchema", op.Name, k)
			}
			for _, r := range s.Required {
				if r == k {
					t.Errorf("%s 的固定参数 %s 还留在 required 里", op.Name, k)
				}
			}
		}
	}
}

func TestEveryOpHasDeclaredModule(t *testing.T) {
	known := map[string]bool{}
	for _, m := range Modules {
		known[m.Key] = true
	}
	for _, op := range Catalog {
		if !known[op.Module] {
			t.Errorf("%s 的板块 %s 未在 Modules 里声明", op.Name, op.Module)
		}
	}
}

// server instructions 是**唯一**一段会被上游加载的散文（工具描述是 defer 的），
// 而 Claude Code 把它截断在 2KB。板块索引超了就会被砍掉尾巴，
// 后面的板块模型就再也找不到了。留一半余量给固定的说明文字。
func TestModuleIndexFitsInstructionsBudget(t *testing.T) {
	var b strings.Builder
	for _, m := range Modules {
		b.WriteString(m.Key + " " + m.Desc + " · ")
	}
	if n := len(b.String()); n > 1024 {
		t.Errorf("板块索引 %d 字节，超过给它的 1KB 预算（instructions 总共只有 2KB）", n)
	}
}

// ByName 找不到时必须返回 nil 而不是零值 Op——
// 零值 Op 的 Path 是空串，会让运行时的 /ai/console 前缀检查失去意义。
func TestByNameMissReturnsNil(t *testing.T) {
	if got := ByName("this_tool_does_not_exist"); got != nil {
		t.Errorf("期望 nil，得到 %+v", got)
	}
	if len(Catalog) > 0 {
		if got := ByName(Catalog[0].Name); got == nil {
			t.Error("已知工具名查不到")
		}
	}
}
