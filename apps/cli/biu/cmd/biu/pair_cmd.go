// `biu pair` — 把这台机器配对到 BiuMind 账号，换一个 scoped + 可吊销的
// device token（Runtime v3 R6.1 / D5），替代在 daemon 上直接放完整用户 PAT。
//
// 流程：request 拿配对码 → 在已登录设备(手机/web)输入批准 → 轮询 poll 拿
// device token → 存 ~/.biu/device_token (0600)。之后 `biu agent worker` /
// `biu serve` 自动优先用它。

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/secretstore"
	"github.com/spf13/cobra"
)

// deviceTokenPath 返回 device token 的 0600 文件路径（~/.biu/device_token）。
func deviceTokenPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".biu", "device_token"), nil
}

// deviceTokenStore 返回 device token 的 secretstore（0600 文件唯一存储）。
func deviceTokenStore() *secretstore.Store {
	p, _ := deviceTokenPath()
	return secretstore.Open(p)
}

// loadDeviceToken 读取已配对的 device token（不存在 → ""）。
func loadDeviceToken() string {
	v, err := deviceTokenStore().Get()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// saveDeviceToken 存 device token（0600 文件）。
func saveDeviceToken(token string) error {
	return deviceTokenStore().Set(token)
}

func newPairCmd(f *rootFlags) *cobra.Command {
	var (
		brainURL string
		name     string
	)
	c := &cobra.Command{
		Use:   "pair",
		Short: "Pair this device with your BiuMind account (revocable device token)",
		Long: `Exchange a one-time pairing code for a scoped, revocable device token so
this machine's daemon doesn't need your full account PAT. Approve the code on
an already-signed-in device (phone / web). Revoke anytime from there.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.Load(f.cfgPath)
			if err != nil {
				return err
			}
			if brainURL == "" {
				brainURL = firstNonEmpty(os.Getenv("BIUMIND_BRAIN_URL"), cfg.Relay.Endpoint)
			}
			if brainURL == "" {
				return errors.New("biu pair: missing brain URL (set --brain-url or BIUMIND_BRAIN_URL)")
			}
			brainURL = strings.TrimRight(brainURL, "/")
			if name == "" {
				name, _ = os.Hostname()
				if name == "" {
					name = "biu-daemon"
				}
			}
			osArch := runtime.GOOS + "/" + runtime.GOARCH

			// 1. request
			var reqResp struct {
				PairingID     string `json:"pairing_id"`
				Code          string `json:"code"`
				PairingSecret string `json:"pairing_secret"`
				ExpiresAt     string `json:"expires_at"`
			}
			if err := postJSON(brainURL+"/v1/agent/devices/pair/request", map[string]any{
				"machine_name": name, "os_arch": osArch, "worker_kind": "biu_daemon",
			}, &reqResp); err != nil {
				return fmt.Errorf("pair request: %w", err)
			}
			fmt.Fprintf(os.Stderr,
				"\n  配对码: %s\n  在已登录的 BiuMind 设备(手机 / 网页)输入此码批准「%s」(5 分钟内有效)。\n\n等待批准",
				reqResp.Code, name)

			// 2. poll until approved / expired
			deadline := time.Now().Add(5 * time.Minute)
			for time.Now().Before(deadline) {
				time.Sleep(2 * time.Second)
				fmt.Fprint(os.Stderr, ".")
				token, status, err := pollPairing(brainURL, reqResp.PairingID, reqResp.PairingSecret)
				if err != nil {
					return fmt.Errorf("\npair poll: %w", err)
				}
				switch status {
				case "pending":
					continue
				case "approved":
					if err := saveDeviceToken(token); err != nil {
						return fmt.Errorf("\nsave device token: %w", err)
					}
					p, _ := deviceTokenPath()
					fmt.Fprintf(os.Stderr,
						"\n\n✅ 已配对。device token 已存到 %s (0600)。\n   现在直接 `biu agent worker` / `biu serve` 即可(自动优先用它,不再需要 PAT)。\n   在手机/网页端可随时吊销这台设备。\n",
						p)
					return nil
				default:
					return fmt.Errorf("\npairing %s", status)
				}
			}
			return errors.New("\n配对超时(5 分钟未批准)。重试 `biu pair`")
		},
	}
	c.Flags().StringVar(&brainURL, "brain-url", "", "brain base URL (default: BIUMIND_BRAIN_URL / [model-relay].endpoint)")
	c.Flags().StringVar(&name, "name", "", "device name reported to brain (default: hostname)")
	return c
}

// pollPairing 调一次 poll，返回 (token, status, err)。token 仅 status=approved 时非空。
func pollPairing(brainURL, pairingID, pairingSecret string) (token, status string, err error) {
	body, _ := json.Marshal(map[string]any{"pairing_id": pairingID, "pairing_secret": pairingSecret})
	req, _ := http.NewRequest(http.MethodPost, brainURL+"/v1/agent/devices/pair/poll", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var out struct {
		Status      string `json:"status"`
		DeviceToken string `json:"device_token"`
		Error       string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	switch resp.StatusCode {
	case http.StatusAccepted:
		return "", "pending", nil
	case http.StatusOK:
		return out.DeviceToken, "approved", nil
	default:
		msg := out.Error
		if msg == "" {
			msg = resp.Status
		}
		return "", msg, nil
	}
}

// postJSON 发一个 JSON POST 并解码响应到 out。非 2xx → error。
func postJSON(url string, payload any, out any) error {
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("http %s", resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
