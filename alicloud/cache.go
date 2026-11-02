package alicloud

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// balanceCache 是进程内余额缓存，避免对账入口重复打 BSS 接口。
// 键不使用明文 AccessKey，而是其哈希截断。
type balanceCache struct {
	mu sync.Mutex
	m  map[string]balanceEntry
}

type balanceEntry struct {
	at      time.Time
	balance Balance
}

func newBalanceCache() *balanceCache {
	return &balanceCache{m: make(map[string]balanceEntry)}
}

// balanceCacheKey 生成缓存键：sha256(AK-ID 分隔 站点) 十六进制前 20 位。
// 截断在碰撞概率与键长度间取衡；键中不含明文凭证。
func balanceCacheKey(accessKeyID string, site Site) string {
	sum := sha256.Sum256([]byte(accessKeyID + "\x1f" + string(site)))
	return hex.EncodeToString(sum[:])[:20]
}

func (bc *balanceCache) get(key string, ttl time.Duration, now func() time.Time) (Balance, bool) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	entry, ok := bc.m[key]
	if !ok || now().Sub(entry.at) >= ttl {
		return Balance{}, false
	}
	return entry.balance, true
}

func (bc *balanceCache) put(key string, b Balance, now func() time.Time) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.m[key] = balanceEntry{at: now(), balance: b}
}
