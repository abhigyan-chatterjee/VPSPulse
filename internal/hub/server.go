package hub

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"

	"vpspulse/internal/store"
)

// Config holds runtime configuration for the hub HTTP server.
type Config struct {
	PublicURL    string
	TrustProxy   bool
	DownloadsDir string
	DemoMode     bool
}

// Server wraps the HTTP router, store, SSE broker, prober, and alert manager.
type Server struct {
	cfg      Config
	store    *store.Store
	broker   *Broker
	prober   *Prober
	alerts   *AlertManager
	staticFS fs.FS
	mux      *http.ServeMux
}

// NewServer constructs a configured HTTP Server.
func NewServer(cfg Config, st *store.Store, broker *Broker, prober *Prober, alerts *AlertManager, staticFS fs.FS) *Server {
	s := &Server{
		cfg:      cfg,
		store:    st,
		broker:   broker,
		prober:   prober,
		alerts:   alerts,
		staticFS: staticFS,
		mux:      http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

// Handler returns the root http.Handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) registerRoutes() {
	// Public routes
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/auth/status", s.handleAuthStatus)
	s.mux.HandleFunc("POST /api/v1/auth/setup", s.handleAuthSetup)
	s.mux.HandleFunc("POST /api/v1/auth/login", s.handleAuthLogin)
	s.mux.HandleFunc("POST /api/v1/agent/heartbeat", s.handleAgentHeartbeat)
	s.mux.HandleFunc("GET /install.sh", s.handleInstallScript)
	s.mux.HandleFunc("GET /downloads/{file}", s.handleDownloads)

	// Authenticated UI/API routes
	s.mux.Handle("POST /api/v1/auth/logout", s.requireAuth(http.HandlerFunc(s.handleAuthLogout)))
	s.mux.Handle("POST /api/v1/auth/password", s.requireAuth(http.HandlerFunc(s.handleChangePassword)))
	s.mux.Handle("GET /api/v1/events", s.requireAuth(s.broker))

	s.mux.Handle("GET /api/v1/vps", s.requireAuth(http.HandlerFunc(s.handleListVPS)))
	s.mux.Handle("POST /api/v1/vps", s.requireAuth(http.HandlerFunc(s.handleCreateVPS)))
	s.mux.Handle("GET /api/v1/vps/{id}", s.requireAuth(http.HandlerFunc(s.handleGetVPS)))
	s.mux.Handle("PATCH /api/v1/vps/{id}", s.requireAuth(http.HandlerFunc(s.handleUpdateVPS)))
	s.mux.Handle("DELETE /api/v1/vps/{id}", s.requireAuth(http.HandlerFunc(s.handleDeleteVPS)))
	s.mux.Handle("POST /api/v1/vps/{id}/rotate-token", s.requireAuth(http.HandlerFunc(s.handleRotateVPSToken)))
	s.mux.Handle("GET /api/v1/vps/{id}/metrics", s.requireAuth(http.HandlerFunc(s.handleVPSMetrics)))
	s.mux.Handle("PUT /api/v1/vps/{id}/systemd", s.requireAuth(http.HandlerFunc(s.handleSetWatchedUnits)))
	s.mux.Handle("PATCH /api/v1/vps/{id}/containers/{name}", s.requireAuth(http.HandlerFunc(s.handleContainerPolicy)))

	s.mux.Handle("GET /api/v1/probes", s.requireAuth(http.HandlerFunc(s.handleListProbes)))
	s.mux.Handle("POST /api/v1/probes", s.requireAuth(http.HandlerFunc(s.handleCreateProbe)))
	s.mux.Handle("PATCH /api/v1/probes/{id}", s.requireAuth(http.HandlerFunc(s.handleUpdateProbe)))
	s.mux.Handle("DELETE /api/v1/probes/{id}", s.requireAuth(http.HandlerFunc(s.handleDeleteProbe)))
	s.mux.Handle("POST /api/v1/probes/{id}/check", s.requireAuth(http.HandlerFunc(s.handleCheckProbeNow)))

	s.mux.Handle("GET /api/v1/incidents", s.requireAuth(http.HandlerFunc(s.handleListIncidents)))
	s.mux.Handle("POST /api/v1/incidents/{id}/ack", s.requireAuth(http.HandlerFunc(s.handleAckIncident)))

	s.mux.Handle("GET /api/v1/channels", s.requireAuth(http.HandlerFunc(s.handleListChannels)))
	s.mux.Handle("POST /api/v1/channels", s.requireAuth(http.HandlerFunc(s.handleCreateChannel)))
	s.mux.Handle("PATCH /api/v1/channels/{id}", s.requireAuth(http.HandlerFunc(s.handleUpdateChannel)))
	s.mux.Handle("DELETE /api/v1/channels/{id}", s.requireAuth(http.HandlerFunc(s.handleDeleteChannel)))
	s.mux.Handle("POST /api/v1/channels/{id}/test", s.requireAuth(http.HandlerFunc(s.handleTestChannel)))

	s.mux.Handle("GET /api/v1/settings", s.requireAuth(http.HandlerFunc(s.handleGetSettings)))
	s.mux.Handle("PATCH /api/v1/settings", s.requireAuth(http.HandlerFunc(s.handleUpdateSettings)))

	// Embedded SPA static files with index.html fallback
	s.mux.HandleFunc("/", s.handleSPA)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "demo_mode": s.cfg.DemoMode})
}

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "endpoint not found")
		return
	}
	if s.staticFS == nil {
		http.NotFound(w, r)
		return
	}
	clean := strings.TrimPrefix(r.URL.Path, "/")
	if clean == "" {
		clean = "index.html"
	}
	if info, err := fs.Stat(s.staticFS, clean); err == nil && !info.IsDir() {
		http.FileServer(http.FS(s.staticFS)).ServeHTTP(w, r)
		return
	}
	// SPA client-side route fallback
	indexBytes, err := fs.ReadFile(s.staticFS, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexBytes)
}

type ctxKey string

const sessionCtxKey ctxKey = "session"

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("vpspulse_session")
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		sess, err := s.store.GetSession(cookie.Value)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}
		ctx := context.WithValue(r.Context(), sessionCtxKey, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func sessionFromCtx(r *http.Request) *store.Session {
	v, _ := r.Context().Value(sessionCtxKey).(*store.Session)
	return v
}

func (s *Server) isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if s.cfg.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	if strings.HasPrefix(strings.ToLower(s.cfg.PublicURL), "https://") {
		return true
	}
	return false
}

func (s *Server) resolvePublicURL(r *http.Request) string {
	if s.cfg.PublicURL != "" {
		return strings.TrimRight(s.cfg.PublicURL, "/")
	}
	scheme := "http"
	if s.isSecureRequest(r) {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "localhost:8080"
	}
	return scheme + "://" + host
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, dst any) error {
	defer func() { _ = r.Body.Close() }()
	dec := json.NewDecoder(r.Body)
	return dec.Decode(dst)
}
