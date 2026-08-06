// `biu agent worker` — biu daemon mode（S3-8）。
//
// 把当前机器注册成 brain Agent Plane 的一个 environment（worker_kind=
// biu_daemon），long-poll work，跑 biumindkit Agent 处理，把帧推回
// brain 让客户端看到流。
//
// 跟 `biu bridge` 区别：
//   - bridge：本机 IDE 驱动 biu，bridge 暴露 HTTP+WS 等 IDE 来连
//   - agent worker：biu 反过来连 brain，从远端 client（Flutter app /
//     web）触发的任务被推到本机执行
//
// 运行：
//
//	BIUMIND_PAT=<pat> biu agent worker --brain-url https://your-biumind.example.com

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/biumind/biumind/apps/cli/biu/internal/agentplane"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	"github.com/biumind/biumind/apps/cli/biu/pkg/exechost"
	agentpkg "github.com/biumind/biumind/packages/go-sdk/biu/agent"
	"github.com/spf13/cobra"
)

// cliRunnerBuilder 按 WorkPayload.Backend 返回外部 CLI Runner（Runtime v3 R3/
// Q3）。Claude Code（R3）+ Codex（R8）；其余 / 非 CLI backend 返 nil → worker
// 落到内建 biumindkit 路径。agent_cmd + serve_cmd 共用。
//
// runExternalBackend 用 ResolveBackend(payload.Backend) 取 ClearEnv/Model（A1：
// 清平台 key 让 CLI 用用户自己的订阅；codex 清 OPENAI_API_KEY）——backend 无关，
// 这里只负责挑对应 adapter。
func cliRunnerBuilder(work agentplane.WorkPayload) agentpkg.Runner {
	switch work.Backend {
	case agentpkg.ClaudeCodeBackend.ID:
		return agentpkg.NewClaudeCode(agentpkg.ClaudeCodeBackend.Command)
	case agentpkg.CodexBackend.ID:
		return agentpkg.NewCodex(agentpkg.CodexBackend.Command)
	}
	return nil
}

func newAgentCmd(f *rootFlags) *cobra.Command {
	c := &cobra.Command{
		Use:   "agent",
		Short: "BiuMind Agent Plane integration (S3-8)",
		Long: `Connect this machine to a brain Agent Plane as a biu_daemon worker.
Brain dispatches work over JetStream → biu daemon picks it up → runs the
biumindkit Agent locally → publishes frames back to brain.`,
	}
	c.AddCommand(newAgentWorkerCmd(f))
	return c
}

func newAgentWorkerCmd(f *rootFlags) *cobra.Command {
	var (
		brainURL     string
		machineName  string
		poolTag      string
		allowedRoots []string
		toolPolicy   string
	)
	c := &cobra.Command{
		Use:   "worker",
		Short: "Run as a biu_daemon worker against a brain Agent Plane",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.Load(f.cfgPath)
			if err != nil {
				return err
			}

			// R6.1: device token（biu pair 配对得来，scoped+可吊销）优先于 PAT。
			pat := firstNonEmpty(
				os.Getenv("BIUMIND_DEVICE_TOKEN"), loadDeviceToken(),
				os.Getenv("BIUMIND_PAT"), f.token, os.Getenv("BIUMIND_TOKEN"), cfg.Relay.VirtualKey,
			)
			if pat == "" {
				return errors.New("agent worker: no credential (run `biu pair`, or set BIUMIND_PAT / --token)")
			}
			if brainURL == "" {
				brainURL = firstNonEmpty(os.Getenv("BIUMIND_BRAIN_URL"), cfg.Relay.Endpoint)
			}
			if brainURL == "" {
				return errors.New("agent worker: missing brain URL (set BIUMIND_BRAIN_URL or [relay] endpoint)")
			}
			if machineName == "" {
				h, _ := os.Hostname()
				machineName = h
				if machineName == "" {
					machineName = "biu-daemon"
				}
			}

			// R6.3 / D7：解析路径根（空 → daemon cwd，安全默认非无界）+ 能力 preset。
			roots := resolveAllowedRoots(allowedRoots)
			daemonPreset := normalizePreset(toolPolicy)
			fmt.Fprintf(os.Stderr, "[biu] tool floor: policy=%s roots=%v\n", daemonPreset, roots)

			// 路径地板的单一实现：空 Workdir → 第一个根（约束相对路径工具）；
			// 越界 → 拒该 work。biumindkit 路径（builder 闭包）与外部 CLI backend
			// 路径（worker.runExternalBackend）共用它，避免两条路径校验漂移。
			resolveWorkdir := func(wd string) (string, error) {
				if wd == "" {
					return roots[0], nil
				}
				if !withinRoots(wd, roots) {
					return "", fmt.Errorf("work.Workdir %q outside allowed roots %v", wd, roots)
				}
				return wd, nil
			}

			model := firstNonEmpty(f.model, cfg.Default.Model)
			builder := func(ctx context.Context, work agentplane.WorkPayload, askPerm biumindkit.PermissionPolicyFn) (*biumindkit.Agent, error) {
				// R6.3：有效 preset = daemon flag ∩ brain per-device stamp（取更严，
				// flag 是上限 brain 只能收窄）。floor → 能力地板；roots → 路径地板。
				preset := intersectPreset(daemonPreset, work.ToolPolicy)
				floor := resolveToolFloor(preset)
				// Workdir 越界 → 拒绝该 work（不静默 clamp）。空 Workdir 用 roots[0]。
				cwd, werr := resolveWorkdir(work.Workdir)
				if werr != nil {
					return nil, werr
				}
				// Per-work agent。每条 work 一个 fresh agent,简化生命周期。
				//
				// askPerm 由 worker 注入 —— 把 PermissionAsk 通过 publishFrame
				// 推到 .out NATS subject(brain 透传到 client WS),client 答复
				// 经 brain control queue 投回 worker.answerPermission。这条链
				// 接通后,worker 模式不再 deny 兜底,工具调用真能走到 client。
				//
				// work.Workdir 由 brain 从 chat.threads.workdir 透传:让 daemon
				// 在指定目录跑工具。空 → fall back 到 daemon 启动 cwd。
				//
				// work.UserBearer (P4) 是 brain 投下来的委托 user JWT, 优先作
				// model-relay Authorization → relay 原生解析该 user 的 BYOK。
				// 无值 → 老路径(BIUMIND_TOKEN 走平台池)。
				return buildSDKAgent(
					cfg, f,
					firstNonEmpty(work.Model, model),
					floorPolicy(roots, floor, askPerm),
					withCwd(cwd),
					withBearer(work.UserBearer),
					withExecHost(exechost.For(work.RuntimeEnvMode)),
					withAllowedRoots(roots),
					withToolFloor(floor),
					// §8.2 翻案:brain 服务端组装的 prior 多轮 → PriorMessages。
					withPriorMessages(priorMessagesFromHistory(work.History)),
				)
			}

			// R6.2：X25519 密钥对——pubkey 上报 brain（P4 前 BYOK 信封加密用，
			// 现保留给 S3-4 mcp_config）。生成失败不阻断（回退明文，仅告警）。
			privkey, pubkey, kerr := loadOrCreateKeypair()
			if kerr != nil {
				fmt.Fprintf(os.Stderr, "[biu] 警告：X25519 密钥加载失败，BYOK 将走明文：%v\n", kerr)
				privkey, pubkey = nil, nil
			}

			client := agentplane.NewClient(brainURL, pat, nil)
			worker := agentplane.NewWorker(client, agentplane.WorkerConfig{
				EnvironmentName: machineName,
				PoolTag:         poolTag,
				Capabilities:    []string{"sandbox"}, // 占位；S3-4 启用 X25519 + tools 后扩
				RunnerBuilder:   cliRunnerBuilder,
				PublicKey:       pubkey,
				Privkey:         privkey,
				ResolveWorkdir:  resolveWorkdir,
			}, builder, slog.Default())

			ctx, cancel := signal.NotifyContext(context.Background(),
				syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			fmt.Fprintf(os.Stderr, "[biu] agent worker registered as %s; long-polling brain %s\n",
				machineName, brainURL)
			return worker.Run(ctx)
		},
	}
	c.Flags().StringVar(&brainURL, "brain-url", "",
		"brain base URL (default: BIUMIND_BRAIN_URL env or [relay].endpoint)")
	c.Flags().StringVar(&machineName, "machine-name", "",
		"environment machine_name reported to brain (default: hostname)")
	c.Flags().StringVar(&poolTag, "pool-tag", "",
		"optional pool tag for runtime-style routing")
	c.Flags().StringSliceVar(&allowedRoots, "allowed-roots", nil,
		"filesystem roots this daemon may touch (repeatable); empty = daemon cwd only")
	c.Flags().StringVar(&toolPolicy, "tool-policy", policyWorkspaceWrite,
		"capability floor: readonly | workspace-write | full")
	return c
}
