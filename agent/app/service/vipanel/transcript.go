package vipanel

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// 归一化后的事件词表。面板只认这几种，harness 各自负责把自己的记录翻译过来。
const (
	EvUser       = "user"
	EvAssistant  = "assistant"
	EvThinking   = "thinking"
	EvTool       = "tool"
	EvToolResult = "tool_result"
	EvTitle      = "title"
)

type Event struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	Name    string `json:"name,omitempty"`  // 工具名
	Input   string `json:"input,omitempty"` // 工具入参，原样的 JSON
	ID      string `json:"id,omitempty"`    // tool_use id，用来把结果配回调用
	IsError bool   `json:"isError,omitempty"`
	Ts      string `json:"ts,omitempty"`
}

// 内务记录：斜杠命令的展开、本地命令的回显。
// 这些是 claude 写给自己看的，混进聊天流里就是一堆看不懂的尖括号。
var localCommand = regexp.MustCompile(`^\s*<(local-command-(caveat|stdout)|command-(name|message|args))>`)

type rawLine struct {
	Type        string          `json:"type"`
	Message     *rawMessage     `json:"message"`
	Timestamp   string          `json:"timestamp"`
	CustomTitle string          `json:"customTitle"`
	Title       string          `json:"title"`
	IsSidechain bool            `json:"isSidechain"`
	Content     json.RawMessage `json:"content"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type rawBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	Name     string          `json:"name"`
	ID       string          `json:"id"`
	Input    json.RawMessage `json:"input"`

	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	RawResult json.RawMessage `json:"content"`
}

// parseLine 把一行 JSONL 翻译成零个或多个归一化事件。
func parseLine(b []byte) []Event {
	var r rawLine
	if err := json.Unmarshal(b, &r); err != nil {
		return nil // 半行/损坏，跳过
	}
	// 子 agent 的记录不进主对话流，否则会和主线交错成一团
	if r.IsSidechain {
		return nil
	}

	switch r.Type {
	case "custom-title":
		if r.CustomTitle != "" {
			return []Event{{Type: EvTitle, Text: r.CustomTitle}}
		}
		return nil
	case "ai-title":
		if r.Title != "" {
			return []Event{{Type: EvTitle, Text: r.Title}}
		}
		return nil
	case "user", "assistant":
	default:
		return nil
	}
	if r.Message == nil {
		return nil
	}

	// content 可能是一个裸字符串，也可能是 block 数组
	var text string
	if err := json.Unmarshal(r.Message.Content, &text); err == nil {
		if strings.TrimSpace(text) == "" || localCommand.MatchString(text) {
			return nil
		}
		return []Event{{Type: EvUser, Text: text, Ts: r.Timestamp}}
	}

	var blocks []rawBlock
	if err := json.Unmarshal(r.Message.Content, &blocks); err != nil {
		return nil
	}

	var out []Event
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) == "" || localCommand.MatchString(b.Text) {
				continue
			}
			kind := EvAssistant
			if r.Type == "user" {
				kind = EvUser
			}
			out = append(out, Event{Type: kind, Text: b.Text, Ts: r.Timestamp})
		case "thinking":
			if strings.TrimSpace(b.Thinking) == "" {
				continue
			}
			out = append(out, Event{Type: EvThinking, Text: b.Thinking, Ts: r.Timestamp})
		case "tool_use":
			out = append(out, Event{
				Type: EvTool, Name: b.Name, ID: b.ID,
				Input: string(b.Input), Ts: r.Timestamp,
			})
		case "tool_result":
			out = append(out, Event{
				Type: EvToolResult, ID: b.ToolUseID, IsError: b.IsError,
				Text: flattenResult(b.RawResult), Ts: r.Timestamp,
			})
		}
	}
	return out
}

// tool_result 的 content 有时是字符串，有时是 block 数组。压成一段文本。
func flattenResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []rawBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var sb strings.Builder
		for _, b := range blocks {
			if b.Text != "" {
				sb.WriteString(b.Text)
			}
		}
		return sb.String()
	}
	return string(raw)
}

// ReadTranscript 把整个文件读成事件序列。
func ReadTranscript(path string) []Event {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []Event
	sc := bufio.NewScanner(f)
	// transcript 单行可以非常长（一次大文件读取的 tool_result），
	// 默认 64KB 的上限会直接把这一行判成错误并中断扫描。
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		out = append(out, parseLine(sc.Bytes())...)
	}
	return out
}

// Tailer 从文件末尾接着读新写入的行。
//
// 用轮询而不是 inotify：transcript 由另一个进程以追加方式写，轮询 size 足够，
// 而且不用处理文件还没被创建、被轮转、跨文件系统这些边角。
type Tailer struct {
	path   string
	offset int64
	stop   chan struct{}
	once   sync.Once
}

func NewTailer(path string, fromEnd bool) *Tailer {
	t := &Tailer{path: path, stop: make(chan struct{})}
	if fromEnd {
		if st, err := os.Stat(path); err == nil {
			t.offset = st.Size()
		}
	}
	return t
}

func (t *Tailer) Stop() { t.once.Do(func() { close(t.stop) }) }

// Run 持续把新事件送进 out，直到 Stop。
func (t *Tailer) Run(out func([]Event)) {
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-tick.C:
			if evs := t.readNew(); len(evs) > 0 {
				out(evs)
			}
		}
	}
}

func (t *Tailer) readNew() []Event {
	st, err := os.Stat(t.path)
	if err != nil {
		return nil
	}
	// 文件被截断/换掉了，从头再来
	if st.Size() < t.offset {
		t.offset = 0
	}
	if st.Size() == t.offset {
		return nil
	}
	f, err := os.Open(t.path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return nil
	}

	rd := bufio.NewReaderSize(f, 1<<20)
	var out []Event
	consumed := t.offset
	for {
		line, err := rd.ReadBytes('\n')
		if err != nil {
			// 最后一行还没写完（没有换行符），这次不消费它，下轮再读
			break
		}
		consumed += int64(len(line))
		out = append(out, parseLine(line)...)
	}
	t.offset = consumed
	return out
}
