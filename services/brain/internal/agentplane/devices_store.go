// Device pairing + revocable device tokens (Runtime v3 R6.1 / D5).
//
// 安全要点：
//   - device token 只在 daemon poll（命中 approved）时铸造，**只存 hash**，
//     明文仅在那一次 poll 响应里返回。brain 任何时刻不存明文 token。
//   - poll 身份靠 pairing_secret（daemon 持有，brain 存 hash，constant-time 比较）。
//   - 配对码 8 位数字 + 5min TTL + pending-only 可匹配，janitor 清过期。

package agentplane

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	pairingTTL        = 5 * time.Minute
	deviceTokenTTL    = 365 * 24 * time.Hour
	deviceTokenPrefix = "biu_dev_"
)

// ErrPairingSecret —— daemon poll 时 pairing_secret 不匹配（非合法请求者）。
var ErrPairingSecret = errors.New("agentplane: pairing secret mismatch")

// Pairing 是 CreatePairing 的返回（含只此一次的 code + pairing_secret）。
type Pairing struct {
	PairingID     uuid.UUID
	Code          string
	PairingSecret string
	MachineName   string
	ExpiresAt     time.Time
}

// Device 是 agent_devices 行（列设备 / 鉴权用，不含 token 明文）。
type Device struct {
	DeviceID   uuid.UUID
	UserID     uuid.UUID
	Name       string
	Prefix     string
	ToolPolicy string // R6.3：readonly | workspace-write | full
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	// Online / LastSeenAt（R6.4）：device 维度的在线态——取该 device 最新
	// environment 的 state/last_seen_at（device token 注册的 environment，由
	// janitor 维护 state）。区别于 LastUsedAt（token 鉴权热路径打点）。
	// device 从未起过 worker → Online=false、LastSeenAt=nil。
	Online     bool
	LastSeenAt *time.Time
}

// ToolPolicyPresets 是合法的 per-device 工具权限 preset（与 migration 00043 的
// CHECK 约束 + daemon floor.go 一致）。
var ToolPolicyPresets = map[string]bool{
	"readonly":        true,
	"workspace-write": true,
	"full":            true,
}

// randomDigits 生成 n 位十进制配对码（crypto/rand）。
func randomDigits(n int) (string, error) {
	buf := make([]byte, n)
	for i := 0; i < n; i++ {
		d, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		buf[i] = byte('0' + d.Int64())
	}
	return string(buf), nil
}

func randomHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func sha256Hex(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// CreatePairing 建一个 pending 配对，返回 code + pairing_secret（只此一次）。
func (s *Store) CreatePairing(ctx context.Context, machineName, osArch, workerKind string) (*Pairing, error) {
	code, err := randomDigits(8)
	if err != nil {
		return nil, err
	}
	secret, err := randomHex(24)
	if err != nil {
		return nil, err
	}
	pid := uuid.New()
	exp := time.Now().Add(pairingTTL)
	const q = `
		INSERT INTO agent_pairings
			(pairing_id, code, pairing_secret_hash, machine_name, os_arch, worker_kind, status, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7)
	`
	if _, err := s.pool.Exec(ctx, q, pid, code, sha256Hex(secret),
		machineName, nullableString(osArch), nullableString(workerKind), exp); err != nil {
		return nil, fmt.Errorf("create pairing: %w", err)
	}
	return &Pairing{PairingID: pid, Code: code, PairingSecret: secret, MachineName: machineName, ExpiresAt: exp}, nil
}

// ApprovePairing 把 pending 配对绑定到批准用户。返回 machine_name 供 UI 确认。
// 错码 / 已过期 / 已批准 → ErrNotFound。
func (s *Store) ApprovePairing(ctx context.Context, userID uuid.UUID, code string) (string, error) {
	const q = `
		UPDATE agent_pairings
		   SET user_id = $1, status = 'approved', approved_at = now()
		 WHERE code = $2 AND status = 'pending' AND expires_at > now()
		RETURNING machine_name
	`
	var machineName string
	err := s.pool.QueryRow(ctx, q, userID, code).Scan(&machineName)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("approve pairing: %w", err)
	}
	return machineName, nil
}

// PollPairing 是 daemon 轮询。校验 pairing_secret 后：
//   - pending  → ("", nil, "pending", nil)
//   - approved → 铸造 device token（只存 hash），pairing 标 consumed，返回明文 token + Device
//   - consumed / expired → 对应 status
//
// pairing_secret 不匹配 → ErrInvalid；pairing 不存在 → ErrNotFound。
func (s *Store) PollPairing(ctx context.Context, pairingID uuid.UUID, pairingSecret string) (token string, dev *Device, status string, err error) {
	var (
		storedHash  []byte
		st          string
		userID      *uuid.UUID
		machineName string
		expiresAt   time.Time
	)
	const sel = `SELECT pairing_secret_hash, status, user_id, machine_name, expires_at FROM agent_pairings WHERE pairing_id = $1`
	if err = s.pool.QueryRow(ctx, sel, pairingID).Scan(&storedHash, &st, &userID, &machineName, &expiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, "", ErrNotFound
		}
		return "", nil, "", fmt.Errorf("poll pairing: %w", err)
	}
	// constant-time 校验 daemon 身份。
	if subtle.ConstantTimeCompare(storedHash, sha256Hex(pairingSecret)) != 1 {
		return "", nil, "", ErrPairingSecret
	}
	if time.Now().After(expiresAt) {
		return "", nil, "expired", nil
	}
	switch st {
	case "pending":
		return "", nil, "pending", nil
	case "consumed":
		return "", nil, "consumed", nil
	case "approved":
		if userID == nil {
			return "", nil, "", fmt.Errorf("poll pairing: approved but no user_id")
		}
		token, dev, err = s.mintDeviceToken(ctx, pairingID, *userID, machineName)
		if err != nil {
			return "", nil, "", err
		}
		return token, dev, "approved", nil
	default:
		return "", nil, st, nil
	}
}

// mintDeviceToken 在一个事务里铸造 device token + 写 agent_devices（只存 hash）
// + 把 pairing 标 consumed。返回明文 token（仅此一次）。
func (s *Store) mintDeviceToken(ctx context.Context, pairingID, userID uuid.UUID, name string) (string, *Device, error) {
	prefix, err := randomHex(4) // 8 hex chars
	if err != nil {
		return "", nil, err
	}
	secret, err := randomHex(32)
	if err != nil {
		return "", nil, err
	}
	full := deviceTokenPrefix + prefix + "_" + secret
	deviceID := uuid.New()
	exp := time.Now().Add(deviceTokenTTL)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_devices (device_id, user_id, name, token_hash, prefix, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		deviceID, userID, name, sha256Hex(full), prefix, exp); err != nil {
		return "", nil, fmt.Errorf("mint device token: %w", err)
	}
	// 标 consumed —— 仅当仍是 approved（防并发二次铸造）。
	tag, err := tx.Exec(ctx, `UPDATE agent_pairings SET status='consumed' WHERE pairing_id=$1 AND status='approved'`, pairingID)
	if err != nil {
		return "", nil, fmt.Errorf("consume pairing: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", nil, ErrNotFound // 已被并发消费
	}
	if err := tx.Commit(ctx); err != nil {
		return "", nil, err
	}
	return full, &Device{DeviceID: deviceID, UserID: userID, Name: name, Prefix: prefix, ExpiresAt: exp}, nil
}

// VerifyDeviceToken 校验一个 device token（鉴权热路径）。命中未吊销未过期 →
// 返回 user_id + device_id；否则 ErrNotFound。异步更新 last_used_at。
func (s *Store) VerifyDeviceToken(ctx context.Context, token string) (userID, deviceID uuid.UUID, err error) {
	const q = `
		SELECT device_id, user_id FROM agent_devices
		 WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
	`
	if err = s.pool.QueryRow(ctx, q, sha256Hex(token)).Scan(&deviceID, &userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, uuid.Nil, ErrNotFound
		}
		return uuid.Nil, uuid.Nil, err
	}
	// last_used_at 异步打点（best-effort，不阻塞鉴权）。
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = s.pool.Exec(bg, `UPDATE agent_devices SET last_used_at = now() WHERE device_id = $1`, deviceID)
	}()
	return userID, deviceID, nil
}

// ListDevices 列某用户的设备 token（不含 hash）。
func (s *Store) ListDevices(ctx context.Context, userID uuid.UUID) ([]Device, error) {
	// R6.4：LATERAL 取该 device 最新 environment 的 state/last_seen_at（在线态）。
	// LEFT JOIN 保证从未起过 worker 的设备仍返回（e.* 为 NULL）。命中 R6.3 建的
	// agent_environments_device_idx（partial WHERE device_id IS NOT NULL）。
	const q = `
		SELECT d.device_id, d.user_id, d.name, d.prefix, d.tool_policy,
		       d.created_at, d.last_used_at, d.expires_at, d.revoked_at,
		       e.state, e.last_seen_at
		  FROM agent_devices d
		  LEFT JOIN LATERAL (
		      SELECT state, last_seen_at FROM agent_environments
		       WHERE device_id = d.device_id
		       ORDER BY last_seen_at DESC LIMIT 1
		  ) e ON true
		 WHERE d.user_id = $1 ORDER BY d.created_at DESC
	`
	rows, err := s.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		var state *string
		if err := rows.Scan(&d.DeviceID, &d.UserID, &d.Name, &d.Prefix, &d.ToolPolicy,
			&d.CreatedAt, &d.LastUsedAt, &d.ExpiresAt, &d.RevokedAt,
			&state, &d.LastSeenAt); err != nil {
			return nil, err
		}
		d.Online = state != nil && *state == "online"
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDeviceToolPolicy 取某设备的 tool_policy preset。device 不存在 → ErrNotFound。
// R6.3：session 创建时按 environment.device_id 反查，stamp 进 work payload。
func (s *Store) GetDeviceToolPolicy(ctx context.Context, deviceID uuid.UUID) (string, error) {
	const q = `SELECT tool_policy FROM agent_devices WHERE device_id = $1 AND revoked_at IS NULL`
	var policy string
	if err := s.pool.QueryRow(ctx, q, deviceID).Scan(&policy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get device tool_policy: %w", err)
	}
	return policy, nil
}

// SetDeviceToolPolicy 改某用户某设备的 tool_policy（属主校验）。preset 非法由调
// 用方先拒（API 层）。返回 ErrNotFound 当设备不存在 / 非本人 / 已吊销。
func (s *Store) SetDeviceToolPolicy(ctx context.Context, userID, deviceID uuid.UUID, policy string) error {
	const q = `UPDATE agent_devices SET tool_policy = $3
		WHERE device_id = $1 AND user_id = $2 AND revoked_at IS NULL`
	tag, err := s.pool.Exec(ctx, q, deviceID, userID, policy)
	if err != nil {
		return fmt.Errorf("set device tool_policy: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeDevice 吊销某用户的一台设备 token（属主校验）。
func (s *Store) RevokeDevice(ctx context.Context, userID, deviceID uuid.UUID) error {
	const q = `UPDATE agent_devices SET revoked_at = now() WHERE device_id = $1 AND user_id = $2 AND revoked_at IS NULL`
	tag, err := s.pool.Exec(ctx, q, deviceID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SweepExpiredPairings 删过期的 pending/approved 配对（janitor 调）。consumed 的
// 也清（已无用）。返回删除行数。
func (s *Store) SweepExpiredPairings(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM agent_pairings WHERE expires_at < now() OR status = 'consumed'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
