// 编码模块（BiuMind Code）的 WebSocket 端点 —— GET /v1/code/ws。
//
// 与 chat 的 ws.go 的关键差异：
//   - **非 turn-gated**：socket 一连即开、长驻；不像 chat 那样「无 turn 在跑就 409」。
//     PTY 是长生命周期会话，不是一问一答的 turn。
//   - **单写 goroutine + 缓冲 channel**：所有出站帧（code_response / code_pty_chunk /
//     code_pty_exit / ping）都经一个 `out` channel 由唯一的 write pump 写 conn。
//     比 chat 的 writeMu 更适合 PTY 的多写者（read pump 回响应 + 多个 batcher
//     goroutine 推 chunk），且 channel 满 → emit 阻塞 → batcher 阻塞 → reader 阻塞
//     → OS PTY 缓冲填满 → agent write() 阻塞，**把 源头反压一路延伸到 WS**。
//   - **无 ring buffer/replay**：PTY 是实时流，M0 不做断线重放；resume/re-attach 留 M2/M3。
//
// 帧路由（read pump）：
//   - code_request   → Service.Dispatch（git/fs 同步、pty.open 注册流）→ code_response
//   - code_pty_input → Service.Input（高频，绕过 RPC 信封，）
//   - code_pty_resize→ Service.Resize（）

package bridge

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
	"github.com/gorilla/websocket"
)

// codeOutBuffer 是出站帧 channel 容量。给得较大以容纳 PTY 突发输出；满了会触发
// 反压链（见文件头注释），不丢帧。
const codeOutBuffer = 256

// wsEmitter 是连接级 PtyEmitter —— 把某 PTY 的输出/退出包成帧塞进 out。
// done 关闭后 send 立即返回（连接已断，丢弃后续帧，不阻塞 batcher）。
type wsEmitter struct {
	out  chan sdkproto.Frame
	done chan struct{}
}

func (e *wsEmitter) send(f sdkproto.Frame) {
	select {
	case e.out <- f:
	case <-e.done:
	}
}

func (e *wsEmitter) Chunk(ptyID string, data []byte) {
	e.send(&sdkproto.CodePtyChunk{Type: sdkproto.TypeCodePtyChunk, PtyID: ptyID, Data: data})
}

func (e *wsEmitter) Exit(ptyID string, code int, errMsg string) {
	e.send(&sdkproto.CodePtyExit{Type: sdkproto.TypeCodePtyExit, PtyID: ptyID, ExitCode: code, Err: errMsg})
}

func (e *wsEmitter) SessionEvent(taskID string, event map[string]any) {
	raw, err := json.Marshal(event)
	if err != nil {
		return // 解析出的事件理应可序列化;失败则丢弃该条,不拆连接
	}
	e.send(&sdkproto.CodeSessionEvent{Type: sdkproto.TypeCodeSessionEvent, TaskID: taskID, Event: raw})
}

// codeWS 升级 HTTP → WS 并驱动编码模块的帧循环。
func (s *Server) codeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrader 已写 4xx；dev/loopback 环境无需日志。
		return
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	emit := &wsEmitter{
		out:  make(chan sdkproto.Frame, codeOutBuffer),
		done: make(chan struct{}),
	}
	var closeOnce sync.Once
	closeDone := func() { closeOnce.Do(func() { close(emit.done) }) }
	defer closeDone()

	// 单写 goroutine：唯一触碰 conn 写路径的地方。出站帧 + 心跳都从这里发。
	go func() {
		ping := time.NewTicker(wsPingPeriod)
		defer ping.Stop()
		for {
			select {
			case f := <-emit.out:
				if err := writeSDKFrame(conn, f); err != nil {
					closeDone()
					return
				}
			case <-ping.C:
				// WriteControl 是 gorilla 内部 thread-safe，但本端只有这一个写
				// goroutine，无并发顾虑。
				if err := conn.WriteControl(
					websocket.PingMessage, nil, time.Now().Add(wsWriteWait),
				); err != nil {
					closeDone()
					return
				}
			case <-emit.done:
				return
			}
		}
	}()

	// Read pump（当前 goroutine）。解析每条入站帧并路由。
	for {
		_, msg, rerr := conn.ReadMessage()
		if rerr != nil {
			return // 客户端断开 / 协议错误 → defer closeDone 收尾
		}
		frame, perr := sdkproto.UnmarshalFrame(msg)
		if perr != nil {
			continue // 不认识的帧不致命，忽略
		}
		switch f := frame.(type) {
		case *sdkproto.CodeRequest:
			// Dispatch 可能阻塞（git/fs 跑子进程、pty.open 拉起进程），放 goroutine
			// 里跑，让 read pump 继续 drain 输入 —— pty_input/resize 等高频控制帧
			// 不能被一条慢 git.status 卡住。响应按 request_id 关联，乱序无碍。
			// 注：M0 loopback 受信，不限并发；远控（M6）经 agent-plane 时再加配额。
			go func(req *sdkproto.CodeRequest) {
				resp := s.code.Dispatch(r.Context(), req, emit)
				emit.send(resp)
			}(f)
		case *sdkproto.CodePtyInput:
			_ = s.code.Input(f.PtyID, f.Data)
		case *sdkproto.CodePtyResize:
			_ = s.code.Resize(f.PtyID, f.Cols, f.Rows)
		default:
			// code_response / code_pty_chunk / code_pty_exit 是出站方向；
			// 其它平面帧（control/chat/lifecycle）在 code-WS 上无意义，忽略。
		}
	}
}
