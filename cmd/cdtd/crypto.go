package main

// 凭据加密：AES-256-GCM + 数据目录旁的 master.key（32 字节随机）。
// 规范：
//   - 密钥文件已存在则直接用；内容不完整报错（绝不悄悄重生成，否则已存密文全部解不开）
//   - 密文格式 "enc:v1:base64(nonce||ciphertext)"；无前缀视为历史明文（读取兼容，
//     下次落盘自动转为加密——平滑迁移）
//   - 解密失败返回错误而非静默返回空值（避免把坏数据当空密钥用）

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const secretPrefix = "enc:v1:"

// --- 管理员密码哈希（PBKDF2-HMAC-SHA256） ------------------
//
// 存储格式：pbkdf2_sha256$<rounds>$<salt_b64>$<hash_b64>

const pbkdf2Rounds = 260_000

// pbkdf2Sha256 独立实现 PBKDF2（RFC 2898），DKLen 固定 32（单个 SHA-256 块），
// 避免引入第三方依赖。
func pbkdf2Sha256(password, salt []byte, rounds int) []byte {
	mac := hmac.New(sha256.New, password)
	mac.Write(salt)
	mac.Write([]byte{0, 0, 0, 1}) // INT(1)：DKLen=32 时只有一个块
	u := mac.Sum(nil)
	t := make([]byte, 32)
	copy(t, u)
	for n := 1; n < rounds; n++ {
		mac.Reset()
		mac.Write(u)
		u = mac.Sum(nil)
		for i := range t {
			t[i] ^= u[i]
		}
	}
	return t
}

// HashPassword 生成哈希串。
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	digest := pbkdf2Sha256([]byte(password), salt, pbkdf2Rounds)
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s",
		pbkdf2Rounds,
		base64.StdEncoding.EncodeToString(salt),
		base64.StdEncoding.EncodeToString(digest),
	), nil
}

// VerifyPassword 校验密码（常量时间比较；支持任意轮次的存量哈希）。
func VerifyPassword(password, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	rounds, err := strconv.Atoi(parts[1])
	if err != nil || rounds < 1000 {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	expect, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	actual := pbkdf2Sha256([]byte(password), salt, rounds)
	return hmac.Equal(expect, actual)
}

// LoadMasterKey 读取（或首次创建）主密钥文件。
func LoadMasterKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		if len(raw) >= 32 {
			return raw[:32], nil
		}
		return nil, fmt.Errorf(
			"主密钥文件 %s 内容不完整（%d 字节，至少 32）。请从备份恢复；确要重来请删除该文件后重新录入所有凭据",
			path, len(raw))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// sealSecret 加密为 "enc:v1:..." 形态。
func sealSecret(key, plaintext []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, plaintext, nil)
	return secretPrefix + base64.StdEncoding.EncodeToString(ct), nil
}

// openSecret 解密；无 enc: 前缀的历史明文原样返回（isPlain=true 提示调用方
// 可在下次落盘时转为加密）。
func openSecret(key []byte, stored string) (plain []byte, isPlain bool, err error) {
	if !strings.HasPrefix(stored, secretPrefix) {
		return []byte(stored), true, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, secretPrefix))
	if err != nil {
		return nil, false, fmt.Errorf("密文格式损坏: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, false, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, false, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, false, errors.New("密文过短")
	}
	plain, err = gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return nil, false, fmt.Errorf("解密失败（主密钥不匹配？）: %w", err)
	}
	return plain, false, nil
}
