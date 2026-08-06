// Package pty 是编码模块的通用伪终端（PTY）管理器 —— 提供通用管道部分（不含
// Claude/Codex 专属命令构造，那是 M2 在本包之上叠加）。
//
// 设计要点：
//   - 用单张 map 按 id 索引 handle；creack/pty 的 master *os.File 同时是读端和
//     写端，故每个 handle（含 ptmx + cmd）收敛成单个对象，无需分别存
//     master/writer/child。
//   - 读循环常量：32KB 读缓冲、16ms / 64KB 批量、cap-32 有界 channel 反压（满则
//     reader 阻塞 → OS PTY 缓冲填满 → agent 的 write() 阻塞，从源头限流）。
//   - Input/Resize 先在 map 锁下取出 handle，再在 handle 自己的 writeMu 下做 IO ——
//     不在持 map 锁期间做 IO。
//   - resize 钳到 [2,10000]，防隐藏容器算出 cols=2 经 SIGWINCH 打散全屏 TUI。
//
// ⚠️ creack/pty 不原生支持 Windows ConPTY（portable_pty 支持）。本包当前面向
// macOS / Linux；Windows 留 M2 处理（可能需 conpty shim）。
package pty

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
)

const (
	// readBufferSize 是单次 PTY read 的缓冲大小。4KB 时 npm install 这类大输出会
	// 产生 25000+ 次发送，故放大到 32KB。
	readBufferSize = 32 * 1024
	// flushInterval 是批量 flush 节奏（≈60fps 一帧），把高频读和较慢的序列化/
	// 发送解耦。
	flushInterval = 16 * time.Millisecond
	// maxBatchBytes：累积超过此阈值立即 flush，避免单帧过大。
	maxBatchBytes = 64 * 1024
	// emitChannelCap 是 reader → batcher 有界 channel 容量。满时 reader 阻塞，
	// 反压一路传到写入进程，从源头限流。
	emitChannelCap = 32
)

// ErrNotFound 表示给定 id 没有活跃 PTY。
var ErrNotFound = errors.New("pty: no such session")

// ChunkFn 在每批 PTY 输出就绪时被调用（data 为该批原始字节，调用方拥有所有权）。
type ChunkFn func(id string, data []byte)

// ExitFn 在 PTY 进程退出且输出全部 flush 后被调用一次。code 为进程退出码
// （0=正常）；err 非空表示 wait 自身出错而非进程返回非零。
type ExitFn func(id string, code int, err error)

// OpenSpec 描述要拉起的进程。Env 是追加到 os.Environ() 之上的额外变量。
type OpenSpec struct {
	Cmd  string
	Args []string
	Cwd  string
	Env  []string
	Cols uint16
	Rows uint16
}

type handle struct {
	id      string
	ptmx    *os.File
	cmd     *exec.Cmd
	writeMu sync.Mutex // 串行化写；绝不在持 Manager.mu 期间持有

	// sink 可替换 —— 输出/退出回调经 sinkMu 派发,支持断线重连后把活 PTY 的输出
	// 重绑到新连接的 emitter(Reattach)。batcher / 退出 goroutine 调 emitChunk /
	// emitExit 间接派发,故换 sink 立即对正在跑的 PTY 生效。
	sinkMu  sync.Mutex
	onChunk ChunkFn
	onExit  ExitFn
}

// emitChunk / emitExit 在 sinkMu 下读当前 sink 再调 —— 与 Reattach 的换 sink 串行,
// 避免读到半替换状态。
func (h *handle) emitChunk(id string, data []byte) {
	h.sinkMu.Lock()
	f := h.onChunk
	h.sinkMu.Unlock()
	if f != nil {
		f(id, data)
	}
}

func (h *handle) emitExit(id string, code int, err error) {
	h.sinkMu.Lock()
	f := h.onExit
	h.sinkMu.Unlock()
	if f != nil {
		f(id, code, err)
	}
}

// Manager 持有所有活跃 PTY。一个 biu serve 进程一个实例（跨 WS 连接共享，
// 故断线重连可重新 attach —— attach 逻辑 M2/M3 补）。
type Manager struct {
	mu   sync.Mutex
	ptys map[string]*handle
}

// NewManager 建空管理器。
func NewManager() *Manager {
	return &Manager{ptys: map[string]*handle{}}
}

// Open 拉起一个进程并接管其 PTY。id 由调用方提供（须唯一）。onChunk 收到分批的
// 输出字节；onExit 在进程退出后调用一次。立即返回（读/退出在后台 goroutine）。
func (m *Manager) Open(id string, spec OpenSpec, onChunk ChunkFn, onExit ExitFn) error {
	cols, rows := spec.Cols, spec.Rows
	if cols == 0 {
		cols = 220 // 默认列宽
	}
	if rows == 0 {
		rows = 50
	}

	cmd := exec.Command(spec.Cmd, spec.Args...)
	if spec.Cwd != "" {
		cmd.Dir = spec.Cwd
	}
	cmd.Env = buildEnv(spec.Env)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		return err
	}

	h := &handle{id: id, ptmx: ptmx, cmd: cmd, onChunk: onChunk, onExit: onExit}
	m.mu.Lock()
	m.ptys[id] = h
	m.mu.Unlock()

	// reader → 有界 channel → batcher → h.emitChunk(经可替换 sink)。channel 满则
	// reader 阻塞（反压）。用 h.emitChunk 而非直接 onChunk,使 Reattach 换 sink 生效。
	in := make(chan []byte, emitChannelCap)
	batcherDone := make(chan struct{})
	go func() {
		runBatcher(id, in, h.emitChunk)
		close(batcherDone)
	}()
	go runReader(ptmx, in)

	// 退出监控：cmd.Wait 返回后，等 batcher 把残余输出 flush 完，再发 onExit，
	// 保证「最后一段输出」一定先于 exit 通知到达客户端。
	go func() {
		waitErr := cmd.Wait()
		<-batcherDone
		_ = ptmx.Close()
		m.remove(id)
		code, werr := exitInfo(waitErr)
		h.emitExit(id, code, werr)
	}()
	return nil
}

// Reattach 把活 PTY 的输出/退出回调重绑到新的 sink(断线重连:新 WS 连接接管该
// PTY 的输出)。找到并替换返回 true;无此 PTY 返回 false。输入(Input)与连接无关,
// 不需重绑。
func (m *Manager) Reattach(id string, onChunk ChunkFn, onExit ExitFn) bool {
	h := m.get(id)
	if h == nil {
		return false
	}
	h.sinkMu.Lock()
	h.onChunk = onChunk
	h.onExit = onExit
	h.sinkMu.Unlock()
	return true
}

// Input 把字节写入 PTY（键盘/粘贴）。先在 map 锁下取 handle，再在 handle 的
// writeMu 下写 —— 不在 map 锁期间做 IO。
func (m *Manager) Input(id string, data []byte) error {
	h := m.get(id)
	if h == nil {
		return ErrNotFound
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_, err := h.ptmx.Write(data)
	return err
}

// Resize 调整 PTY 窗口。畸形尺寸（<2 或 >10000）静默忽略 —— 防前端在隐藏容器
// 算出 cols=2 经 SIGWINCH 打散全屏 TUI。
func (m *Manager) Resize(id string, cols, rows uint16) error {
	if cols < 2 || rows < 2 || cols > 10000 || rows > 10000 {
		return nil
	}
	h := m.get(id)
	if h == nil {
		return ErrNotFound
	}
	return pty.Setsize(h.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
}

// Kill 终止 PTY 进程。句柄的清理由退出监控 goroutine 在 cmd.Wait 返回后完成。
func (m *Manager) Kill(id string) error {
	h := m.get(id)
	if h == nil {
		return ErrNotFound
	}
	if h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
	}
	return nil
}

// Active 返回当前活跃 PTY 的 id 列表。
func (m *Manager) Active() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.ptys))
	for id := range m.ptys {
		ids = append(ids, id)
	}
	return ids
}

// CloseAll 杀掉所有 PTY 进程 —— daemon 关停时调用。
func (m *Manager) CloseAll() {
	m.mu.Lock()
	handles := make([]*handle, 0, len(m.ptys))
	for _, h := range m.ptys {
		handles = append(handles, h)
	}
	m.mu.Unlock()
	for _, h := range handles {
		if h.cmd.Process != nil {
			_ = h.cmd.Process.Kill()
		}
	}
}

func (m *Manager) get(id string) *handle {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ptys[id]
}

func (m *Manager) remove(id string) {
	m.mu.Lock()
	delete(m.ptys, id)
	m.mu.Unlock()
}

// runReader 在自己的 goroutine 里循环读 PTY，把每次读到的字节（拷贝后）送进有界
// channel。channel 满 → 阻塞（反压）。读到 EOF/err → 关 channel，batcher 收尾。
func runReader(ptmx *os.File, in chan<- []byte) {
	defer close(in)
	buf := make([]byte, readBufferSize)
	for {
		n, err := ptmx.Read(buf)
		if n > 0 {
			cp := make([]byte, n)
			copy(cp, buf[:n])
			in <- cp
		}
		if err != nil {
			return
		}
	}
}

// runBatcher 把 PTY 输出按「空闲首字节直发、高频才批量」策略推给 onChunk:
//
//   - 空闲态(无观察窗口)收到数据 → 立即 flush 这一段。单次按键 echo 这类孤立
//     输出延迟 ≈ 传输+调度(亚毫秒级),不再白等一个 flushInterval。
//   - 直发后开一个 flushInterval 观察窗口:窗口内再来的数据累积成批,到点一次性
//     flush;持续高频(到点仍有累积)则继续按 ~60fps 节奏合帧,并受 maxBatchBytes
//     触发即时 flush。窗口到点无累积 → 回空闲态(下次首字节又直发)。
//   - channel 关闭 → flush 残余并退出。
//
// 即:孤立交互走直发(低延迟),npm install 这类突发仍合帧(高吞吐、不打爆下游)。
// 实测延迟基准见 apps/client/test/features/code/code_latency_bench_test.dart。
func runBatcher(id string, in <-chan []byte, onChunk ChunkFn) {
	var batch []byte
	var timer *time.Timer
	var timerC <-chan time.Time

	// 每次都用全新 timer(开销可忽略:空闲时每按键一次、突发时每 16ms 一次),
	// 规避 time.Timer.Reset 在「已触发但未 drain」时的 channel 竞态。
	openWindow := func() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.NewTimer(flushInterval)
		timerC = timer.C
	}
	closeWindow := func() {
		if timer != nil {
			timer.Stop()
		}
		timerC = nil
	}
	flush := func() {
		if len(batch) > 0 {
			onChunk(id, batch)
			batch = nil
		}
	}

	for {
		select {
		case b, ok := <-in:
			if !ok {
				flush()
				closeWindow()
				return
			}
			if timerC == nil {
				// 空闲 → 首字节直发,开观察窗口。
				onChunk(id, b)
				openWindow()
			} else {
				// 窗口内 → 批量;超阈值即时 flush 并重置窗口继续观察突发。
				batch = append(batch, b...)
				if len(batch) >= maxBatchBytes {
					flush()
					openWindow()
				}
			}
		case <-timerC:
			if len(batch) > 0 {
				flush()
				openWindow() // 仍在突发,保持合帧节奏
			} else {
				closeWindow() // 突发结束,回空闲(下次首字节直发)
			}
		}
	}
}

// buildEnv 在 os.Environ() 之上叠加 UTF-8 locale 与终端类型（让 Claude Code /
// Codex 这类 TUI 输出正确转义序列），再追加调用方的 extra env。从 Dock 启动的 GUI
// 子进程环境里没有 locale，多字节输入会乱码，故显式补 LANG/LC_CTYPE。
func buildEnv(extra []string) []string {
	env := os.Environ()
	has := func(prefix string) bool {
		for _, e := range env {
			if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
				return true
			}
		}
		return false
	}
	if !has("LANG=") {
		env = append(env, "LANG=en_US.UTF-8")
	}
	if !has("LC_CTYPE=") {
		env = append(env, "LC_CTYPE=en_US.UTF-8")
	}
	env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
	env = append(env, extra...)
	return env
}

// exitInfo 把 cmd.Wait 的 error 翻成 (exitCode, walltError)。正常退出 → (0,nil)；
// 非零退出 → (code,nil)；wait 自身失败（非 ExitError）→ (-1, err)。
func exitInfo(waitErr error) (int, error) {
	if waitErr == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		return ee.ExitCode(), nil
	}
	return -1, waitErr
}
