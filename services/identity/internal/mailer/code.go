// Code generation + verification for 6-digit email confirmation.
//
// 不存明文 — sha256 hash 入库, 校验走 constant-time compare.
// crypto/rand 保证不可预测; 模 1000000 后再用 %06d 兜零.

package mailer

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
)

// GenerateCode 返回 (明文 6 位字符串, sha256 hash). 失败基本只在系统熵池
// 异常 (实际场景几乎不会).
func GenerateCode() (string, []byte, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", nil, err
	}
	n := binary.BigEndian.Uint32(b[:]) % 1000000
	code := fmt.Sprintf("%06d", n)
	return code, HashCode(code), nil
}

// HashCode — 业务层算 hash 用 (例如校验时把用户输入 hash 后跟存库的比).
func HashCode(code string) []byte {
	h := sha256.Sum256([]byte(code))
	return h[:]
}

// VerifyCode constant-time compare; subtle 防侧信道.
func VerifyCode(code string, stored []byte) bool {
	got := HashCode(code)
	return subtle.ConstantTimeCompare(got, stored) == 1
}
