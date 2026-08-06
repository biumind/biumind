// X25519 keypair 持久化（Runtime v3 R6.2；R6.4 迁 keychain）。
//
// daemon 启动时 loadOrCreateKeypair() 取私钥：OS keychain 优先（hex 编码存，
// account=agentplane-privkey），keychain 不可用回退 ~/.biu/agentplane/privkey
// （R6.4 起存 hex；兼容读 R6.2 的 raw 32B 旧文件并自动迁移）。pubkey 由 privkey
// 推导，注册时 hex 上报 brain 做 BYOK 信封加密。
package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/biumind/biumind/apps/cli/biu/internal/secretstore"
	"github.com/biumind/biumind/packages/go-sdk/biu/agentcrypto"
)

// privkeyPath 返回 X25519 私钥 0600 文件回退路径（~/.biu/agentplane/privkey）。
func privkeyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".biu", "agentplane", "privkey"), nil
}

// loadOrCreateKeypair 读取（或首次生成并持久化）daemon 的 X25519 密钥对，返回
// raw 32B 的 privkey + pubkey。
func loadOrCreateKeypair() (privkey, pubkey []byte, err error) {
	legacyPath, _ := privkeyPath()
	store := secretstore.Open(keychainServiceName, agentplanePrivkeyAccnt, legacyPath)

	// 1) keychain / hex 文件。
	if hexv, gerr := store.Get(); gerr == nil && hexv != "" {
		if raw, derr := hex.DecodeString(hexv); derr == nil && len(raw) == agentcrypto.X25519KeySize {
			pub, perr := agentcrypto.PublicFromPrivate(raw)
			if perr != nil {
				return nil, nil, fmt.Errorf("derive pubkey from stored privkey: %w", perr)
			}
			return raw, pub, nil
		}
		// hexv 非合法 hex（多半是 keychain-less 主机把 R6.2 raw 旧文件当 string
		// 读出）→ 落到 (2) 显式按 raw 处理。
	}

	// 2) R6.2 旧 raw 二进制文件 → 迁移到 keychain/hex。
	if b, rerr := os.ReadFile(legacyPath); rerr == nil && len(b) == agentcrypto.X25519KeySize {
		pub, perr := agentcrypto.PublicFromPrivate(b)
		if perr != nil {
			return nil, nil, fmt.Errorf("derive pubkey from legacy privkey: %w", perr)
		}
		// keychain 可用→写 keychain 删旧 raw 文件；不可用→hex 覆写同文件。失败仅
		// 告警（内存里的 key 仍可用，下次重启再迁）。
		if serr := store.Set(hex.EncodeToString(b)); serr != nil {
			fmt.Fprintf(os.Stderr, "[biu] 警告：privkey 迁移持久化失败：%v\n", serr)
		}
		return b, pub, nil
	}

	// 3) 生成新密钥对并持久化。
	priv, pub, gerr := agentcrypto.GenerateKeypair()
	if gerr != nil {
		return nil, nil, gerr
	}
	if serr := store.Set(hex.EncodeToString(priv)); serr != nil {
		// 持久化失败不阻断：daemon 本次仍能用，下次重启会重新生成（pubkey 变 →
		// brain 注册时重存）。告警提示。
		fmt.Fprintf(os.Stderr, "[biu] 警告：privkey 持久化失败，重启后将重新生成：%v\n", serr)
	}
	return priv, pub, nil
}
