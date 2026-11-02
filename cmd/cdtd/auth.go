package main

// 管理员账户与会话：账号密码登录与初始化向导。
//
// 模式判定：
//   - 存在管理员 → 账号密码模式（登录成功发放会话 token）
//   - 无管理员但传了 -token → 静态口令模式（兼容旧行为）
//   - 两者都无 → 免鉴权
//   - 无管理员时 /api/setup 开放（仅此端点），用于初始化向导

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const sessionTTL = 14 * 24 * time.Hour

// AdminAccount 是唯一的管理员（单用户场景）。
type AdminAccount struct {
	Username string `json:"username"`
	PassHash string `json:"pass_hash"` // pbkdf2_sha256$...
}

type sessionEntry struct {
	User      string    `json:"user"`
	ExpiresAt time.Time `json:"expires_at"`
}

// --- 存储方法 -----------------------------------------------------------------

func (s *Store) AdminGet() (AdminAccount, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data.Admin == nil {
		return AdminAccount{}, false
	}
	return *s.data.Admin, true
}

func (s *Store) AdminSet(a AdminAccount) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Admin = &a
	return s.save()
}

// SessionCreate 发放会话 token（随机 256bit）。
func (s *Store) SessionCreate(user string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Sessions == nil {
		s.data.Sessions = map[string]sessionEntry{}
	}
	s.data.Sessions[token] = sessionEntry{User: user, ExpiresAt: time.Now().Add(sessionTTL)}
	_ = s.save()
	return token, nil
}

// SessionValid 校验会话（过期即失效；惰性清理）。
func (s *Store) SessionValid(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data.Sessions[token]
	if !ok {
		return "", false
	}
	if time.Now().After(e.ExpiresAt) {
		delete(s.data.Sessions, token)
		_ = s.save()
		return "", false
	}
	return e.User, true
}

// SessionDelete 登出。
func (s *Store) SessionDelete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Sessions[token]; ok {
		delete(s.data.Sessions, token)
		_ = s.save()
	}
}

// --- API ----------------------------------------------------------------------

type loginCheck struct{ ok, authRequired bool }

// authMode 判定当前鉴权形态。
func (s *Server) authMode() string {
	if _, ok := s.store.AdminGet(); ok {
		return "account"
	}
	if s.authToken != "" {
		return "static"
	}
	return "none"
}

// handleInitStatus 初始化向导探测：管理员是否已创建。
func (s *Server) handleInitStatus(w http.ResponseWriter, r *http.Request) {
	_, has := s.store.AdminGet()
	jsonOK(w, map[string]any{"initialized": has, "mode": s.authMode()})
}

// handleSetup 初始化向导：创建管理员（仅当不存在时；密码 ≥10 位）。
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if _, has := s.store.AdminGet(); has {
		jsonErr(w, http.StatusForbidden, "管理员已存在（如需重置请删除数据文件中的 admin 字段）")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" || body.Password == "" {
		jsonErr(w, http.StatusBadRequest, "用户名和密码不能为空")
		return
	}
	hash, err := HashPassword(body.Password)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.AdminSet(AdminAccount{Username: body.Username, PassHash: hash}); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.logEvent("info", "管理员账户已创建：%s", body.Username)
	jsonOK(w, map[string]bool{"ok": true})
}

// handleLogin 登录：账号密码模式校验用户名+密码并发放会话；
// 静态口令模式沿用旧逻辑（防爆破锁定照旧）。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Token    string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	switch s.authMode() {
	case "account":
		admin, _ := s.store.AdminGet()
		if !s.login.allow() {
			jsonErr(w, http.StatusTooManyRequests, fmt.Sprintf("尝试次数过多，请 %d 秒后再试", loginLockSeconds))
			return
		}
		if body.Username != admin.Username || !VerifyPassword(body.Password, admin.PassHash) {
			s.login.fail()
			jsonErr(w, http.StatusUnauthorized, "用户名或密码错误")
			return
		}
		s.login.reset()
		token, err := s.store.SessionCreate(admin.Username)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonOK(w, map[string]any{"ok": true, "token": token, "username": admin.Username})
	case "static":
		if !s.login.allow() {
			jsonErr(w, http.StatusTooManyRequests, fmt.Sprintf("尝试次数过多，请 %d 秒后再试", loginLockSeconds))
			return
		}
		if subtle.ConstantTimeCompare([]byte(body.Token), []byte(s.authToken)) != 1 {
			s.login.fail()
			jsonErr(w, http.StatusUnauthorized, "口令错误")
			return
		}
		s.login.reset()
		jsonOK(w, map[string]any{"ok": true, "token": body.Token, "username": "admin"})
	default:
		jsonOK(w, map[string]any{"ok": true, "username": ""}) // 免鉴权部署
	}
}

// handleMe 当前登录用户（会话或静态口令皆可）。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	token := bearerOf(r)
	if user, ok := s.store.SessionValid(token); ok {
		jsonOK(w, map[string]string{"username": user})
		return
	}
	if s.authToken != "" && token == s.authToken {
		jsonOK(w, map[string]string{"username": "admin"})
		return
	}
	jsonErr(w, http.StatusUnauthorized, "未登录")
}

// handleLogout 登出（吊销会话）。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.store.SessionDelete(bearerOf(r))
	jsonOK(w, map[string]bool{"ok": true})
}

// handleChangePassword 修改管理员密码（需已登录）。
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	admin, has := s.store.AdminGet()
	if !has {
		jsonErr(w, http.StatusForbidden, "未初始化管理员")
		return
	}
	var body struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	if body.New == "" {
		jsonErr(w, http.StatusBadRequest, "新密码不能为空")
		return
	}
	if !VerifyPassword(body.Old, admin.PassHash) {
		jsonErr(w, http.StatusUnauthorized, "旧密码错误")
		return
	}
	hash, err := HashPassword(body.New)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.AdminSet(AdminAccount{Username: admin.Username, PassHash: hash}); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.logEvent("info", "管理员密码已修改")
	jsonOK(w, map[string]bool{"ok": true})
}

// bearerOf 从请求取 Bearer token（兼容 ?token=）。
func bearerOf(r *http.Request) string {
	t := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if t == "" {
		t = r.URL.Query().Get("token")
	}
	return t
}

// withToken 升级：会话 token 或静态口令皆可通过；登录/初始化端点放行。
func (s *Server) withAuth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			h.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/login" || r.URL.Path == "/api/setup" || r.URL.Path == "/api/setup/status" {
			h.ServeHTTP(w, r)
			return
		}
		switch s.authMode() {
		case "none":
			h.ServeHTTP(w, r)
			return
		case "static":
			if bearerOf(r) == s.authToken {
				h.ServeHTTP(w, r)
				return
			}
			jsonErr(w, http.StatusUnauthorized, "token 无效（-token 启用了鉴权）")
			return
		default: // account
			if user, ok := s.store.SessionValid(bearerOf(r)); ok {
				r.Header.Set("X-Cdt-User", user)
				h.ServeHTTP(w, r)
				return
			}
			jsonErr(w, http.StatusUnauthorized, "未登录")
		}
	})
}
