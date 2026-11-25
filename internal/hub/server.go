package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"

	"vpspulse/internal/store"
	"vpspulse/internal/version"
)

var validHostHeaderRe = regexp.MustCompile(`^[A-Za-z0-9.\-:\[\]]+$`)

// Config holds runtime configuration for the hub HTTP server.
type Config struct {
	PublicURL    string
	TrustProxy   bool
	DownloadsDir string
	DemoMode     bool
}

// Server wraps the HTTP router, store, SSE broker, prober, and alert manager.
type Server struct {
	ctx      context.Context
	cfg      Config
	store    *store.Store
	broker   *Broker
	prober   *Prober
	alerts   *AlertManager
	staticFS fs.FS
	mux      *http.ServeMux
	limiter  *ipRateLimiter
}

// NewServer constructs a configured HTTP Server.
func NewServer(ctx context.Context, cfg Config, st *store.Store, broker *Broker, prober *Prober, alerts *AlertManager, staticFS fs.FS) *Server {
	if ctx == nil {
		ctx = context.Background()
	}
	if broker != nil {
		broker.SetStore(st)
	}
	s := &Server{
		ctx:      ctx,
		cfg:      cfg,
		store:    st,
		broker:   broker,
		prober:   prober,
		alerts:   alerts,
		staticFS: staticFS,
		mux:      http.NewServeMux(),
		limiter:  newIPRateLimiter(5, 5), // 5 attempts/min + burst 5 per IP
	}
	s.registerRoutes()
	return s
}

// Handler returns the root http.Handler wrapped with security headers and CSRF origin protection.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive, nosnippet, noimageindex")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")

		// Enforce same-origin checks on state-changing browser requests (exempting bearer-authed agent heartbeats).
		if r.URL.Path != "/api/v1/agent/heartbeat" && isStateChangingMethod(r.Method) {
			if !s.isSameOriginRequest(r) {
				writeError(w, http.StatusForbidden, "cross-origin request blocked")
				return
			}
		}
		s.mux.ServeHTTP(w, r)
	})
}

func isStateChangingMethod(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodPatch || m == http.MethodDelete
}

func (s *Server) isSameOriginRequest(r *http.Request) bool {
	if sfs := strings.ToLower(r.Header.Get("Sec-Fetch-Site")); sfs != "" {
		return sfs == "same-origin" || sfs == "none"
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" {
			return false
		}
		return strings.EqualFold(u.Host, r.Host)
	}
	return true
}

func (s *Server) registerRoutes() {
	// Public routes
	s.mux.HandleFunc("GET /robots.txt", s.handleRobotsTxt)
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

func (s *Server) handleRobotsTxt(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"demo_mode": s.cfg.DemoMode,
		"version":   version.String(),
	})
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
		u, err := url.Parse(strings.TrimRight(s.cfg.PublicURL, "/"))
		if err == nil && (u.Scheme == "http" || u.Scheme == "https") && validHostHeaderRe.MatchString(u.Host) && u.User == nil {
			return u.Scheme + "://" + u.Host + u.Path
		}
		return ""
	}
	scheme := "http"
	if s.isSecureRequest(r) {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = "localhost:8080"
	}
	if !validHostHeaderRe.MatchString(host) {
		return ""
	}
	return scheme + "://" + host
}

// clientIP extracts the caller IP, trusting proxy headers only when TrustProxy is enabled.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); net.ParseIP(cf) != nil {
			return cf
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			for _, part := range strings.Split(xff, ",") {
				ip := strings.TrimSpace(part)
				if net.ParseIP(ip) != nil {
					return ip
				}
			}
		}
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(xri) != nil {
			return xri
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && net.ParseIP(host) != nil {
		return host
	}
	if net.ParseIP(strings.TrimSpace(r.RemoteAddr)) != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return ""
}

// isPublicIP returns true only for globally routable unicast IPv4/IPv6 addresses.
func isPublicIP(ipStr string) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(ipStr))
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	// Reject CGNAT 100.64.0.0/10
	cgnat := netip.MustParsePrefix("100.64.0.0/10")
	if cgnat.Contains(addr) {
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeInternalError(w http.ResponseWriter, contextMsg string, err error) {
	log.Printf("internal error (%s): %v", contextMsg, err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) (int, error) {
	if maxBytes <= 0 {
		maxBytes = 64 << 10
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	defer func() { _ = r.Body.Close() }()
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return http.StatusRequestEntityTooLarge, errors.New("request body too large")
		}
		return http.StatusBadRequest, errors.New("invalid json")
	}
	return 0, nil
}
