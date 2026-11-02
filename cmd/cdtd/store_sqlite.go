package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type sqliteBackend struct {
	db   *sql.DB
	path string
}

func newSQLiteBackend(path string) (*sqliteBackend, error) {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据库目录失败: %w", err)
		}
	}

	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite 数据库 %s 失败: %w", path, err)
	}

	// 单连接或合理连接池以避免 SQLite 写锁冲突
	db.SetMaxOpenConns(1)

	b := &sqliteBackend{db: db, path: path}
	if err := b.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化 SQLite 表结构失败: %w", err)
	}
	return b, nil
}

func (b *sqliteBackend) Close() error {
	if b.db != nil {
		return b.db.Close()
	}
	return nil
}

func (b *sqliteBackend) initSchema() error {
	schema := `
CREATE TABLE IF NOT EXISTS instances (
    id TEXT PRIMARY KEY,
    name TEXT,
    access_key_id TEXT,
    access_key_secret TEXT,
    region_id TEXT,
    instance_id TEXT,
    site TEXT,
    note TEXT,
    quota_gb REAL,
    threshold_gb REAL
);

CREATE TABLE IF NOT EXISTS snapshots (
    instance_id TEXT PRIMARY KEY,
    status TEXT,
    stopped_mode TEXT,
    public_ip TEXT,
    region_id TEXT,
    traffic_gb REAL,
    balance REAL,
    currency TEXT,
    bill_total REAL,
    bill_cycle TEXT,
    refreshed_at TEXT,
    refresh_error TEXT
);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT
);

CREATE TABLE IF NOT EXISTS dns_records (
    record TEXT PRIMARY KEY,
    target_kind TEXT,
    target_id TEXT,
    last_ip TEXT,
    last_status TEXT,
    last_at INTEGER,
    sort_order INTEGER
);

CREATE TABLE IF NOT EXISTS groups (
    id TEXT PRIMARY KEY,
    name TEXT,
    enabled INTEGER,
    failover_enabled INTEGER,
    cycle TEXT,
    anchor_at TEXT,
    members TEXT,
    run TEXT
);

CREATE TABLE IF NOT EXISTS keepalives (
    instance_id TEXT PRIMARY KEY,
    enabled INTEGER
);

CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY,
    kind TEXT,
    title TEXT,
    detail TEXT,
    status TEXT,
    progress TEXT,
    pending_status TEXT,
    pending_kind TEXT,
    server_id TEXT,
    at TEXT,
    updated_at TEXT
);

CREATE TABLE IF NOT EXISTS flags (
    key TEXT PRIMARY KEY,
    created_at TEXT,
    value INTEGER
);

CREATE TABLE IF NOT EXISTS samples (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    instance_id TEXT,
    at TEXT,
    gb REAL
);
CREATE INDEX IF NOT EXISTS idx_samples_inst ON samples(instance_id, at);

CREATE TABLE IF NOT EXISTS admin (
    username TEXT PRIMARY KEY,
    pass_hash TEXT
);

CREATE TABLE IF NOT EXISTS sessions (
    token TEXT PRIMARY KEY,
    user TEXT,
    expires_at TEXT
);

CREATE TABLE IF NOT EXISTS meta (
    key TEXT PRIMARY KEY,
    value TEXT
);
`
	_, err := b.db.Exec(schema)
	return err
}

func (b *sqliteBackend) isEmpty() (bool, error) {
	var count int
	err := b.db.QueryRow("SELECT (SELECT count(*) FROM instances) + (SELECT count(*) FROM admin) + (SELECT count(*) FROM groups)").Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 0, nil
}

func (b *sqliteBackend) Load(masterKey []byte) (*fileData, error) {
	empty, err := b.isEmpty()
	if err != nil {
		return nil, err
	}

	// 如果 SQLite 数据库为空，尝试检测同目录下已有的 JSON 文件并自动迁移
	if empty {
		jsonCandidates := []string{
			strings.TrimSuffix(b.path, filepath.Ext(b.path)) + ".json",
			filepath.Join(filepath.Dir(b.path), "cdtd-data.json"),
		}
		for _, cand := range jsonCandidates {
			if cand == b.path {
				continue
			}
			if raw, err := os.ReadFile(cand); err == nil && len(raw) > 0 {
				var imported fileData
				if err := json.Unmarshal(raw, &imported); err == nil {
					log.Printf("检测到已有 JSON 数据文件 %s，正在自动迁移至 SQLite...", cand)
					if err := b.Save(&imported, masterKey); err == nil {
						log.Printf("成功将 %s 的数据迁移导入至 SQLite 数据库 %s", cand, b.path)
						break
					}
				}
			}
		}
	}

	data := &fileData{
		Snapshots: map[string]*Snapshot{},
		Settings:  map[string]string{},
		Flags:     map[string]flagEntry{},
		Samples:   map[string][]Sample{},
		Sessions:  map[string]sessionEntry{},
	}

	// 1. instances
	rows, err := b.db.Query("SELECT id, name, access_key_id, access_key_secret, region_id, instance_id, site, note, quota_gb, threshold_gb FROM instances")
	if err != nil {
		return nil, fmt.Errorf("读取 instances 表失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var in Instance
		if err := rows.Scan(&in.ID, &in.Name, &in.AccessKeyID, &in.AccessKeySecret, &in.RegionID, &in.InstanceID, &in.Site, &in.Note, &in.QuotaGB, &in.ThresholdGB); err != nil {
			return nil, err
		}
		data.Instances = append(data.Instances, &in)
	}

	// 2. snapshots
	snapRows, err := b.db.Query("SELECT instance_id, status, stopped_mode, public_ip, region_id, traffic_gb, balance, currency, bill_total, bill_cycle, refreshed_at, refresh_error FROM snapshots")
	if err != nil {
		return nil, fmt.Errorf("读取 snapshots 表失败: %w", err)
	}
	defer snapRows.Close()
	for snapRows.Next() {
		var id, refreshedAtStr string
		var snap Snapshot
		var trafficGB sql.NullFloat64
		if err := snapRows.Scan(&id, &snap.Status, &snap.StoppedMode, &snap.PublicIP, &snap.RegionID, &trafficGB, &snap.Balance, &snap.Currency, &snap.BillTotal, &snap.BillCycle, &refreshedAtStr, &snap.RefreshError); err != nil {
			return nil, err
		}
		if trafficGB.Valid {
			val := trafficGB.Float64
			snap.TrafficGB = &val
		}
		snap.RefreshedAt = parseTime(refreshedAtStr)
		data.Snapshots[id] = &snap
	}

	// 3. settings
	setRows, err := b.db.Query("SELECT key, value FROM settings")
	if err != nil {
		return nil, fmt.Errorf("读取 settings 表失败: %w", err)
	}
	defer setRows.Close()
	for setRows.Next() {
		var k, v string
		if err := setRows.Scan(&k, &v); err != nil {
			return nil, err
		}
		data.Settings[k] = v
	}

	// 4. dns_records
	dnsRows, err := b.db.Query("SELECT record, target_kind, target_id, last_ip, last_status, last_at, sort_order FROM dns_records ORDER BY sort_order ASC, record ASC")
	if err != nil {
		return nil, fmt.Errorf("读取 dns_records 表失败: %w", err)
	}
	defer dnsRows.Close()
	for dnsRows.Next() {
		var rec DNSRecord
		if err := dnsRows.Scan(&rec.Record, &rec.TargetKind, &rec.TargetID, &rec.LastIP, &rec.LastStatus, &rec.LastAt, &rec.SortOrder); err != nil {
			return nil, err
		}
		data.DNSRecords = append(data.DNSRecords, rec)
	}

	// 5. groups
	grpRows, err := b.db.Query("SELECT id, name, enabled, failover_enabled, cycle, anchor_at, members, run FROM groups")
	if err != nil {
		return nil, fmt.Errorf("读取 groups 表失败: %w", err)
	}
	defer grpRows.Close()
	for grpRows.Next() {
		var g Group
		var enabled, failoverEnabled int
		var anchorAtStr, membersJSON, runJSON string
		if err := grpRows.Scan(&g.ID, &g.Name, &enabled, &failoverEnabled, &g.Cycle, &anchorAtStr, &membersJSON, &runJSON); err != nil {
			return nil, err
		}
		g.Enabled = enabled != 0
		g.FailoverEnabled = failoverEnabled != 0
		g.AnchorAt = parseTime(anchorAtStr)
		if membersJSON != "" {
			_ = json.Unmarshal([]byte(membersJSON), &g.Members)
		}
		if runJSON != "" {
			var r GroupRun
			if err := json.Unmarshal([]byte(runJSON), &r); err == nil {
				g.Run = &r
			}
		}
		data.Groups = append(data.Groups, g)
	}

	// 6. keepalives
	keepRows, err := b.db.Query("SELECT instance_id, enabled FROM keepalives")
	if err != nil {
		return nil, fmt.Errorf("读取 keepalives 表失败: %w", err)
	}
	defer keepRows.Close()
	for keepRows.Next() {
		var k Keepalive
		var enabled int
		if err := keepRows.Scan(&k.InstanceID, &enabled); err != nil {
			return nil, err
		}
		k.Enabled = enabled != 0
		data.Keepalives = append(data.Keepalives, k)
	}

	// 7. events
	evRows, err := b.db.Query("SELECT id, kind, title, detail, status, progress, pending_status, pending_kind, server_id, at, updated_at FROM events ORDER BY id ASC")
	if err != nil {
		return nil, fmt.Errorf("读取 events 表失败: %w", err)
	}
	defer evRows.Close()
	for evRows.Next() {
		var ev Event
		var progressJSON, atStr, upStr string
		if err := evRows.Scan(&ev.ID, &ev.Kind, &ev.Title, &ev.Detail, &ev.Status, &progressJSON, &ev.PendingStatus, &ev.PendingKind, &ev.ServerID, &atStr, &upStr); err != nil {
			return nil, err
		}
		if progressJSON != "" {
			_ = json.Unmarshal([]byte(progressJSON), &ev.Progress)
		}
		ev.At = parseTime(atStr)
		ev.UpdatedAt = parseTime(upStr)
		data.Events = append(data.Events, ev)
	}

	// 8. flags
	flagRows, err := b.db.Query("SELECT key, created_at, value FROM flags")
	if err != nil {
		return nil, fmt.Errorf("读取 flags 表失败: %w", err)
	}
	defer flagRows.Close()
	for flagRows.Next() {
		var k, catStr string
		var val int
		if err := flagRows.Scan(&k, &catStr, &val); err != nil {
			return nil, err
		}
		data.Flags[k] = flagEntry{
			CreatedAt: parseTime(catStr),
			Value:     val,
		}
	}

	// 9. samples
	smpRows, err := b.db.Query("SELECT instance_id, at, gb FROM samples ORDER BY id ASC")
	if err != nil {
		return nil, fmt.Errorf("读取 samples 表失败: %w", err)
	}
	defer smpRows.Close()
	for smpRows.Next() {
		var instID, atStr string
		var gb float64
		if err := smpRows.Scan(&instID, &atStr, &gb); err != nil {
			return nil, err
		}
		data.Samples[instID] = append(data.Samples[instID], Sample{
			At: parseTime(atStr),
			GB: gb,
		})
	}

	// 10. admin
	var admin AdminAccount
	err = b.db.QueryRow("SELECT username, pass_hash FROM admin LIMIT 1").Scan(&admin.Username, &admin.PassHash)
	if err == nil {
		data.Admin = &admin
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("读取 admin 表失败: %w", err)
	}

	// 11. sessions
	sessRows, err := b.db.Query("SELECT token, user, expires_at FROM sessions")
	if err != nil {
		return nil, fmt.Errorf("读取 sessions 表失败: %w", err)
	}
	defer sessRows.Close()
	for sessRows.Next() {
		var token, expStr string
		var s sessionEntry
		if err := sessRows.Scan(&token, &s.User, &expStr); err != nil {
			return nil, err
		}
		s.ExpiresAt = parseTime(expStr)
		data.Sessions[token] = s
	}

	// 12. meta
	var seqStr string
	err = b.db.QueryRow("SELECT value FROM meta WHERE key = 'event_seq'").Scan(&seqStr)
	if err == nil {
		var seq int64
		if _, err := fmt.Sscanf(seqStr, "%d", &seq); err == nil {
			data.EventSeq = seq
		}
	}

	return data, nil
}

func (b *sqliteBackend) Save(data *fileData, masterKey []byte) error {
	tx, err := b.db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	// 1. instances
	if _, err := tx.Exec("DELETE FROM instances"); err != nil {
		return err
	}
	instStmt, err := tx.Prepare("INSERT INTO instances(id, name, access_key_id, access_key_secret, region_id, instance_id, site, note, quota_gb, threshold_gb) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer instStmt.Close()

	for _, in := range data.Instances {
		secret := in.AccessKeySecret
		if masterKey != nil && secret != "" && !strings.HasPrefix(secret, secretPrefix) {
			enc, err := sealSecret(masterKey, []byte(secret))
			if err != nil {
				return fmt.Errorf("凭据加密失败: %w", err)
			}
			secret = enc
		}
		if _, err := instStmt.Exec(in.ID, in.Name, in.AccessKeyID, secret, in.RegionID, in.InstanceID, in.Site, in.Note, in.QuotaGB, in.ThresholdGB); err != nil {
			return err
		}
	}

	// 2. snapshots
	if _, err := tx.Exec("DELETE FROM snapshots"); err != nil {
		return err
	}
	snapStmt, err := tx.Prepare("INSERT INTO snapshots(instance_id, status, stopped_mode, public_ip, region_id, traffic_gb, balance, currency, bill_total, bill_cycle, refreshed_at, refresh_error) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer snapStmt.Close()

	for id, snap := range data.Snapshots {
		var trafficGB any = nil
		if snap.TrafficGB != nil {
			trafficGB = *snap.TrafficGB
		}
		if _, err := snapStmt.Exec(id, snap.Status, snap.StoppedMode, snap.PublicIP, snap.RegionID, trafficGB, snap.Balance, snap.Currency, snap.BillTotal, snap.BillCycle, formatTime(snap.RefreshedAt), snap.RefreshError); err != nil {
			return err
		}
	}

	// 3. settings
	if _, err := tx.Exec("DELETE FROM settings"); err != nil {
		return err
	}
	setStmt, err := tx.Prepare("INSERT INTO settings(key, value) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer setStmt.Close()

	for k, v := range data.Settings {
		if _, err := setStmt.Exec(k, v); err != nil {
			return err
		}
	}

	// 4. dns_records
	if _, err := tx.Exec("DELETE FROM dns_records"); err != nil {
		return err
	}
	dnsStmt, err := tx.Prepare("INSERT INTO dns_records(record, target_kind, target_id, last_ip, last_status, last_at, sort_order) VALUES (?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer dnsStmt.Close()

	for _, rec := range data.DNSRecords {
		if _, err := dnsStmt.Exec(rec.Record, rec.TargetKind, rec.TargetID, rec.LastIP, rec.LastStatus, rec.LastAt, rec.SortOrder); err != nil {
			return err
		}
	}

	// 5. groups
	if _, err := tx.Exec("DELETE FROM groups"); err != nil {
		return err
	}
	grpStmt, err := tx.Prepare("INSERT INTO groups(id, name, enabled, failover_enabled, cycle, anchor_at, members, run) VALUES (?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer grpStmt.Close()

	for _, g := range data.Groups {
		membersRaw, _ := json.Marshal(g.Members)
		var runRaw []byte
		if g.Run != nil {
			runRaw, _ = json.Marshal(g.Run)
		}
		en, fo := 0, 0
		if g.Enabled {
			en = 1
		}
		if g.FailoverEnabled {
			fo = 1
		}
		if _, err := grpStmt.Exec(g.ID, g.Name, en, fo, g.Cycle, formatTime(g.AnchorAt), string(membersRaw), string(runRaw)); err != nil {
			return err
		}
	}

	// 6. keepalives
	if _, err := tx.Exec("DELETE FROM keepalives"); err != nil {
		return err
	}
	keepStmt, err := tx.Prepare("INSERT INTO keepalives(instance_id, enabled) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer keepStmt.Close()

	for _, k := range data.Keepalives {
		en := 0
		if k.Enabled {
			en = 1
		}
		if _, err := keepStmt.Exec(k.InstanceID, en); err != nil {
			return err
		}
	}

	// 7. events
	if _, err := tx.Exec("DELETE FROM events"); err != nil {
		return err
	}
	evStmt, err := tx.Prepare("INSERT INTO events(id, kind, title, detail, status, progress, pending_status, pending_kind, server_id, at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer evStmt.Close()

	for _, ev := range data.Events {
		progRaw, _ := json.Marshal(ev.Progress)
		if _, err := evStmt.Exec(ev.ID, ev.Kind, ev.Title, ev.Detail, ev.Status, string(progRaw), ev.PendingStatus, ev.PendingKind, ev.ServerID, formatTime(ev.At), formatTime(ev.UpdatedAt)); err != nil {
			return err
		}
	}

	// 8. flags
	if _, err := tx.Exec("DELETE FROM flags"); err != nil {
		return err
	}
	flagStmt, err := tx.Prepare("INSERT INTO flags(key, created_at, value) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer flagStmt.Close()

	for k, f := range data.Flags {
		if _, err := flagStmt.Exec(k, formatTime(f.CreatedAt), f.Value); err != nil {
			return err
		}
	}

	// 9. samples
	if _, err := tx.Exec("DELETE FROM samples"); err != nil {
		return err
	}
	smpStmt, err := tx.Prepare("INSERT INTO samples(instance_id, at, gb) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer smpStmt.Close()

	for instID, list := range data.Samples {
		for _, s := range list {
			if _, err := smpStmt.Exec(instID, formatTime(s.At), s.GB); err != nil {
				return err
			}
		}
	}

	// 10. admin
	if _, err := tx.Exec("DELETE FROM admin"); err != nil {
		return err
	}
	if data.Admin != nil {
		if _, err := tx.Exec("INSERT INTO admin(username, pass_hash) VALUES (?, ?)", data.Admin.Username, data.Admin.PassHash); err != nil {
			return err
		}
	}

	// 11. sessions
	if _, err := tx.Exec("DELETE FROM sessions"); err != nil {
		return err
	}
	sessStmt, err := tx.Prepare("INSERT INTO sessions(token, user, expires_at) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer sessStmt.Close()

	for token, s := range data.Sessions {
		if _, err := sessStmt.Exec(token, s.User, formatTime(s.ExpiresAt)); err != nil {
			return err
		}
	}

	// 12. meta
	if _, err := tx.Exec("DELETE FROM meta WHERE key = 'event_seq'"); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO meta(key, value) VALUES ('event_seq', ?)", fmt.Sprintf("%d", data.EventSeq)); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	tx = nil
	return nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err == nil {
		return t
	}
	t, err = time.Parse(time.RFC3339, s)
	if err == nil {
		return t
	}
	return time.Time{}
}

