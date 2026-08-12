package dto

type ViSessionItem struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Cwd          string   `json:"cwd"`
	Dir          string   `json:"dir"`
	Harness      string   `json:"harness"`
	Status       string   `json:"status"`
	Alive        bool     `json:"alive"`
	LastUsed     int64    `json:"lastUsed"`
	Notes        string   `json:"notes"`
	Mode         string   `json:"mode"`
	Capabilities ViCaps   `json:"capabilities"`
	_            struct{} `json:"-"`
}

type ViCaps struct {
	StructuredEvents bool     `json:"structuredEvents"`
	Resume           bool     `json:"resume"`
	Interrupt        bool     `json:"interrupt"`
	Auth             bool     `json:"auth"`
	Models           []string `json:"models"`
	EffortLevels     []string `json:"effortLevels"`
	Commands         any      `json:"commands"`
	LoginModes       any      `json:"loginModes"`
}

type ViHarnessItem struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	// Installed 为 false 时界面必须把它标成不可用，而不是让用户点进去撞墙。
	Installed bool          `json:"installed"`
	Install   ViInstallInfo `json:"install"`
	Caps      ViCaps        `json:"capabilities"`
}

// ViInstallInfo 告诉界面「这个 harness 能不能装、装之前还缺什么」。
type ViInstallInfo struct {
	Installable bool `json:"installable"`
	// Missing 是本机缺少的前置依赖。非空时界面显示原因而不是安装按钮——
	// 让用户点一个注定失败的按钮不如直接告诉他缺什么。
	Missing []ViPrereq `json:"missing,omitempty"`
	Note    string     `json:"note,omitempty"`
}

type ViPrereq struct {
	Binary string `json:"binary"`
	Hint   string `json:"hint"`
}

type ViSessionCreate struct {
	Cwd     string `json:"cwd" validate:"required"`
	Title   string `json:"title"`
	Harness string `json:"harness"`
}

type ViSessionID struct {
	ID string `json:"id" validate:"required"`
}

type ViSessionRename struct {
	ID    string `json:"id" validate:"required"`
	Title string `json:"title" validate:"required"`
}

type ViPoolInfo struct {
	Size   int      `json:"size"`
	Active int      `json:"active"`
	Order  []string `json:"order"`
}

type ViPoolSize struct {
	Size int `json:"size" validate:"required,min=1,max=16"`
}

type ViAccountCapture struct {
	Harness string `json:"harness" validate:"required"`
	Label   string `json:"label"`
}

type ViAccountRef struct {
	Harness string `json:"harness" validate:"required"`
	ID      string `json:"id" validate:"required"`
}

type ViProxy struct {
	URL string `json:"url"`
}

type ViProviderReq struct {
	Harness string `json:"harness" validate:"required"`
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
}

type ViHarnessRef struct {
	Harness string `json:"harness" validate:"required"`
}
