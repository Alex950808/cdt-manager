package main

import (
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Instance 是一台受管实例的配置（含凭证，落盘前整个文件建议限制权限）。
type Instance struct {
	ID              string  `json:"id"` // 内部 UUID，与云上实例 ID 无关
	Name            string  `json:"name"`
	AccessKeyID     string  `json:"access_key_id"`
	AccessKeySecret string  `json:"access_key_secret"`
	RegionID        string  `json:"region_id"`
	InstanceID      string  `json:"instance_id"`
	Site            string  `json:"site"` // china | international
	Note            string  `json:"note"`
	QuotaGB         float64 `json:"quota_gb,omitempty"`     // 月度流量配额（展示用）
	ThresholdGB     float64 `json:"threshold_gb,omitempty"` // 超限阈值（引擎判定用）
}

// Quota / Threshold 带默认值兜底（存量数据无字段时默认 200/188）。
func (in *Instance) Quota() float64 {
	if in.QuotaGB > 0 {
		return in.QuotaGB
	}
	return 200
}

func (in *Instance) Threshold() float64 {
	if in.ThresholdGB > 0 {
		return in.ThresholdGB
	}
	return 188
}

// Snapshot 是最近一次成功刷新的云上数据快照（仪表盘直接读，不用等接口）。
type Snapshot struct {
	Status       string    `json:"status"`
	StoppedMode  string    `json:"stopped_mode"`
	PublicIP     string    `json:"public_ip"`
	RegionID     string    `json:"region_id"`
	TrafficGB    *float64  `json:"traffic_gb"` // nil 表示接口成功但无明细（勿当 0）
	Balance      float64   `json:"balance"`
	Currency     string    `json:"currency"`
	BillTotal    float64   `json:"bill_total"`
	BillCycle    string    `json:"bill_cycle"`
	RefreshedAt  time.Time `json:"refreshed_at"`
	RefreshError string    `json:"refresh_error,omitempty"` // 最近一次刷新失败原因
}

type fileData struct {
	Instances  []*Instance             `json:"instances"`
	Snapshots  map[string]*Snapshot    `json:"snapshots"`
	Settings   map[string]string       `json:"settings,omitempty"`
	DNSRecords []DNSRecord             `json:"dns_records,omitempty"`
	Groups     []Group                 `json:"groups,omitempty"`
	Keepalives []Keepalive             `json:"keepalives,omitempty"`
	Events     []Event                 `json:"events,omitempty"`
	EventSeq   int64                   `json:"event_seq,omitempty"`
	Flags      map[string]flagEntry    `json:"flags,omitempty"`
	Samples    map[string][]Sample     `json:"samples,omitempty"`
	Admin      *AdminAccount           `json:"admin,omitempty"`
	Sessions   map[string]sessionEntry `json:"sessions,omitempty"`
}

// Store 是数据持久化层：实例配置 + 快照 + 事件等。
// 支持 JSON 文件或 SQLite 数据库存储（零额外配置）。
// 提供 masterKey 时，AccessKeySecret 落盘自动 AES-GCM 加密（历史明文读取后自动迁移）。
type Store struct {
	mu          sync.RWMutex
	path        string
	backend     storeBackend
	data        fileData
	pruneCursor time.Time // 定时清理节流
	masterKey   []byte    // 非 nil 启用凭据加密
}

type storeBackend interface {
	Load(masterKey []byte) (*fileData, error)
	Save(data *fileData, masterKey []byte) error
	Close() error
}

type jsonBackend struct {
	path string
}

func newJSONBackend(path string) *jsonBackend {
	return &jsonBackend{path: path}
}

func (b *jsonBackend) Close() error {
	return nil
}

func (b *jsonBackend) Load(masterKey []byte) (*fileData, error) {
	raw, err := os.ReadFile(b.path)
	if errors.Is(err, os.ErrNotExist) {
		return &fileData{Snapshots: map[string]*Snapshot{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var data fileData
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, err
		}
	}
	if data.Snapshots == nil {
		data.Snapshots = map[string]*Snapshot{}
	}
	return &data, nil
}

func (b *jsonBackend) Save(data *fileData, masterKey []byte) error {
	wired := *data
	if masterKey != nil {
		wired.Instances = make([]*Instance, len(data.Instances))
		for i, in := range data.Instances {
			cp := *in
			if cp.AccessKeySecret != "" && !strings.HasPrefix(cp.AccessKeySecret, secretPrefix) {
				enc, err := sealSecret(masterKey, []byte(cp.AccessKeySecret))
				if err != nil {
					return fmt.Errorf("凭据加密失败: %w", err)
				}
				cp.AccessKeySecret = enc
			}
			wired.Instances[i] = &cp
		}
	}
	raw, err := json.MarshalIndent(&wired, "", "  ")
	if err != nil {
		return err
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, b.path) // 原子替换，避免写一半崩溃损坏
}

func NewStore(path, masterKeyPath string) (*Store, error) {
	return NewStoreWithDriver(path, masterKeyPath, "auto")
}

func NewStoreWithDriver(path, masterKeyPath, driver string) (*Store, error) {
	s := &Store{path: path, data: fileData{Snapshots: map[string]*Snapshot{}}}
	if masterKeyPath != "" {
		key, err := LoadMasterKey(masterKeyPath)
		if err != nil {
			return nil, err
		}
		s.masterKey = key
	}

	driver = strings.ToLower(strings.TrimSpace(driver))
	if driver == "" || driver == "auto" {
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".db" || ext == ".sqlite" || ext == ".sqlite3" {
			driver = "sqlite"
		} else {
			driver = "json"
		}
	}

	var backend storeBackend
	switch driver {
	case "sqlite":
		sb, err := newSQLiteBackend(path)
		if err != nil {
			return nil, err
		}
		backend = sb
	case "json":
		backend = newJSONBackend(path)
	default:
		return nil, fmt.Errorf("不支持的存储引擎: %s（仅支持 json 或 sqlite）", driver)
	}
	s.backend = backend

	loaded, err := backend.Load(s.masterKey)
	if err != nil {
		_ = backend.Close()
		return nil, err
	}
	if loaded != nil {
		s.data = *loaded
	}
	if s.data.Snapshots == nil {
		s.data.Snapshots = map[string]*Snapshot{}
	}
	if s.data.Settings == nil {
		s.data.Settings = map[string]string{}
	}
	if s.data.Flags == nil {
		s.data.Flags = map[string]flagEntry{}
	}
	if s.data.Samples == nil {
		s.data.Samples = map[string][]Sample{}
	}
	if s.data.Sessions == nil {
		s.data.Sessions = map[string]sessionEntry{}
	}

	// 解密凭据（历史明文自动迁移为密文）
	migrated := false
	if s.masterKey != nil {
		for _, in := range s.data.Instances {
			if in.AccessKeySecret == "" {
				continue
			}
			plain, isPlain, err := openSecret(s.masterKey, in.AccessKeySecret)
			if err != nil {
				_ = backend.Close()
				return nil, fmt.Errorf("实例 %s 的密钥解密失败: %w", in.Name, err)
			}
			in.AccessKeySecret = string(plain)
			if isPlain {
				migrated = true
			}
		}
		if migrated {
			_ = s.save()
		}
	}
	return s, nil
}

// Close 释放存储后端资源（如 SQLite 数据库连接）。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.backend != nil {
		return s.backend.Close()
	}
	return nil
}

// save 落盘（调用方须已持锁）。
// 启用加密时，AccessKeySecret 以 AES-GCM 密文写盘（内存中保持明文）。
func (s *Store) save() error {
	return s.backend.Save(&s.data, s.masterKey)
}

// List 返回实例配置副本（含凭证；对外输出前由 API 层打码）。
func (s *Store) List() []*Instance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Instance, 0, len(s.data.Instances))
	for _, in := range s.data.Instances {
		cp := *in
		out = append(out, &cp)
	}
	return out
}

// Get 按 ID 取单个实例。
func (s *Store) Get(id string) (Instance, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, in := range s.data.Instances {
		if in.ID == id {
			return *in, true
		}
	}
	return Instance{}, false
}

// Create 新增实例；返回生成的 ID。
func (s *Store) Create(in *Instance) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.ID == "" {
		in.ID = newUUID()
	}
	for _, ex := range s.data.Instances {
		if ex.ID == in.ID {
			return "", errors.New("实例 ID 已存在")
		}
	}
	s.data.Instances = append(s.data.Instances, in)
	return in.ID, s.save()
}

// Update 覆盖实例配置（按 ID 匹配）。语义：**非空字段才覆盖**——
// 编辑表单未填的项（如不重填密钥）保持原值，杜绝误清空凭证。
func (s *Store) Update(in *Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, ex := range s.data.Instances {
		if ex.ID != in.ID {
			continue
		}
		updated := *ex
		if in.Name != "" {
			updated.Name = in.Name
		}
		if in.AccessKeyID != "" {
			updated.AccessKeyID = in.AccessKeyID
		}
		if in.AccessKeySecret != "" {
			updated.AccessKeySecret = in.AccessKeySecret
		}
		if in.RegionID != "" {
			updated.RegionID = in.RegionID
		}
		if in.InstanceID != "" {
			updated.InstanceID = in.InstanceID
		}
		if in.Site != "" {
			updated.Site = in.Site
		}
		if in.Note != "" {
			updated.Note = in.Note
		}
		if in.QuotaGB > 0 {
			updated.QuotaGB = in.QuotaGB
		}
		if in.ThresholdGB > 0 {
			updated.ThresholdGB = in.ThresholdGB
		}
		s.data.Instances[i] = &updated
		return s.save()
	}
	return errors.New("实例不存在")
}

// Delete 移除实例及其快照。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.data.Instances[:0]
	found := false
	for _, ex := range s.data.Instances {
		if ex.ID == id {
			found = true
			continue
		}
		kept = append(kept, ex)
	}
	if !found {
		return errors.New("实例不存在")
	}
	s.data.Instances = kept
	delete(s.data.Snapshots, id)
	return s.save()
}

// PutSnapshot 保存实例快照。
func (s *Store) PutSnapshot(id string, snap *Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Snapshots[id] = snap
	_ = s.save()
}

// Snapshot 读取实例快照副本。
func (s *Store) Snapshot(id string) (Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.data.Snapshots[id]
	if !ok {
		return Snapshot{}, false
	}
	return *snap, true
}

// errNotFound 供各处统一返回"目标不存在"类错误。
func errNotFound(msg string) error { return errors.New(msg) }

// newUUID 生成请求级唯一 ID（RFC 4122 v4 形态）。
func newUUID() string {
	var b [16]byte
	if _, err := crand.Read(b[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	b[6] = (b[6] & 0x0F) | 0x40
	b[8] = (b[8] & 0x3F) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
