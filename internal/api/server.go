package api

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/darkerline/agent/internal/config"
	"github.com/darkerline/agent/internal/metrics"
	"github.com/darkerline/agent/internal/xray"
)

type Server struct {
	cfg     *config.Config
	manager *xray.Manager
	process *xray.Process
}

func New(cfg *config.Config, manager *xray.Manager, process *xray.Process) *Server {
	return &Server{cfg: cfg, manager: manager, process: process}
}

func (s *Server) Run() error {
	mux := http.NewServeMux()

	// Auth middleware applied to all routes
	mux.HandleFunc("/health", s.auth(s.handleHealth))
	mux.HandleFunc("/metrics", s.auth(s.handleMetrics))
	mux.HandleFunc("/xray/status", s.auth(s.handleXrayStatus))
	mux.HandleFunc("/xray/reload", s.auth(s.handleXrayReload))
	mux.HandleFunc("/xray/restart", s.auth(s.handleXrayRestart))
	mux.HandleFunc("/xray/inbounds", s.auth(s.handleInbounds))
	mux.HandleFunc("/clients", s.auth(s.handleClients))
	mux.HandleFunc("/clients/add", s.auth(s.handleAddClient))
	mux.HandleFunc("/clients/remove", s.auth(s.handleRemoveClient))
	mux.HandleFunc("/clients/update", s.auth(s.handleUpdateClient))
	mux.HandleFunc("/inbound/ensure", s.auth(s.handleEnsureInbound))

	srv := &http.Server{
		Addr:         s.cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	// TLS if cert/key provided
	if s.cfg.TLSCert != "" && s.cfg.TLSKey != "" {
		cert, err := tls.LoadX509KeyPair(s.cfg.TLSCert, s.cfg.TLSKey)
		if err != nil {
			return fmt.Errorf("load TLS: %w", err)
		}
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
		log.Printf("Agent listening on %s (TLS)", s.cfg.ListenAddr)
		return srv.ListenAndServeTLS("", "")
	}

	log.Printf("Agent listening on %s", s.cfg.ListenAddr)
	return srv.ListenAndServe()
}

// ── Auth middleware ────────────────────────────────────────────────────────────

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AgentToken == "" {
			// No token configured — allow all (dev mode)
			next(w, r)
			return
		}

		token := r.Header.Get("X-Agent-Token")
		if token == "" {
			// Also check Authorization: Bearer <token>
			auth := r.Header.Get("Authorization")
			if strings.HasPrefix(auth, "Bearer ") {
				token = strings.TrimPrefix(auth, "Bearer ")
			}
		}

		if token != s.cfg.AgentToken {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func decodeBody(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// ── Handlers ───────────────────────────────────────────────────────────────────

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]interface{}{
		"ok":          true,
		"server_name": s.cfg.ServerName,
		"xray":        s.process.IsRunning(),
		"uptime":      int64(s.process.Uptime().Seconds()),
		"time":        time.Now().UTC(),
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	m, err := metrics.Collect()
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	m2 := map[string]interface{}{
		"cpu_percent":    roundf(m.CPUPercent),
		"ram_percent":    roundf(m.RAMPercent),
		"ram_used_mb":    m.RAMUsedMB,
		"ram_total_mb":   m.RAMTotalMB,
		"disk_percent":   roundf(m.DiskPercent),
		"disk_used_gb":   roundf(m.DiskUsedGB),
		"disk_total_gb":  roundf(m.DiskTotalGB),
		"net_rx_bytes":   m.NetRxBytes,
		"net_tx_bytes":   m.NetTxBytes,
		"uptime_seconds": m.UptimeSecs,
		"load_avg_1":     m.LoadAvg1,
		"xray_running":   s.process.IsRunning(),
		"xray_uptime":    int64(s.process.Uptime().Seconds()),
	}

	// Count active clients
	clients, _ := s.manager.ListClients()
	total := 0
	for _, list := range clients {
		total += len(list)
	}
	m2["clients_count"] = total

	jsonOK(w, m2)
}

func (s *Server) handleXrayStatus(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]interface{}{
		"running": s.process.IsRunning(),
		"uptime":  int64(s.process.Uptime().Seconds()),
		"version": s.process.Version(s.cfg.XrayBin),
	})
}

func (s *Server) handleXrayReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, 405, "method not allowed")
		return
	}
	if err := s.process.Reload(); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

func (s *Server) handleXrayRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, 405, "method not allowed")
		return
	}
	if err := s.process.Restart(); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

func (s *Server) handleInbounds(w http.ResponseWriter, r *http.Request) {
	inbounds, err := s.manager.GetInbounds()
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	jsonOK(w, inbounds)
}

func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	clients, err := s.manager.ListClients()
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	jsonOK(w, clients)
}

// POST /clients/add
type AddClientReq struct {
	InboundTag string `json:"inbound_tag"`
	ID         string `json:"id"` // UUID
	Email      string `json:"email"`
	Flow       string `json:"flow"`
	TotalGB    int64  `json:"total_gb"`
	ExpiryMs   int64  `json:"expiry_ms"`
}

func (s *Server) handleAddClient(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, 405, "method not allowed")
		return
	}
	var req AddClientReq
	if err := decodeBody(r, &req); err != nil {
		jsonErr(w, 400, "invalid body: "+err.Error())
		return
	}
	if req.ID == "" || req.Email == "" {
		jsonErr(w, 400, "id and email are required")
		return
	}

	client := xray.VlessClient{
		ID:         req.ID,
		Email:      req.Email,
		Flow:       req.Flow,
		TotalGB:    req.TotalGB * 1024 * 1024 * 1024, // convert GB to bytes
		ExpiryTime: req.ExpiryMs,
		Enable:     true,
	}

	if err := s.manager.AddClient(req.InboundTag, client); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}

	if err := s.process.Reload(); err != nil {
		log.Printf("xray apply after add client failed: %v", err)
		jsonErr(w, http.StatusBadGateway, "client saved, but xray apply failed: "+err.Error())
		return
	}

	jsonOK(w, map[string]string{"ok": "true", "id": req.ID, "email": req.Email})
}

// POST /clients/remove
type RemoveClientReq struct {
	InboundTag string `json:"inbound_tag"`
	ID         string `json:"id"`
}

func (s *Server) handleRemoveClient(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, 405, "method not allowed")
		return
	}
	var req RemoveClientReq
	if err := decodeBody(r, &req); err != nil {
		jsonErr(w, 400, "invalid body: "+err.Error())
		return
	}
	if req.ID == "" {
		jsonErr(w, 400, "id is required")
		return
	}

	if err := s.manager.RemoveClient(req.InboundTag, req.ID); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}

	if err := s.process.Reload(); err != nil {
		log.Printf("xray apply after remove client failed: %v", err)
		jsonErr(w, http.StatusBadGateway, "client removed, but xray apply failed: "+err.Error())
		return
	}

	jsonOK(w, map[string]bool{"ok": true})
}

// POST /clients/update
type UpdateClientReq struct {
	InboundTag string `json:"inbound_tag"`
	ID         string `json:"id"`
	ExpiryMs   int64  `json:"expiry_ms"`
	TotalGB    int64  `json:"total_gb"`
}

func (s *Server) handleUpdateClient(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, 405, "method not allowed")
		return
	}
	var req UpdateClientReq
	if err := decodeBody(r, &req); err != nil {
		jsonErr(w, 400, "invalid body: "+err.Error())
		return
	}

	totalBytes := req.TotalGB * 1024 * 1024 * 1024
	if err := s.manager.UpdateClient(req.InboundTag, req.ID, req.ExpiryMs, totalBytes); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}

	if err := s.process.Reload(); err != nil {
		log.Printf("xray apply after update client failed: %v", err)
		jsonErr(w, http.StatusBadGateway, "client updated, but xray apply failed: "+err.Error())
		return
	}

	jsonOK(w, map[string]bool{"ok": true})
}

// POST /inbound/ensure
type EnsureInboundReq struct {
	Tag         string   `json:"tag"`
	Port        int      `json:"port"`
	PrivateKey  string   `json:"private_key"`
	ShortIDs    []string `json:"short_ids"`
	ServerNames []string `json:"server_names"`
	Dest        string   `json:"dest"`
}

func (s *Server) handleEnsureInbound(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, 405, "method not allowed")
		return
	}
	var req EnsureInboundReq
	if err := decodeBody(r, &req); err != nil {
		jsonErr(w, 400, "invalid body: "+err.Error())
		return
	}
	if req.Tag == "" || req.Port == 0 || req.PrivateKey == "" {
		jsonErr(w, 400, "tag, port, private_key are required")
		return
	}

	if err := s.manager.EnsureRealityInbound(req.Tag, req.Port, req.PrivateKey, req.ShortIDs, req.ServerNames, req.Dest); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}

	if err := s.process.Reload(); err != nil {
		log.Printf("xray apply after ensure inbound failed: %v", err)
		jsonErr(w, http.StatusBadGateway, "inbound saved, but xray apply failed: "+err.Error())
		return
	}

	jsonOK(w, map[string]bool{"ok": true})
}

func roundf(f float64) float64 {
	return float64(int(f*10)) / 10
}
