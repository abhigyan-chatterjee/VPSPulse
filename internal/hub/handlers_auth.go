package hub

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"vpspulse/internal/store"
)

const sessionTTL = 7 * 24 * time.Hour

type rateBucket struct {
	tokens    float64
	lastCheck time.Time
}

type ipRateLimiter struct {
	mu      sync.Mutex
	rate    float64 // tokens per second
	burst   float64
	buckets map[string]*rateBucket
}

func newIPRateLimiter(perMinute, burst int) *ipRateLimiter {
	return &ipRateLimiter{
		rate:    float64(perMinute) / 60.0,
		burst:   float64(burst),
		buckets: map[string]*rateBucket{},
	}
}

func (l *ipRateLimiter) Allow(ip string) bool {
	if ip == "" {
		ip = "unknown"
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.buckets) > 4096 {
		cutoff := now.Add(-15 * time.Minute)
		for k, b := range l.buckets {
			if b.lastCheck.Before(cutoff) {
				delete(l.buckets, k)
			}
		}
	}

	b, ok := l.buckets[ip]
	if !ok {
		b = &rateBucket{tokens: l.burst, lastCheck: now}
		l.buckets[ip] = b
	} else {
		elapsed := now.Sub(b.lastCheck).Seconds()
		b.tokens += elapsed * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.lastCheck = now
	}
	if b.tokens < 1.0 {
		return false
	}
	b.tokens -= 1.0
	return true
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	hasAdmin, err := s.store.HasAdmin()
	if err != nil {
		writeInternalError(w, "auth status", err)
		return
	}
	authenticated := false
	username := ""
	if c, err := r.Cookie("vpspulse_session"); err == nil && c.Value != "" {
		if sess, err := s.store.GetSession(c.Value); err == nil {
			authenticated = true
			username = sess.Username
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"needs_setup":   !hasAdmin,
		"authenticated": authenticated,
		"username":      username,
		"demo_mode":     s.cfg.DemoMode,
		"public_url":    s.resolvePublicURL(r),
	})
}

func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if code, err := decodeJSON(w, r, &body, 16<<10); err != nil {
		writeError(w, code, err.Error())
		return
	}
	u, err := s.store.CreateFirstAdmin(body.Username, body.Password)
	if err != nil {
		if errors.Is(err, store.ErrAdminAlreadyExists) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tok, err := s.store.CreateSession(u.ID, sessionTTL)
	if err != nil {
		writeInternalError(w, "create session", err)
		return
	}
	s.setSessionCookie(w, r, tok, int(sessionTTL.Seconds()))
	writeJSON(w, http.StatusCreated, map[string]any{"username": u.Username, "authenticated": true})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow(s.clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many login attempts, please wait a minute")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if code, err := decodeJSON(w, r, &body, 16<<10); err != nil {
		writeError(w, code, err.Error())
		return
	}
	u, err := s.store.VerifyAdmin(body.Username, body.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	tok, err := s.store.CreateSession(u.ID, sessionTTL)
	if err != nil {
		writeInternalError(w, "create session", err)
		return
	}
	s.setSessionCookie(w, r, tok, int(sessionTTL.Seconds()))
	writeJSON(w, http.StatusOK, map[string]any{"username": u.Username, "authenticated": true})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("vpspulse_session"); err == nil && c.Value != "" {
		_ = s.store.DeleteSession(c.Value)
	}
	s.setSessionCookie(w, r, "", -1)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromCtx(r)
	if sess == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if code, err := decodeJSON(w, r, &body, 16<<10); err != nil {
		writeError(w, code, err.Error())
		return
	}
	if _, err := s.store.VerifyAdmin(sess.Username, body.CurrentPassword); err != nil {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	if err := s.store.UpdatePassword(sess.UserID, body.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_ = s.store.DeleteUserSessions(sess.UserID)
	tok, err := s.store.CreateSession(sess.UserID, sessionTTL)
	if err == nil {
		s.setSessionCookie(w, r, tok, int(sessionTTL.Seconds()))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, val string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     "vpspulse_session",
		Value:    val,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.isSecureRequest(r),
		MaxAge:   maxAge,
	})
}
