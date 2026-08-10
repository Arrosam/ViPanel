package vipanel

// Harness 是面板和具体 agent（Claude Code / 别的什么）之间**唯一**的接缝。
//
// 这套接口的设计原则是：能力**声明**，不靠失败去探测。
// 面板永远不该「先调一下试试，报错了就说明不支持」——那样每加一个 harness
// 都要在面板各处补 if。所有差异都收在实现里，面板只读 Capabilities。
//
// 判断一个方法该不该进这个接口，看它是否**因 harness 而异**：
//   - 「怎么启动进程」因 harness 而异 → Spawn
//   - 「LRU 池怎么调度」不因 harness 而异 → 不进接口
type Harness interface {
	ID() string
	DisplayName() string
	Capabilities() Capabilities

	// Spawn 返回启动这个 agent 所需的命令。resume 为真时要接回原有对话。
	Spawn(ctx SpawnContext) PtySpec

	// Submit 把一条消息交给 agent。
	//
	// 单独成一个方法而不是让调用方自己 write，是因为提交动作本身因 harness
	// 而异：Claude Code 的 TUI 必须把正文和回车拆成两次写，合在一起会被当成
	// 粘贴的换行，消息永远发不出去。
	Submit(write func([]byte), text string)

	// Interrupt 中断当前生成。
	Interrupt(write func([]byte))

	// HasHistory 判断这个 session id 是否已经产生过对话。
	//
	// 决定 Spawn 时能不能带 resume：对一个从没说过话的 session id 执行
	// --resume，claude 会以 "No conversation found with session ID" 直接退出。
	HasHistory(sessionID, cwd string) bool
}

// Capabilities 声明一个 harness 支持什么。
// 全 false 是合法的 —— shell harness 就是这样，它的存在是为了证伪这套抽象。
type Capabilities struct {
	StructuredEvents bool     `json:"structuredEvents"` // 有没有可读的结构化对话记录
	Resume           bool     `json:"resume"`           // 能不能接回原有对话
	Interrupt        bool     `json:"interrupt"`        // 能不能中断生成
	Auth             bool     `json:"auth"`             // 有没有自己的登录体系
	Models           []string `json:"models"`
	EffortLevels     []string `json:"effortLevels"`
	// Commands 是这个 harness 支持的斜杠命令。由 harness **声明**，
	// 不是前端硬编码——换一个 harness 命令列表就该跟着换。
	Commands []Command `json:"commands"`
}

type Command struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
}

type SpawnContext struct {
	SessionID string
	Cwd       string
	Resume    bool
	Env       []string
	Cols      int
	Rows      int
}

var registry = map[string]Harness{}

func register(h Harness) { registry[h.ID()] = h }

// Get 取一个 harness；名字不认识时退回默认的，绝不返回 nil。
//
// 返回 nil 会让调用方到处判空，而漏判的那一处就是一个空指针 panic ——
// demo 阶段第一天就是这么炸的（authStatus() 返回 nil）。
func Get(id string) Harness {
	if h, ok := registry[id]; ok {
		return h
	}
	return registry[DefaultHarness]
}

func List() []Harness {
	out := make([]Harness, 0, len(registry))
	for _, h := range registry {
		out = append(out, h)
	}
	return out
}

const DefaultHarness = "claude-code"

// Discovered 是磁盘上还没被面板收录的一段历史对话。
type Discovered struct {
	ID    string `json:"id"`
	Cwd   string `json:"cwd"`
	Title string `json:"title"`
	Mtime int64  `json:"mtime"`
	Size  int64  `json:"size"`
}

// Discoverer 由「自己维护对话记录」的 harness 实现。
// 不进 Harness 主接口：没有结构化记录的 harness 根本没有这个概念，
// 硬塞进去只会逼它们写一个返回 nil 的空方法。
type Discoverer interface {
	Discover(known map[string]bool, limit int) []Discovered
}

// AutoAllower 由「有一些纯内部机制的工具」的 harness 实现。
//
// 这类工具不碰任何东西，只是 harness 自己找工具/等服务的中间步骤。
// 为它们弹确认卡片是纯噪音——而且比噪音更糟：实测里 agent 每次想找面板工具
// 都被拦住等满 120 秒，整条链路根本跑不起来。
//
// 判断哪些工具属于这一类是 harness 特有的知识，所以放在各自的适配层里。
type AutoAllower interface {
	AutoAllow(tool string) bool
}

// AutoAllowed 问某个会话的 harness：这个工具要不要直接放行。
// 会话不在或 harness 没实现这个接口时一律返回 false——默认是问人，不是放行。
func AutoAllowed(sessionID, tool string) bool {
	s, ok := M().Get(sessionID)
	if !ok {
		return false
	}
	a, ok := s.Harness.(AutoAllower)
	return ok && a.AutoAllow(tool)
}

// TitleSource 由「自己会给会话起标题」的 harness 实现。
//
// **标题藏在 harness 私有的记录格式里，字段名是它的私有知识。**
// 这个接口存在的直接原因就是一个真实的 bug：通用代码曾经自己去猜那个字段名，
// transcript.go 和 harness_claude.go 的 peek() 各猜了一遍、都猜成 "title"，
// 而 claude 实际写的是 "aiTitle"。两处都静默失败——一处没有消费者所以不报错，
// 另一处有目录名兜底所以看不出来，于是「会话标题不会自动更新」这件事一直像是设计如此。
//
// 猜错一次是失误，猜错两次是结构问题：这类知识不该有第二份拷贝。
type TitleSource interface {
	// TitleOf 从一行原始记录里取标题，取不到返回空串。
	// manual 为 true 表示这是用户手工定的标题——它应当压过自动生成的那个。
	TitleOf(line []byte) (title string, manual bool)
}

// PermissionStore 由「自己有一份权限规则配置」的 harness 实现。
//
// 「总是允许」的名单必须由**我们的钩子**来认——实测过，把工具写进
// harness 的允许名单不会让 PreToolUse 不触发，它跑在权限规则之前，
// 我们一返回 allow/deny，它的规则就没机会执行了。
//
// 但名单**存在 harness 自己的配置里**，这样真相仍然只有一份：
// 用户在标准位置看得到、改得了，哪天我们的钩子不在了它自己也会认。
// 见 MCP.md §6.2。
type PermissionStore interface {
	AlwaysAllowed(tool string) bool
	AddAlwaysAllow(tool string) error
}

// AuthState 是 harness 自己的登录状态（与面板登录无关）。
type AuthState struct {
	Supported bool `json:"supported"`
	// HookInstalled 为 false 时界面必须显式标出「权限代理未生效」。
	// 这个面板没有沙箱兜底，静默失去护栏是不可接受的。
	HookInstalled bool   `json:"hookInstalled"`
	LoggedIn      bool   `json:"loggedIn"`
	AuthMethod    string `json:"authMethod,omitempty"`
	Email         string `json:"email,omitempty"`
	Plan          string `json:"plan,omitempty"`
}

// Authenticator 由「自己有一套登录体系」的 harness 实现。
// 和 Discoverer 一样是可选接口：shell 没有登录这个概念，
// 硬塞进主接口只会逼它写一个空方法。
type Authenticator interface {
	AuthStatus() AuthState
	LoginSpec(mode string) PtySpec
	Logout() error
}

// Controller 由「能在运行中调整行为」的 harness 实现。
// 和 Discoverer / Authenticator 一样是可选接口。
type Controller interface {
	// CycleMode 循环切换执行模式。
	CycleMode(write func([]byte))
	SetModel(write func([]byte), v string)
	SetEffort(write func([]byte), v string)
	// ReadMode 从 agent 屏幕的原始字节里读当前模式，读不出返回空串。
	ReadMode(screen []byte) string
}
