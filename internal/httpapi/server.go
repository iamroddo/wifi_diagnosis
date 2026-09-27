// Package httpapi wires together HTTP routes for the diagnostic server.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"wifi-diagnostics/internal/clientip"
	"wifi-diagnostics/internal/config"
	"wifi-diagnostics/internal/diag"
	"wifi-diagnostics/internal/session"
	"wifi-diagnostics/internal/store"
	"wifi-diagnostics/internal/unifi"
)

// Server holds all handler dependencies.
type Server struct {
	logger         *slog.Logger
	diagHandler    *diag.Handler
	sessions       *session.Manager
	store          *store.Store
	unifiClient    unifi.UniFiClient
	trustedProxies []net.IP
	frontendFS     http.FileSystem
	siteTitle      string
	contactName    string
	contactEmail   string
}

// NewServer creates an httpapi.Server.
func NewServer(
	logger *slog.Logger,
	diagH *diag.Handler,
	sessions *session.Manager,
	st *store.Store,
	uc unifi.UniFiClient,
	trusted []net.IP,
	frontendFS http.FileSystem,
	cfg *config.Config,
) *Server {
	return &Server{
		logger:         logger,
		diagHandler:    diagH,
		sessions:       sessions,
		store:          st,
		unifiClient:    uc,
		trustedProxies: trusted,
		frontendFS:     frontendFS,
		siteTitle:      cfg.SiteTitle,
		contactName:    cfg.ContactName,
		contactEmail:   cfg.ContactEmail,
	}
}

// Handler returns the root http.Handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Diagnostic WebSocket
	mux.HandleFunc("/ws", s.handleWS)

	// HTTP fallbacks
	mux.HandleFunc("/ping", diag.HTTPLatencyHandler)
	mux.HandleFunc("/download", diag.HTTPDownloadHandler)

	// Session API
	mux.HandleFunc("/api/session", s.handleSessionStart)
	mux.HandleFunc("/api/session/", s.handleSessionGet)

	// UI config
	mux.HandleFunc("/api/config", s.handleConfig)

	// Health
	mux.HandleFunc("/healthz", s.handleHealthz)

	// Admin
	mux.HandleFunc("/admin", s.handleAdmin)
	mux.HandleFunc("/admin/", s.handleAdmin)
	mux.HandleFunc("/api/admin/sessions", s.handleAdminSessions)

	// Frontend (catch-all)
	mux.Handle("/", http.FileServer(s.frontendFS))

	return mux
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	s.diagHandler.ServeWS(w, r)
}

func (s *Server) handleSessionStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientIP := clientip.FromRequest(r, s.trustedProxies)
	ua := r.UserAgent()

	sess, err := s.sessions.Create(clientIP, ua)
	if err != nil {
		s.logger.Error("session: create failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	s.logger.Info("session: created",
		"session_id", sess.ID, "client_ip", clientIP)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"session_id": sess.ID,
		"client_ip":  clientIP,
	})
}

func (s *Server) handleSessionGet(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/api/session/"):]
	if id == "" {
		http.Error(w, "session id required", http.StatusBadRequest)
		return
	}

	sess := s.sessions.Get(id)
	if sess == nil {
		// Fall back to stored sessions.
		stored, err := s.store.Get(r.Context(), id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stored) //nolint:errcheck
		return
	}

	snap := sess.Snapshot()
	resp := map[string]any{
		"session_id":         sess.ID,
		"client_ip":          sess.ClientIP,
		"correlation_status": snap.CorrelationStatus,
	}
	if snap.UnifiClient != nil {
		cl := snap.UnifiClient
		resp["wifi"] = map[string]any{
			"ssid":          cl.ESSID,
			"ap_mac":        cl.APMAC,
			"ap_name":       cl.APName,
			"band":          cl.Band,
			"channel":       cl.Channel,
			"channel_width": cl.ChannelWidth,
			"rssi_dbm":      cl.RSSI,
			"tx_rate_bps":   cl.TXRate,
			"rx_rate_bps":   cl.RXRate,
			"retries":       cl.Retries,
			"satisfaction":  cl.Satisfaction,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	unifiOK := false
	if s.unifiClient != nil {
		if err := s.unifiClient.Ping(ctx); err == nil {
			unifiOK = true
		}
	}

	// The health endpoint returns 200 regardless of UniFi status.
	// UniFi unavailability is a degraded state, not a fatal one.
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"status":   "ok",
		"unifi_ok": unifiOK,
		"time":     time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	// Serve the admin SPA page — auth is delegated to the reverse proxy.
	http.ServeFile(w, r, "admin.html")
}

func (s *Server) handleAdminSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.store.List(r.Context(), 50)
	if err != nil {
		s.logger.Error("admin: list sessions failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if sessions == nil {
		sessions = []*store.DiagSession{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions) //nolint:errcheck
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"title":         s.siteTitle,
		"contact_name":  s.contactName,
		"contact_email": s.contactEmail,
	})
}
