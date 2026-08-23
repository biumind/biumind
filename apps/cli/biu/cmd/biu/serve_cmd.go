// `biu serve` — 桌面端嵌入 daemon 入口（S6-1 + S6-2）。
//
// 把现有的 `biu bridge`（HTTP/SSE for IDE/local UI）+ `biu agent worker`
// （NATS poll for brain remote dispatch）合一，外加 PID 文件 + healthz
// 让 Flutter 桌面 app 知道 daemon 是不是健康。
//
// 典型用法（Flutter desktop spawn）：
//
//	biu serve --port 0 --pid-file ~/.biumind/biu.pid
//	  → stdout 打印 `BIU_BRIDGE_URL=http://127.0.0.1:53827`，Flutter 解析拿端口
//	  → SIGTERM 时 graceful：bridge close + deregister
//
// 加 --register 时同时注册成 brain agent_plane environment（biu_daemon
// kind），让远端客户端也能调度本机：
//
//	BIUMIND_PAT=<pat> biu serve --register --brain-url https://your-biumind.example.com
//
// 跟 `biu bridge` / `biu agent worker` 关系：
//   - serve 是 superset；现有两个命令保留作单一职能入口（不 break 老用法）
//   - serve 内部调 bridge.NewServer + agentplane.Worker，没新协议

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/agentplane"
	"github.com/biumind/biumind/apps/cli/biu/internal/bridge"
	"github.com/biumind/biumind/apps/cli/biu/internal/client"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/gitassist"
	"github.com/biumind/biumind/apps/cli/biu/internal/procmgmt"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	"github.com/biumind/biumind/apps/cli/biu/pkg/exechost"
	"github.com/biumind/biumind/packages/go-sdk/biu/llm"
	"github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	"github.com/spf13/cobra"
)

func newServeCmd(f *rootFlags) *cobra.Command {
	var (
		port         int
		bindAddr     string
		authToken    string
		pidFile      string
		register     bool
		brainURL     string
		identityURL  string
		allowedRoots []string
		toolPolicy   string
	)
	c := &cobra.Command{
		Use:   "serve",
		Short: "Run biu as a long-lived daemon (bridge HTTP + healthz + optional brain register)",
		Long: `Combine the bridge HTTP/SSE server (IDE / local UI) with optional
agent_plane registration (so remote clients can dispatch to this machine).

Used by Flutter desktop apps as a child process: spawn 'biu serve --port 0
--pid-file ~/.biumind/biu.pid', parse stdout for BIU_BRIDGE_URL=...`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// daemon 落盘日志 —— Flutter 桌面端 spawn 的子进程 stderr 不落盘,
			// 崩溃 / panic 后无 stack 可查(实测过)。这里把 slog 同时写 stderr
			// + ~/.biu/logs/daemon.log,加上 worker.safeGo 的 panic recover,
			// 下次异常有据可查。
			closeLog := setupDaemonLog()
			defer closeLog()

			cfg, _, err := config.Load(f.cfgPath)
			if err != nil {
				return err
			}
			model := firstNonEmpty(f.model, cfg.Default.Model)

			// PID 文件保护 —— 已有进程 → 拒启；stale 自动清。
			if pidFile != "" {
				if err := acquirePIDFile(pidFile); err != nil {
					return err
				}
				defer releasePIDFile(pidFile)
			}

			// bridge factory：跟 `biu bridge` 同款
			factory := func(extras bridge.AgentExtras) (*biumindkit.Agent, error) {
				return buildSDKAgent(cfg, f, model, extras.PermissionPolicy)
			}
			if probe, err := factory(bridge.AgentExtras{}); err != nil {
				return fmt.Errorf("serve: agent factory: %w", err)
			} else {
				_ = probe.Close()
			}

			brSrv, err := bridge.NewServer(bridge.Options{
				AgentFactory: factory, AuthToken: authToken,
				CommitGenerator: buildCommitGenerator(cfg, f, model),
			})
			if err != nil {
				return err
			}
			addr := bindAddr
			if addr == "" {
				addr = fmt.Sprintf("127.0.0.1:%d", port)
			}
			ln, handler, err := brSrv.Listen(addr)
			if err != nil {
				return err
			}

			// /healthz —— 包一层，把 bridge handler 路由 + 加固定健康检查。
			// Flutter daemon manager 通过 GET /healthz 探活。
			//
			// /metrics —— Prometheus 抓取入口。daemon 端打 cancel SLO /
			// 后续工作量 metrics(暂时只 cancel 一项)。serve label 设为
			// "biu_daemon" 让 brain + daemon 上报到同一组 series 用 service
			// 标签区分。
			metrics.SetService("biu_daemon")
			// tokenSetFn 持 worker.SetToken 闭包; mux handler 在此构建, 但 setFn
			// 在 startAgentWorker (下方) 之后才填充。mutex 保护 handler 读 ↔ 填充写。
			var (
				tokenSetMu sync.Mutex
				tokenSetFn func(string)
			)
			mux := http.NewServeMux()
			mux.Handle("/healthz", http.HandlerFunc(healthzHandler))
			mux.Handle("/metrics", metrics.Handler())
			mux.Handle("/", handler)
			// /internal/token — BiuDaemonManager (Flutter) 推 fresh access_token,
			// 热更 worker.client.token 不重启 daemon (生产 TTL 1h; 不推则 token 过期
			// → worker 401 → daemon 退出 → brain GC environment → environment_id 报错)。
			// loopback 127.0.0.1, 不走 bridge authMiddleware (--auth-token 只护 /v1/code/*)。
			mux.HandleFunc("POST /internal/token", func(w http.ResponseWriter, r *http.Request) {
				tokenSetMu.Lock()
				fn := tokenSetFn
				tokenSetMu.Unlock()
				if fn == nil {
					http.Error(w, "worker not registered", http.StatusNotFound)
					return
				}
				var body struct {
					Token string `json:"token"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "bad json", http.StatusBadRequest)
					return
				}
				if body.Token == "" {
					http.Error(w, "empty token", http.StatusBadRequest)
					return
				}
				fn(body.Token)
				w.WriteHeader(http.StatusOK)
			})
			// 通告解析地址 —— Flutter 解析 stdout 第一行拿 port
			resolved := ln.Addr().String()
			bridgeURL := "http://" + resolved
			fmt.Printf("BIU_BRIDGE_URL=%s\n", bridgeURL)
			os.Stdout.Sync()
			fmt.Fprintf(os.Stderr, "[biu] serve listening on %s (auth=%v, pid_file=%v)\n",
				resolved, authToken != "", pidFile != "")

			ctx, cancel := signal.NotifyContext(context.Background(),
				syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			// app-spawned daemon(--pid-file 由 app 注入)→ 监视父进程死亡自退,
			// 避免 app 退出后遗留孤儿 daemon(macOS 无 PR_SET_PDEATHSIG)。手动
			// `biu serve`(无 --pid-file)不启用,免误退。
			if pidFile != "" {
				watchParentDeath(cancel)
			}

			httpSrv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

			// 启动 HTTP 服 —— 跑在 goroutine，主线程等 ctx 取消
			httpErrCh := make(chan error, 1)
			go func() {
				httpErrCh <- httpSrv.Serve(ln)
			}()

			// 可选：起 agent worker（注册到 brain，long-poll work）
			var (
				stopWorker context.CancelFunc
				workerDone chan struct{}
			)
			if register {
				var setToken func(string)
				stopWorker, workerDone, setToken, err = startAgentWorker(ctx, cfg, f, model, brainURL, identityURL, allowedRoots, toolPolicy)
				if err != nil {
					_ = httpSrv.Shutdown(context.Background())
					return fmt.Errorf("serve --register: %w", err)
				}
				tokenSetMu.Lock()
				tokenSetFn = setToken
				tokenSetMu.Unlock()
			}

			// 等终止信号 / HTTP 退出 / agent worker 自然退出。
			//
			// workerDone 是新增分支:之前 worker.Run 在 startAgentWorker 内
			// 自己起 goroutine,如果 register 失败(token 过期 / network /
			// brain 4xx 等)它静默退出而 daemon HTTP 仍跑成"僵尸 daemon" —
			// daemon manager GET /healthz 还能返 200,客户端以为在跑但 UI
			// 永远显示「无 daemon 在线」。
			// 现在 worker 退出 = daemon 主进程也退出,client 端 exitFuture
			// 一触发自动 respawn(下一次 spawn 用最新 token,循环自愈)。
			select {
			case <-ctx.Done():
				fmt.Fprintln(os.Stderr, "[biu] serve: shutdown signal")
			case err := <-httpErrCh:
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					fmt.Fprintf(os.Stderr, "[biu] serve: http err: %v\n", err)
				}
			case <-workerDoneOrNever(workerDone):
				fmt.Fprintln(os.Stderr,
					"[biu] serve: agent worker exited prematurely, shutting down daemon for client respawn")
			}

			// graceful shutdown：先停 worker（让它 deregister）再关 HTTP
			if stopWorker != nil {
				stopWorker()
				select {
				case <-workerDone:
				case <-time.After(5 * time.Second):
					fmt.Fprintln(os.Stderr, "[biu] serve: worker shutdown timeout")
				}
			}
			// 先杀掉编码模块的活跃 PTY（避免遗留孤儿 agent 子进程），再停 HTTP。
			brSrv.Close()
			shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutCancel()
			_ = httpSrv.Shutdown(shutCtx)
			return nil
		},
	}
	c.Flags().IntVar(&port, "port", 0,
		"port to listen on (0 = OS-assigned; print resolved port to stdout as BIU_BRIDGE_URL=...)")
	c.Flags().StringVar(&bindAddr, "listen", "",
		"explicit listen addr (overrides --port; e.g. 0.0.0.0:8088)")
	c.Flags().StringVar(&authToken, "auth-token", "",
		"bearer token required on every request (empty = no auth, dev only)")
	c.Flags().StringVar(&pidFile, "pid-file", "",
		"PID file path; existing-and-alive process aborts startup. Stale auto-cleared.")
	c.Flags().BoolVar(&register, "register", false,
		"also register as brain agent_plane environment (biu_daemon worker)")
	c.Flags().StringVar(&brainURL, "brain-url", "",
		"brain base URL (default: BIUMIND_BRAIN_URL env or [relay].endpoint)")
	c.Flags().StringVar(&identityURL, "identity-url", "",
		"identity base URL for client-side BYOK key fetch (default: BIUMIND_IDENTITY_URL env or brain-url)")
	c.Flags().StringSliceVar(&allowedRoots, "allowed-roots", nil,
		"filesystem roots this daemon may touch (repeatable); empty = daemon cwd only")
	c.Flags().StringVar(&toolPolicy, "tool-policy", policyWorkspaceWrite,
		"capability floor: readonly | workspace-write | full")
	return c
}

// healthzHandler —— GET /healthz 简单 200 OK，让 Flutter daemon manager
// 探活。后续可加 bridge / worker 状态聚合返回 JSON。
func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true,"service":"biu","mode":"serve"}`))
}

// acquirePIDFile 写当前 PID 到 path,并按持有者状态决定如何获取锁:
//   - stale(进程已死)           → 直接 overwrite。
//   - 存活且是 biu serve          → 终止它并接管(hot restart / 重启遗留的旧
//     daemon)。daemon 是桌面 app 的实现细节,最新实例应拥有它;旧的"拒绝启动"
//     语义恰是 daemon 泄漏后新实例报「未就绪」的根因。pid 文件在 app 私有
//     support 目录、由本 daemon 写,持有者几乎必是自家遗留 daemon。
//   - 存活但不是 biu serve(pid 被无关进程复用)→ 保守拒绝,绝不误杀。
//
// 实现在 internal/procmgmt(与 repo-app runner 共用);这里只做
// biu serve 专属的 reclaim 判定注入。
func acquirePIDFile(path string) error {
	return procmgmt.AcquirePIDFile(path, processIsBiuServe, "a biu serve")
}

// processIsBiuServe 尽力判断 pid 是否一个 biu serve 进程(决定能否安全接管锁)。
// 用 ps 读命令行;ps 不可用 / 进程已消失 → 返回 false(调用方据此保守不杀)。
func processIsBiuServe(pid int) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	cmd := string(out)
	return strings.Contains(cmd, "biu") && strings.Contains(cmd, "serve")
}

// terminatePID 优雅终止 pid:先 SIGTERM 等最多 ~2s,仍在则 SIGKILL。
// 实现已提升到 internal/procmgmt。
func terminatePID(pid int) {
	procmgmt.TerminatePID(pid)
}

// watchParentDeath 监视父进程(spawn 本 daemon 的 app 引擎进程):父进程死亡后
// 子进程被 reparent(macOS → launchd pid 1),ppid 随之变化 → 触发 cancel 走优雅
// 关停。只治 clean quit / 崩溃(此时引擎进程真的没了);hot restart 时引擎存活、
// ppid 不变,不会误退(那条路径由 acquirePIDFile 接管处理)。
func watchParentDeath(cancel context.CancelFunc) {
	initial := os.Getppid()
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for range t.C {
			if cur := os.Getppid(); cur != initial {
				fmt.Fprintf(os.Stderr, "[biu] serve: parent process gone (ppid %d→%d), shutting down\n",
					initial, cur)
				cancel()
				return
			}
		}
	}()
}

// releasePIDFile 删 PID 文件。defer 调用；err 仅 log。
// 实现已提升到 internal/procmgmt。
func releasePIDFile(path string) {
	procmgmt.ReleasePIDFile(path)
}

// processAlive 用 syscall 0 信号探活（不发实际信号，只查权限）。Unix-only
// 语义；Windows 上 Process 接口同样支持但语义不同 —— 当前只在桌面 macOS /
// Linux 部署，Windows 通过 launchd 等价物处理。
// 实现已提升到 internal/procmgmt。
func processAlive(pid int) bool {
	return procmgmt.ProcessAlive(pid)
}

// setupDaemonLog 把 slog 默认 handler 指向 stderr + ~/.biu/logs/daemon.log。
// 桌面端 spawn 的 daemon stderr 只被 app 转发进内存 logger(不落盘),崩溃后
// 无 stack。这里给 daemon 一份持久日志(含 worker.safeGo 捕获的 goroutine
// panic stack + handleWork recover 的引擎 panic)。打不开文件不致命 → 退回
// 纯 stderr。日志级别可经 BIUMIND_LOG_LEVEL=debug 调高(排查 daemon 崩溃时
// 看 work/permission 时序)。返回 close fn,daemon 退出时关文件。
func setupDaemonLog() func() {
	noop := func() {}
	home, err := os.UserHomeDir()
	if err != nil {
		return noop
	}
	dir := filepath.Join(home, ".biu", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return noop
	}
	path := filepath.Join(dir, "daemon.log")
	// 简单按大小滚动:>10MB 截断重开。单文件,不引滚动库。
	if fi, statErr := os.Stat(path); statErr == nil && fi.Size() > 10<<20 {
		_ = os.Truncate(path, 0)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return noop
	}
	level := slog.LevelInfo
	if strings.EqualFold(os.Getenv("BIUMIND_LOG_LEVEL"), "debug") {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(
		io.MultiWriter(os.Stderr, f),
		&slog.HandlerOptions{Level: level},
	)))
	slog.Info("biu serve: daemon log initialized", "path", path, "level", level.String())
	return func() { _ = f.Close() }
}

// workerDoneOrNever 抹平 register=false 时 workerDone == nil 的差异 ——
// nil channel 永不就绪,select 分支静默挂着;非 nil 跟原 channel 一致。
// 让 select 语句不需要在 register / no-register 两种模式下分别写。
func workerDoneOrNever(ch chan struct{}) <-chan struct{} {
	if ch == nil {
		return nil // nil channel: select case 永不触发
	}
	return ch
}

// buildCommitGenerator 构造编码模块 AI commit msg 的 LLM 缝 —— 复用 cloud-mode
// 同款 model-relay provider(满足 I6:daemon 不直连 LLM,过 model-relay)。无 relay
// URL / token 时返回 nil,Service 会对 generateCommitMessage 回明确错误而非崩溃。
// 复用 device token / PAT / virtual_key 的解析链,与 SDK agent / agent worker 一致。
func buildCommitGenerator(cfg *config.Config, f *rootFlags, model string) gitassist.Generator {
	relayURL := firstNonEmpty(f.relayURL, os.Getenv("BIUMIND_MODEL_RELAY_URL"), cfg.Relay.Endpoint)
	token := firstNonEmpty(
		os.Getenv("BIUMIND_DEVICE_TOKEN"), loadDeviceToken(),
		f.token, os.Getenv("BIUMIND_TOKEN"), os.Getenv("BIUMIND_PAT"), cfg.Relay.VirtualKey,
	)
	if relayURL == "" || token == "" {
		return nil
	}
	if model == "" {
		model = "claude-opus-4-8"
	}
	p := client.New(relayURL, token)
	return func(ctx context.Context, prompt string) (string, error) {
		frames, err := p.ChatStream(ctx, client.ChatRequest{
			Model:     model,
			Messages:  []client.Message{{Role: "user", Content: prompt}},
			MaxTokens: 2048,
		})
		if err != nil {
			return "", err
		}
		return llm.CollectText(frames)
	}
}

// startAgentWorker 起 agent_plane worker —— 跟 `biu agent worker` 同款配置
// 路径，但跑在 serve 主进程内部。返回 cancel + done channel；caller 用
// cancel 触发 graceful 停（worker.Run 内部 deregister）。
func startAgentWorker(parent context.Context, cfg *config.Config, f *rootFlags, model, brainURL, identityURL string, allowedRoots []string, toolPolicy string) (
	context.CancelFunc, chan struct{}, func(string), error,
) {
	// R6.1: device token（biu pair 配对得来，scoped+可吊销）优先于 PAT。
	pat := firstNonEmpty(
		os.Getenv("BIUMIND_DEVICE_TOKEN"), loadDeviceToken(),
		os.Getenv("BIUMIND_PAT"), f.token, os.Getenv("BIUMIND_TOKEN"), cfg.Relay.VirtualKey,
	)
	if pat == "" {
		return nil, nil, nil, errors.New("no credential (run `biu pair`, or set BIUMIND_PAT / --token)")
	}
	if brainURL == "" {
		brainURL = firstNonEmpty(os.Getenv("BIUMIND_BRAIN_URL"), cfg.Relay.Endpoint)
	}
	if brainURL == "" {
		return nil, nil, nil, errors.New("missing brain URL (set BIUMIND_BRAIN_URL or [relay].endpoint)")
	}
	// identity URL (client-side BYOK key 取): flag > env > brain-url (单 origin
	// 寻址, identity = brain 同 host 经 nginx 路径反代). 缺省同 brainURL.
	identityURL = firstNonEmpty(identityURL, os.Getenv("BIUMIND_IDENTITY_URL"), brainURL)
	idClient := agentplane.NewIdentityBYOKClient(identityURL)

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "biu-daemon"
	}
	// R6.3 / D7：同 agent_cmd——路径根（空→cwd）+ 能力 preset 硬地板。
	roots := resolveAllowedRoots(allowedRoots)
	daemonPreset := normalizePreset(toolPolicy)
	fmt.Fprintf(os.Stderr, "[biu] serve: tool floor: policy=%s roots=%v\n", daemonPreset, roots)
	builder := func(ctx context.Context, work agentplane.WorkPayload, askPerm biumindkit.PermissionPolicyFn) (*biumindkit.Agent, error) {
		// askPerm 走 brain control queue 反向问 client (见 worker.askPermissionFor)。
		// work.UserBearer (P4) 是 brain 投下来的委托 user JWT, 优先作 model-relay
		// Authorization → relay 原生解析该 user 的 BYOK。空 → 回退 BIUMIND_TOKEN/PAT。
		preset := intersectPreset(daemonPreset, work.ToolPolicy)
		floor := resolveToolFloor(preset)
		if work.Workdir != "" && !withinRoots(work.Workdir, roots) {
			return nil, fmt.Errorf("work.Workdir %q outside allowed roots %v", work.Workdir, roots)
		}
		cwd := work.Workdir
		if cwd == "" {
			cwd = roots[0]
		}
		opts := []buildSDKAgentOption{
			withCwd(cwd),
			withBearer(work.UserBearer),
			withExecHost(exechost.For(work.RuntimeEnvMode)),
			withAllowedRoots(roots),
			withToolFloor(floor),
			// §8.2 翻案:brain 服务端组装的 prior 多轮 → PriorMessages,
			// 让 agent 模式不再单轮(否则"你还没问过我")。
			withPriorMessages(priorMessagesFromHistory(work.History)),
		}
		// client-side BYOK 命中 (brain 透传 ClientSideRecordID) → 用 work.UserBearer
		// (brain 透传 user JWT) 调 identity 取明文 key (统一加密存 identity),
		// withClientSide 让 buildSDKAgent 跳 relay 本机直连上游. 取失败 (record
		// 不存在 / revoked / 网络断) → 不加 opt → 落 relay fallback (relay 连不到
		// 内网 proxy 则上游失败, 符合预期).
		if work.ClientSideRecordID != "" {
			if cred, err := idClient.GetCredential(ctx, work.UserBearer, work.ClientSideRecordID); err == nil {
				opts = append(opts, withClientSide(cred.Key, cred.BaseURL, cred.Protocol))
			} else if !errors.Is(err, agentplane.ErrIdentityCredentialNotFound) {
				fmt.Fprintf(os.Stderr, "[biu] serve: 取 client-side 凭据失败: %v\n", err)
			}
		}
		return buildSDKAgent(cfg, f, firstNonEmpty(work.Model, model),
			floorPolicy(roots, floor, askPerm), opts...)
	}
	// R6.2：X25519 密钥对（见 agent_cmd.go 同款逻辑）。失败回退明文 BYOK。
	privkey, pubkey, kerr := loadOrCreateKeypair()
	if kerr != nil {
		fmt.Fprintf(os.Stderr, "[biu] serve: 警告：X25519 密钥加载失败，BYOK 将走明文：%v\n", kerr)
		privkey, pubkey = nil, nil
	}

	client := agentplane.NewClient(brainURL, pat, nil)
	worker := agentplane.NewWorker(client, agentplane.WorkerConfig{
		RunnerBuilder:   cliRunnerBuilder,
		EnvironmentName: hostname,
		Capabilities:    []string{"sandbox"},
		PublicKey:       pubkey,
		Privkey:         privkey,
		// B2: 注册成功（含 re-register）后 stdout 打印 env_id，让 Flutter
		// BiuDaemonManager 持有本机 daemon env_id（client-side BYOK 命中时
		// 定向投 work 到本机，与 loopback 推的 key 同机）。
		OnRegistered: func(envID string) {
			fmt.Printf("BIU_DAEMON_ENV_ID=%s\n", envID)
			os.Stdout.Sync()
		},
	}, builder, slog.Default())

	// setToken: bridge POST /internal/token 调, 热更 client.token (worker.SetToken
	// 转发)。闭包捕获 worker, worker.Run 在下方 goroutine 里活着, 闭包长期有效。
	setToken := func(tok string) {
		worker.SetToken(tok)
	}

	wctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := worker.Run(wctx); err != nil {
			fmt.Fprintf(os.Stderr, "[biu] serve: agent worker exited: %v\n", err)
		}
	}()
	fmt.Fprintf(os.Stderr, "[biu] serve: agent worker registered as %s -> %s\n",
		hostname, brainURL)
	return cancel, done, setToken, nil
}
