package main

import (
	crand "crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := crand.Read(key); err != nil {
		t.Fatal(err)
	}
	enc, err := sealSecret(key, []byte("my-secret-ak"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, secretPrefix) {
		t.Fatalf("sealed = %q, want prefix", enc)
	}
	plain, isPlain, err := openSecret(key, enc)
	if err != nil || isPlain || string(plain) != "my-secret-ak" {
		t.Fatalf("open = %q isPlain=%v err=%v", plain, isPlain, err)
	}
	// 错误密钥 → 报错（不静默）
	wrong := make([]byte, 32)
	_, _ = crand.Read(wrong)
	if _, _, err := openSecret(wrong, enc); err == nil {
		t.Error("wrong key should fail")
	}
	// 历史明文 → 原样返回并标记 isPlain
	plain, isPlain, err = openSecret(key, "legacy-plain-ak")
	if err != nil || !isPlain || string(plain) != "legacy-plain-ak" {
		t.Fatalf("legacy = %q isPlain=%v err=%v", plain, isPlain, err)
	}
}

func TestLoadMasterKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	k1, err := LoadMasterKey(path)
	if err != nil || len(k1) != 32 {
		t.Fatalf("create key: len=%d err=%v", len(k1), err)
	}
	// 二次读取同一文件 → 相同密钥
	k2, err := LoadMasterKey(path)
	if err != nil || string(k1) != string(k2) {
		t.Error("reloaded key should be identical")
	}
	// 内容不完整 → 报错且不重生成
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMasterKey(path); err == nil {
		t.Error("short key file should error, not regenerate")
	}
}

func TestStoreEncryptedAtRest(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.json")
	keyPath := filepath.Join(dir, "master.key")

	store, err := NewStore(dataPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(&Instance{
		Name: "A", AccessKeyID: "ak-id", AccessKeySecret: "plain-secret-123",
		RegionID: "cn-hongkong", InstanceID: "i-1", Site: "china",
	}); err != nil {
		t.Fatal(err)
	}

	// 磁盘上是密文，不含明文
	raw, _ := os.ReadFile(dataPath)
	if strings.Contains(string(raw), "plain-secret-123") {
		t.Fatal("plaintext secret leaked to disk")
	}
	if !strings.Contains(string(raw), secretPrefix) {
		t.Error("ciphertext expected on disk")
	}

	// 重开 → 自动解密可用（且明文已迁移为密文形态）
	store2, err := NewStore(dataPath, keyPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	list := store2.List()
	if len(list) != 1 || list[0].AccessKeySecret != "plain-secret-123" {
		t.Fatalf("decrypted secret = %q", list[0].AccessKeySecret)
	}
}
