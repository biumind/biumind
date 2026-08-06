// Codex 用量 —— 通过 `codex app-server` 的 JSON-RPC(over stdin/stdout)拿账户 +
// 限流窗口。
//
// 握手:initialize(id=1) → 等结果 → initialized 通知。随后 account/read +
// account/rateLimits/read。整个流程在一次进程生命周期内完成,读完即 kill —— 不做
// 长驻 RPC 连接池(用量是低频按需,每次新起进程更简单也更稳)。
//
// ⚠️ 本机未装 codex 时无法真机验证(与 task #21 同一阻塞);DetectPath 失败 →
// unavailable。RPC 形状按 codex CLI 实际接口盲写,接口若变需在此集中调整。
package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/code/agent"
)

func readCodex(ctx context.Context) Source {
	path, err := agent.DetectPath("codex")
	if err != nil {
		return unavailable("未找到 codex 二进制;装上 codex CLI 后可见用量。")
	}

	client, err := spawnCodex(ctx, path)
	if err != nil {
		return unavailable("启动 codex app-server 失败:" + err.Error())
	}
	defer client.close()

	account, err := client.call(ctx, "account/read", json.RawMessage(`{}`))
	if err != nil {
		return unavailable("读取 Codex 账户失败:" + err.Error())
	}
	// rateLimits 先试 null 参数,失败回退 {}(两次尝试以兼容不同 codex 版本)。
	rateLimits, err := client.call(ctx, "account/rateLimits/read", json.RawMessage(`null`))
	if err != nil {
		if rl, e2 := client.call(ctx, "account/rateLimits/read", json.RawMessage(`{}`)); e2 == nil {
			rateLimits = rl
		} else {
			return unavailable("读取 Codex 限流失败:" + err.Error())
		}
	}

	return available(parseCodexUsage(account, rateLimits))
}

// codexClient 是一次性的 JSON-RPC 通道。
type codexClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan rpcLine
	nextID int
}

type rpcLine struct {
	val json.RawMessage
	err error
}

func spawnCodex(ctx context.Context, path string) (*codexClient, error) {
	cmd := exec.Command(path, "app-server")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	c := &codexClient{cmd: cmd, stdin: stdin, lines: make(chan rpcLine, 16), nextID: 2}

	// 后台:逐行读 stdout → channel。
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			cp := make([]byte, len(line))
			copy(cp, line)
			c.lines <- rpcLine{val: cp}
		}
		if err := sc.Err(); err != nil {
			c.lines <- rpcLine{err: fmt.Errorf("读 codex app-server 输出失败:%w", err)}
		}
		close(c.lines)
	}()
	// 后台:drain stderr,免得子进程写满管道阻塞。
	go func() { _, _ = io.Copy(io.Discard, stderr) }()

	if err := c.handshake(ctx); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

func (c *codexClient) handshake(ctx context.Context) error {
	if err := c.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"clientInfo":   map[string]any{"name": "biumind", "version": "1.0.0"},
			"capabilities": map[string]any{},
		},
	}); err != nil {
		return err
	}
	if _, err := c.waitFor(ctx, 1); err != nil {
		return err
	}
	return c.writeJSON(map[string]any{"jsonrpc": "2.0", "method": "initialized"})
}

func (c *codexClient) call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	id := c.nextID
	c.nextID++
	if err := c.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		return nil, err
	}
	return c.waitFor(ctx, id)
}

func (c *codexClient) writeJSON(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("序列化 Codex 请求失败:%w", err)
	}
	if _, err := c.stdin.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("写 Codex 请求失败:%w", err)
	}
	return nil
}

// waitFor 阻塞等到匹配 id 的响应,返回其 result;命中 error 字段或超时则报错。
func (c *codexClient) waitFor(ctx context.Context, expectedID int) (json.RawMessage, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("等 Codex 响应 %d 超时", expectedID)
		case line, ok := <-c.lines:
			if !ok {
				return nil, fmt.Errorf("codex app-server 在响应 %d 前关闭", expectedID)
			}
			if line.err != nil {
				return nil, line.err
			}
			var msg struct {
				ID     *int            `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(line.val, &msg); err != nil {
				continue // 非 JSON-RPC 响应行(通知等),跳过
			}
			if msg.ID == nil || *msg.ID != expectedID {
				continue
			}
			if msg.Error != nil {
				return nil, fmt.Errorf("%s", msg.Error.Message)
			}
			if len(msg.Result) == 0 {
				return nil, fmt.Errorf("Codex 响应 %d 既无 result 也无 error", expectedID)
			}
			return msg.Result, nil
		}
	}
}

func (c *codexClient) close() {
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_ = c.cmd.Wait()
}

// parseCodexUsage 从 account/read + rateLimits 结果抽 CodexData。
func parseCodexUsage(account, rateLimits json.RawMessage) CodexData {
	var acc struct {
		Account struct {
			Email    *string `json:"email"`
			PlanType *string `json:"planType"`
		} `json:"account"`
	}
	_ = json.Unmarshal(account, &acc)

	src := codexRateLimitSource(rateLimits)
	var windows struct {
		Primary   json.RawMessage `json:"primary"`
		Secondary json.RawMessage `json:"secondary"`
	}
	_ = json.Unmarshal(src, &windows)

	return CodexData{
		Email:     acc.Account.Email,
		PlanType:  acc.Account.PlanType,
		Primary:   parseCodexWindow(windows.Primary),
		Secondary: parseCodexWindow(windows.Secondary),
	}
}

// codexRateLimitSource 解开 rateLimits 的嵌套:优先 rateLimitsByLimitId.codex,
// 否则该 map 的任意一项,再否则顶层 rateLimits(多级 fallback 兼容不同返回形状)。
func codexRateLimitSource(rateLimits json.RawMessage) json.RawMessage {
	var top struct {
		ByLimitID  map[string]json.RawMessage `json:"rateLimitsByLimitId"`
		RateLimits json.RawMessage            `json:"rateLimits"`
	}
	if err := json.Unmarshal(rateLimits, &top); err == nil {
		if len(top.ByLimitID) > 0 {
			if v, ok := top.ByLimitID["codex"]; ok {
				return v
			}
			for _, v := range top.ByLimitID {
				return v
			}
		}
		if len(top.RateLimits) > 0 {
			return top.RateLimits
		}
	}
	return rateLimits
}
