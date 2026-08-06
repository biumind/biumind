package code

// PTY 输出落盘 + 回放(终端原始视图的持久化:不止已结束任务的结构化 JSONL 回放,
// 我们把每个任务 PTY 的原始字节 tee 到
// ~/.biumind/code/pty-logs/<taskID>.log,重开时 pty.replayLog 读回喂进 xterm,
// 让原始终端跨重启/跨设备会话存活)。
//
// 写:runTask 用 newPTYLogWriter 包住 emit.Chunk,每帧同时落盘。新跑 truncate、
// resume 续跑 append(保留前一段)。读:readPTYLog 限最近 ptyLogMaxReplay 字节
// (终端 scrollback 有上限,无需全量;xterm 侧也只留 1 万行)。

import (
	"io"
	"os"
	"path/filepath"
	"sync"
)

// ptyLogMaxReplay 限制回放读取的字节数(取文件尾部),避免超长会话把内存/IPC 撑爆。
const ptyLogMaxReplay = 2 << 20 // 2 MiB

func ptyLogPath(taskID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".biumind", "code", "pty-logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// taskID 是 UUID(无路径分隔符),仍取 Base 兜底防穿越。
	return filepath.Join(dir, filepath.Base(taskID)+".log"), nil
}

// ptyLogWriter 把 PTY 字节追加到任务日志。所有方法 nil/未打开安全 —— 落盘失败
// 绝不影响任务运行(最坏退化成「重开无终端回放」,与不做时等价)。
type ptyLogWriter struct {
	mu sync.Mutex
	f  *os.File
}

// newPTYLogWriter 打开任务日志。append=true(resume 续跑)保留旧内容,否则 truncate。
func newPTYLogWriter(taskID string, appendMode bool) *ptyLogWriter {
	p, err := ptyLogPath(taskID)
	if err != nil {
		return &ptyLogWriter{}
	}
	flag := os.O_CREATE | os.O_WRONLY
	if appendMode {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(p, flag, 0o644)
	if err != nil {
		return &ptyLogWriter{}
	}
	return &ptyLogWriter{f: f}
}

func (w *ptyLogWriter) write(b []byte) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return
	}
	_, _ = w.f.Write(b)
}

func (w *ptyLogWriter) close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
}

// readPTYLog 返回任务日志尾部最多 ptyLogMaxReplay 字节(无文件返回 nil,无错)。
func readPTYLog(taskID string) ([]byte, error) {
	p, err := ptyLogPath(taskID)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	if st, serr := f.Stat(); serr == nil && st.Size() > ptyLogMaxReplay {
		if _, serr := f.Seek(st.Size()-ptyLogMaxReplay, io.SeekStart); serr != nil {
			return nil, serr
		}
	}
	return io.ReadAll(f)
}

// removePTYLog 删除任务日志(任务删除时清理)。无文件不报错。
func removePTYLog(taskID string) {
	if p, err := ptyLogPath(taskID); err == nil {
		_ = os.Remove(p)
	}
}
