// WebSocket transport for the biu bridge.
//
// 每条 biumindkit.Event 走 toSDKFrame() 翻译成 sdkproto.Frame（SDK Protocol v1
// wire 类型），直接 marshal 成 JSON 推给客户端 —— **裸帧**，没有外层 envelope。
// 跟 SDK Protocol v1 service.json 的 StdoutMessage union 完全对齐：客户端
// 用 `sdkproto.UnmarshalFrame()` 即可解析。
//
// 双向：客户端可以发 SDKControlRequest（interrupt / cancel_async / set_model
// 等），read pump 解析后调 sessionRec.dispatchControl 路由到对应动作，回写
// SDKControlResponse 给客户端。read pump 跟 main push loop 都写同一个 conn ——
// 用 writeMu 串行化，避免 gorilla 抛 concurrent write panic。
//
// 心跳：Ping/Pong 由 gorilla 内置，每 30s 一次 ping，60s 收不到 pong 切断。
//
// Resume：客户端连接时带 `?last_event_id=N`，server 先 replay ring buffer 里
// id > N 的事件再进入实时推流。

package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
	"github.com/gorilla/websocket"
)

// wsUpgrader 配置：放开同源限制（biu bridge 是 loopback 工具，CSRF 不是威胁
// 模型；外网部署用 reverse proxy 加严）。读 buffer 16 KB 容纳大 tool result，
// 写 buffer 4 KB 够普通 frame；都被 gorilla 复用不分配。
var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  16 * 1024,
	WriteBufferSize: 4 * 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// wsTimeouts 集中放各种超时常量 —— 调试时一处改。
const (
	wsPingPeriod = 30 * time.Second // server 主动发 ping 间隔
	wsPongWait   = 60 * time.Second // 收 pong 超时（必须 > pingPeriod）
	wsWriteWait  = 10 * time.Second // 单帧写超时
)

// wsHandler 升级 HTTP → WS。流程：
//  1. session 查找 + ch 快照 + lastSeen 解析
//  2. 升级到 WS，启动 read pump（解析入站 control_request 并 dispatch）
//  3. replay ring buffer 里 id > lastSeen 的帧
//  4. 进入实时推流 + 心跳；ch close 时发 KeepAlive sentinel 然后关连接
func (s *Server) wsHandler(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.lookup(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "no such session")
		return
	}
	lastSeen := parseLastEventID(r)

	rec.mu.Lock()
	ch := rec.eventCh
	rec.mu.Unlock()
	if ch == nil && lastSeen == 0 {
		// 没在跑 turn 又没 resume cursor —— 没东西可推。409 让 IDE 区分
		// "session idle" vs "stream success but empty"。
		writeErr(w, http.StatusConflict, "no turn in progress")
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrader 已写 4xx response；只在 dev 环境跑，无需日志。
		return
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	// writeMu 串行化所有发往 conn 的 write。read pump（处理 control_request）
	// 跟 main loop（推 stream 帧）都走 writeFrame —— 不锁会触发 gorilla 的
	// "concurrent write" panic。
	var writeMu sync.Mutex
	writeFrame := func(frame sdkproto.Frame) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return writeSDKFrame(conn, frame)
	}

	// Read pump：解析每条入站 JSON 帧，按类型 dispatch。
	//   - SDKControlRequest → rec.dispatchControl(req) → SDKControlResponse 回写
	//   - 其他类型（ControlCancelRequest / SDKMessage / Lifecycle）暂时 ignore，
	//     等 S2-3 / S4 实化
	closeRead := make(chan struct{})
	go func() {
		defer close(closeRead)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				// 客户端断开或 protocol error —— 通知 main loop 退出
				return
			}
			frame, err := sdkproto.UnmarshalFrame(msg)
			if err != nil {
				// 收到不认识的帧不是 fatal —— 忽略，让客户端继续工作。
				continue
			}
			switch f := frame.(type) {
			case *sdkproto.SDKControlRequest:
				// client 发起 control（interrupt / cancel / set_model 等）
				response := rec.dispatchControl(f)
				_ = writeFrame(response)
			case *sdkproto.SDKControlResponse:
				// client 回 server 之前发的 can_use_tool —— 唤醒 askPermission
				rec.answerPermission(f)
			default:
				// SDKMessage / Lifecycle / ControlCancelRequest 暂不处理；
				// S2-3+ 时按需补
			}
		}
	}()

	// 1. Replay buffered 帧 —— ring buffer 直接存 sdkproto.Frame，replay 跟
	// 实时推流走完全一样的 wire 编码路径。
	for _, e := range rec.since(lastSeen) {
		if err := writeFrame(e.Frame); err != nil {
			return
		}
	}

	// 2. ch == nil 走 short-circuit：客户端追上历史就关闭
	if ch == nil {
		_ = writeFrame(&sdkproto.KeepAlive{Type: sdkproto.TypeKeepAlive, TS: time.Now().UnixMilli()})
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"),
			time.Now().Add(wsWriteWait),
		)
		return
	}

	// 3. 进入实时推流 + 心跳。submit() goroutine 已经把 biumindkit.Event
	// 翻成 sdkproto.Frame 推到 ch，这一层只负责传输。
	pingTicker := time.NewTicker(wsPingPeriod)
	defer pingTicker.Stop()

	for {
		select {
		case frame, open := <-ch:
			if !open {
				// 当前 turn 跑完。SDKResultSuccess 已经走 ch 推过去；
				// 再发个 KeepAlive 表示流正常结束，然后关连接。
				_ = writeFrame(&sdkproto.KeepAlive{Type: sdkproto.TypeKeepAlive, TS: time.Now().UnixMilli()})
				_ = conn.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"),
					time.Now().Add(wsWriteWait),
				)
				return
			}
			if err := writeFrame(frame); err != nil {
				return
			}
		case <-pingTicker.C:
			// Ping 走 control message 路径，不经 writeFrame —— gorilla
			// 内部 WriteControl 跟 WriteMessage 共享一个 write lock，
			// 这里再加自己的 mu 反而死锁。注意：ping 的并发安全 gorilla
			// 已经保证（WriteControl 是 thread-safe）。
			if err := conn.WriteControl(
				websocket.PingMessage, nil, time.Now().Add(wsWriteWait),
			); err != nil {
				return
			}
		case <-closeRead:
			// 客户端断开
			return
		case <-r.Context().Done():
			return
		}
	}
}

// writeSDKFrame 序列化 sdkproto.Frame + 发一个 WS text frame。Frame 接口的
// MarshalJSON 由具体类型自定义（比如 SDKControlRequest 有自己的）—— 这里直接
// json.Marshal 拿到 wire 字节。**调用方负责并发同步**（wsHandler 里 writeMu）。
func writeSDKFrame(conn *websocket.Conn, frame sdkproto.Frame) error {
	if conn == nil {
		return errors.New("nil conn")
	}
	conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
	buf, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return conn.WriteMessage(websocket.TextMessage, buf)
}
