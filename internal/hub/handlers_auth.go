package hub

import (
	"net/http"
	"time"
)

const sessionTTL = 7 * 24 * time.Hour

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	hasAdmin, err := s.store.HasAdmin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
	hasAdmin, err := s.store.HasAdmin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if hasAdmin {
		writeError(w, http.StatusConflict, "admin account already exists")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	u, err := s.store.CreateAdmin(body.Username, body.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tok, err := s.store.CreateSession(u.ID, sessionTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.setSessionCookie(w, r, tok, int(sessionTTL.Seconds()))
	writeJSON(w, http.StatusCreated, map[string]any{"username": u.Username, "authenticated": true})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	u, err := s.store.VerifyAdmin(body.Username, body.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	tok, err := s.store.CreateSession(u.ID, sessionTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
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
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, val string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     "vpspulse_session",
		Value:    val,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.isSecureRequest(r),
		MaxAge:   maxAge,
	})
}
