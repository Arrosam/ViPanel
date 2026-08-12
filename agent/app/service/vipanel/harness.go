package vipanel

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

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

	// Binary 是这个 harness 要执行的命令名，用来判断它在这台机器上装没装。
	//
	// 单独一个方法而不是从 Spawn 返回的 PtySpec 里读：Spawn **有副作用**
	// （claude 那侧会写向导标记、装钩子、装 MCP），为了问一句「装了吗」
	// 去跑一遍那些副作用是错的。
	Binary() string

	// InstallPlan 返回把这个 harness 装到本机所需的一切。
	//
	// **进主接口而不是做成可选接口**，是因为「怎么装」这个问题对每个 harness
	// 都有答案——包括「装不了」和「不用装」。可选接口的语义是「这个概念对我
	// 不适用」，而安装不属于这一类：shell 的答案是「系统自带」，那是一个
	// 真实的回答，不是缺席。放进主接口，加新 harness 时编译器会逼着回答它。
	InstallPlan() InstallPlan

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
	// LoginModes 是这个 harness 有哪几种登录方式。
	//
	// 登录对话框上那两个单选钮原来是写死的 claudeai / console —— 那是 Claude
	// 独有的两种登录入口，Codex 一种都没有。**有哪些方式、每种长什么样**
	// 是 harness 的知识，由这里声明；**每种方式叫什么**是文案，留在前端的 i18n 里。
	LoginModes []LoginMode `json:"loginModes"`
}

type Command struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
}

// LoginMode 是一种登录方式。ID 同时是前端取文案的 i18n 键。
type LoginMode struct {
	ID string `json:"id"`
	// NeedsCodeInput 决定对话框末尾那个「把授权码粘回来」的输入框出不出现。
	//
	// 这两类流程的**码是反方向走的**，搞混了界面就会要用户去粘一个根本不存在
	// 的东西：
	//   - true —— 浏览器给码，用户粘回终端（Claude 的两种方式都是这样）
	//   - false —— 终端给码，用户拿到别的设备上去输（Codex 的设备码授权）
	NeedsCodeInput bool `json:"needsCodeInput"`
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

// InstallPlan 是把一个 harness 装上所需的一切。
type InstallPlan struct {
	// Installable 为 false 时面板不显示安装按钮。
	// shell 就是这种：它是系统自带的，没有「安装」这个动作。
	Installable bool
	// Requires 是前置依赖。**面板先查这些命令在不在，缺了直接说清楚**，
	// 而不是把安装脚本跑起来、让用户对着 "npm: command not found" 发愣。
	Requires []Prereq
	// Spec 是安装命令本身。走伪终端，输出实时推到浏览器——
	// npm 装东西要几十秒，没有实时输出的进度条只会让人以为卡死了。
	Spec PtySpec
	// Note 是给人看的一句话：装的是什么、从哪来。
	// 安装是往这台机器上放可执行文件，用户有权在点之前知道装的是什么。
	Note string
}

// Prereq 是一个前置依赖。
type Prereq struct {
	Binary string // 要检查在不在的命令名
	// MinVersion 非空时**还要比版本**，格式 "22.0.0"。
	//
	// 只查命令在不在是不够的，这条是真机上撞出来的：Debian 12 自带 Node 18，
	// 而 claude-code 的 engines 要求 >= 22。命令在、版本不够时，按钮照样出现，
	// 用户点下去，npm 跑到一半以 EBADENGINE 失败——正是这套前置检查要防的事。
	MinVersion string
	Hint       string // 不满足时告诉用户怎么办
}

// MissingPrereqs 返回这个 harness 的安装前置里，本机不满足的那些。
func MissingPrereqs(h Harness) []Prereq {
	var out []Prereq
	for _, r := range h.InstallPlan().Requires {
		if _, err := exec.LookPath(r.Binary); err != nil {
			out = append(out, r)
			continue
		}
		if r.MinVersion != "" && !versionAtLeast(binaryVersion(r.Binary), r.MinVersion) {
			out = append(out, r)
		}
	}
	return out
}

// binaryVersion 跑 `<bin> --version` 并从输出里挑出第一串版本号。
// 取不到返回空串——空串在 versionAtLeast 里一律判为不满足，
// 因为「问不出版本」和「版本太低」对用户来说是同一件事：别让他点那个按钮。
func binaryVersion(bin string) string {
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	return versionRe.FindString(string(out))
}

var versionRe = regexp.MustCompile(`[0-9]+(\.[0-9]+)*`)

// versionAtLeast 按段比较点分版本号，不引第三方 semver 库：
// 这里只需要比 major.minor.patch，引一个依赖进 agent/go.mod 不划算
// （那个文件是 rebase 冲突面）。
func versionAtLeast(got, want string) bool {
	if got == "" {
		return false
	}
	g, w := strings.Split(got, "."), strings.Split(want, ".")
	for i := 0; i < len(w); i++ {
		var gv int
		if i < len(g) {
			gv, _ = strconv.Atoi(g[i])
		}
		wv, _ := strconv.Atoi(w[i])
		if gv != wv {
			return gv > wv
		}
	}
	return true
}

// Installed 判断一个 harness 在这台机器上装没装。
//
// 没装的东西不该在界面上表现得可以用：点了新建会话，进程起不来，
// 用户看到的是一个空终端和一句看不懂的报错。
func Installed(h Harness) bool {
	bin := h.Binary()
	if bin == "" {
		return false
	}
	if filepath.IsAbs(bin) {
		st, err := os.Stat(bin)
		return err == nil && !st.IsDir()
	}
	_, err := exec.LookPath(bin)
	return err == nil
}

// List 返回全部 harness，**顺序稳定**。
//
// registry 是 map，直接遍历的顺序每次都不一样——界面上表现为几张卡片
// 每次刷新都在换位置。默认 harness 排第一，其余按 id 字典序。
func List() []Harness {
	out := make([]Harness, 0, len(registry))
	for _, h := range registry {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].ID() == DefaultHarness) != (out[j].ID() == DefaultHarness) {
			return out[i].ID() == DefaultHarness
		}
		return out[i].ID() < out[j].ID()
	})
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

// PermissionDialect 由「钩子的决定词表和默认不一样」的 harness 实现。
//
// 面板等不到人做决定时该回什么，是 harness 特有的：
//   - Claude 的 PreToolUse 认 allow / deny / ask，可以退回 ask 让 TUI 自己问，
//     那边至少还有个人能看见。
//   - Codex 的 PreToolUse 只认 allow / deny，没有 ask 这一档，退无可退。
//
// 不实现这个接口的 harness 一律用 ask —— 保持原有行为不变。
type PermissionDialect interface {
	// TimeoutDecision 是面板在时限内没等到人时该给出的决定。
	TimeoutDecision() Decision
}

// timeoutDecisionFor 问某个会话的 harness：等不到人时回什么。
// 会话不在或没实现这个接口时退回 ask —— 那是原来的行为。
func timeoutDecisionFor(sessionID string) Decision {
	s, ok := M().Get(sessionID)
	if !ok {
		return DecideAsk
	}
	if d, ok := s.Harness.(PermissionDialect); ok {
		return d.TimeoutDecision()
	}
	return DecideAsk
}

// LoginCodeSource 由「登录时终端发出一个码、要用户拿到别处去输」的 harness 实现。
//
// 这是两种登录流的**方向差异**，不是同一件事的两种写法：
//   - Claude：浏览器给码，用户粘回终端。关键信息是那个一次性链接。
//   - Codex 设备码流：终端给码，用户输进浏览器。链接是固定的通用地址
//     （https://auth.openai.com/codex/device），**关键信息是那个码**，
//     而且 15 分钟过期。
//
// 面板原来只把链接挑出来显眼展示，对 Codex 来说恰好把不重要的那个放大了，
// 真正要用的码埋在一堆带 ANSI 转义的原始输出里。
//
// 怎么从输出里认出那个码是 harness 私有的知识，所以由它自己声明。
type LoginCodeSource interface {
	// LoginCode 从一段原始输出里挑出一次性码，挑不到返回空串。
	LoginCode(chunk []byte) string
}

// ExtractLoginCode 问某个 harness：这段输出里有没有要展示给用户的码。
func ExtractLoginCode(harnessID string, chunk []byte) string {
	if src, ok := Get(harnessID).(LoginCodeSource); ok {
		return src.LoginCode(chunk)
	}
	return ""
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
