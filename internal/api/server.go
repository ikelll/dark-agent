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

// xrayGRPC opens a short-lived gRPC connection to Xray API and returns the client.
// Caller is responsible for calling Close(). Returns nil if Xray is not reachable.
func (s *Server) xrayGRPC() (*xray.GRPCClient, error) {
	return xray.NewGRPCClient(s.cfg.XrayAPIAddr)
}

func (s *Server) Run() error {
	mux := http.NewServeMux()

	// Auth middleware applied to all routes
	mux.HandleFunc("/health", s.auth(s.handleHealth))
	mux.HandleFunc("/metrics", s.auth(s.handleMetrics))
	mux.HandleFunc("/version", s.auth(s.handleVersion))
	mux.HandleFunc("/xray/status", s.auth(s.handleXrayStatus))
	mux.HandleFunc("/xray/reload", s.auth(s.handleXrayReload))
	mux.HandleFunc("/xray/restart", s.auth(s.handleXrayRestart))
	mux.HandleFunc("/xray/inbounds", s.auth(s.handleInbounds))
	mux.HandleFunc("/clients", s.auth(s.handleClients))
	mux.HandleFunc("/clients/add", s.auth(s.handleAddClient))
	mux.HandleFunc("/clients/remove", s.auth(s.handleRemoveClient))
	mux.HandleFunc("/clients/update", s.auth(s.handleUpdateClient))
	mux.HandleFunc("/inbound/ensure", s.auth(s.handleEnsureInbound))
	mux.HandleFunc("/inbound/short-ids", s.auth(s.handleUpdateShortIds))
	mux.HandleFunc("/inbound/rollback", s.auth(s.handleRollback))
	mux.HandleFunc("/routing/apply", s.auth(s.handleApplyRouting))
	mux.HandleFunc("/provision", s.auth(s.handleProvision))

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
	ShortId    string `json:"short_id"` // unique per-client REALITY shortId
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

	resp := map[string]interface{}{
		"id":                 req.ID,
		"email":              req.Email,
		"runtime_sync_state": "failed",
		"config_apply_state": "pending",
	}

	// Phase 1: runtime AddUser via Xray gRPC HandlerService (no restart).
	grpcErr := ""
	if gc, err := s.xrayGRPC(); err != nil {
		grpcErr = "grpc connect: " + err.Error()
		log.Printf("xray gRPC unavailable for AddUser email=%s: %v", req.Email, err)
	} else {
		defer gc.Close()
		if err := gc.AddUser(req.InboundTag, req.ID, req.Email, req.Flow); err != nil {
			grpcErr = err.Error()
			log.Printf("HandlerService.AddUser failed email=%s: %v", req.Email, err)
		} else {
			resp["runtime_sync_state"] = "ok"
		}
	}
	if grpcErr != "" {
		resp["runtime_error"] = grpcErr
	}

	// Phase 2: persist to config.json and sync REALITY shortIds via reload.
	// This reload is required because Xray has no runtime API to update realitySettings.shortIds.
	// Note: reload (SIGHUP) may briefly interrupt active REALITY handshakes.
	client := xray.VlessClient{
		ID:         req.ID,
		Email:      req.Email,
		Flow:       req.Flow,
		ShortId:    req.ShortId,
		TotalGB:    req.TotalGB * 1024 * 1024 * 1024,
		ExpiryTime: req.ExpiryMs,
		Enable:     true,
	}
	if err := s.manager.AddClient(req.InboundTag, client); err != nil {
		resp["config_apply_state"] = "failed"
		resp["config_error"] = err.Error()
		log.Printf("config AddClient failed email=%s: %v", req.Email, err)
		jsonOK(w, resp)
		return
	}
	if err := s.process.Reload(); err != nil {
		resp["config_apply_state"] = "failed"
		resp["config_error"] = err.Error()
		log.Printf("xray reload after add client failed: %v", err)
		jsonOK(w, resp)
		return
	}
	resp["config_apply_state"] = "ok"

	jsonOK(w, resp)
}

// POST /clients/remove
type RemoveClientReq struct {
	InboundTag string `json:"inbound_tag"`
	ID         string `json:"id"`
	Email      string `json:"email"` // required for runtime RemoveUser by Xray API
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

	resp := map[string]interface{}{
		"runtime_sync_state": "failed",
		"config_apply_state": "pending",
	}

	// Phase 1: runtime RemoveUser via Xray gRPC HandlerService (no restart).
	grpcErr := ""
	if req.Email == "" {
		grpcErr = "email required for runtime RemoveUser"
		log.Printf("RemoveClient: email missing for runtime removal of id=%s", req.ID)
	} else if gc, err := s.xrayGRPC(); err != nil {
		grpcErr = "grpc connect: " + err.Error()
		log.Printf("xray gRPC unavailable for RemoveUser email=%s: %v", req.Email, err)
	} else {
		defer gc.Close()
		if err := gc.RemoveUser(req.InboundTag, req.Email); err != nil {
			grpcErr = err.Error()
			log.Printf("HandlerService.RemoveUser failed email=%s: %v", req.Email, err)
		} else {
			resp["runtime_sync_state"] = "revoked"
		}
	}
	if grpcErr != "" {
		resp["runtime_error"] = grpcErr
	}

	// Phase 2: remove from config.json and sync REALITY shortIds via reload.
	if err := s.manager.RemoveClient(req.InboundTag, req.ID); err != nil {
		resp["config_apply_state"] = "failed"
		resp["config_error"] = err.Error()
		log.Printf("config RemoveClient failed id=%s: %v", req.ID, err)
		jsonOK(w, resp)
		return
	}
	if err := s.process.Reload(); err != nil {
		resp["config_apply_state"] = "failed"
		resp["config_error"] = err.Error()
		log.Printf("xray reload after remove client failed: %v", err)
		jsonOK(w, resp)
		return
	}
	resp["config_apply_state"] = "revoked"

	jsonOK(w, resp)
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

// GET /version
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]string{
		"agent":  "1.0.0",
		"xray":   s.process.Version(s.cfg.XrayBin),
		"server": s.cfg.ServerName,
	})
}

// POST /inbound/short-ids — directly set REALITY shortIds for an inbound.
type UpdateShortIdsReq struct {
	InboundTag string   `json:"inbound_tag"`
	ShortIDs   []string `json:"short_ids"`
}

func (s *Server) handleUpdateShortIds(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, 405, "method not allowed")
		return
	}
	var req UpdateShortIdsReq
	if err := decodeBody(r, &req); err != nil {
		jsonErr(w, 400, "invalid body: "+err.Error())
		return
	}
	if err := s.manager.UpdateRealityShortIds(req.InboundTag, req.ShortIDs); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if err := s.process.Reload(); err != nil {
		jsonErr(w, http.StatusBadGateway, "shortIds saved, but xray apply failed: "+err.Error())
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

// POST /routing/apply — replace managed Xray routing rules.
type RoutingRuleReq struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag"`
	Domain      []string `json:"domain"`
	IP          []string `json:"ip"`
	Protocol    []string `json:"protocol"`
	OutboundTag string   `json:"outboundTag"`
}

type ApplyRoutingReq struct {
	Rules []RoutingRuleReq `json:"rules"`
}

func (s *Server) handleApplyRouting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, 405, "method not allowed")
		return
	}
	var req ApplyRoutingReq
	if err := decodeBody(r, &req); err != nil {
		jsonErr(w, 400, "invalid body: "+err.Error())
		return
	}
	rules := make([]xray.RoutingRule, 0, len(req.Rules))
	for _, r := range req.Rules {
		if r.OutboundTag != "direct" && r.OutboundTag != "blocked" {
			jsonErr(w, 400, "outboundTag must be direct or blocked")
			return
		}
		rules = append(rules, xray.RoutingRule{
			Type:        r.Type,
			InboundTag:  r.InboundTag,
			Domain:      r.Domain,
			IP:          r.IP,
			Protocol:    r.Protocol,
			OutboundTag: r.OutboundTag,
		})
	}
	if err := s.manager.ApplyRoutingRules(rules); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if err := s.process.Reload(); err != nil {
		jsonErr(w, http.StatusBadGateway, "routing saved, but xray reload failed: "+err.Error())
		return
	}
	jsonOK(w, map[string]interface{}{"ok": true, "rules": len(rules)})
}

// POST /inbound/rollback — restore the last backup config.
func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, 405, "method not allowed")
		return
	}
	if err := s.manager.Rollback(); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if err := s.process.Reload(); err != nil {
		jsonErr(w, http.StatusBadGateway, "rollback applied, but xray reload failed: "+err.Error())
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

// POST /provision — write initial Xray config and start Xray.
type ProvisionReq struct {
	Tag         string   `json:"tag"`
	Port        int      `json:"port"`
	PrivateKey  string   `json:"private_key"`
	ShortIDs    []string `json:"short_ids"`
	ServerNames []string `json:"server_names"`
	Dest        string   `json:"dest"`
	Transport   string   `json:"transport"` // "xhttp" (default) or "tcp"
	Force       bool     `json:"force"`     // overwrite even if active clients exist
}

func (s *Server) handleProvision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, 405, "method not allowed")
		return
	}
	var req ProvisionReq
	if err := decodeBody(r, &req); err != nil {
		jsonErr(w, 400, "invalid body: "+err.Error())
		return
	}
	if req.PrivateKey == "" || req.Port == 0 {
		jsonErr(w, 400, "private_key and port are required")
		return
	}

	// Protect against accidental overwrite of a live config with active clients.
	if !req.Force {
		if existing, err := s.manager.Read(); err == nil {
			for _, inbound := range existing.Inbounds {
				if inbound.Tag == "api" {
					continue
				}
				var settings xray.VlessInboundSettings
				if json.Unmarshal(inbound.Settings, &settings) == nil && len(settings.Clients) > 0 {
					jsonErr(w, 409, fmt.Sprintf(
						"inbound %q already has %d active client(s); send force=true to overwrite",
						inbound.Tag, len(settings.Clients),
					))
					return
				}
			}
		}
	}

	tag := req.Tag
	if tag == "" {
		tag = "darkline-reality"
	}
	dest := req.Dest
	if dest == "" {
		dest = "www.nvidia.com:443"
	}
	serverNames := req.ServerNames
	if len(serverNames) == 0 {
		serverNames = []string{"www.nvidia.com"}
	}
	transport := req.Transport
	if transport == "" {
		transport = "xhttp"
	}

	cfg := xray.DefaultConfig(req.PrivateKey, req.ShortIDs, serverNames, dest, req.Port, transport)
	if err := s.manager.WriteConfig(cfg); err != nil {
		jsonErr(w, 500, "write config: "+err.Error())
		return
	}

	if err := s.process.Restart(); err != nil {
		jsonErr(w, http.StatusBadGateway, "config written, but xray start failed: "+err.Error())
		return
	}

	jsonOK(w, map[string]interface{}{
		"ok":        true,
		"tag":       tag,
		"port":      req.Port,
		"transport": transport,
		"xray":      s.process.IsRunning(),
	})
}
