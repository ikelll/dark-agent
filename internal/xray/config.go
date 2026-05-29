package xray

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// ── Xray config types ──────────────────────────────────────────────────────

type Config struct {
	Log       *LogConfig     `json:"log,omitempty"`
	API       *APIConfig     `json:"api,omitempty"`
	Inbounds  []Inbound      `json:"inbounds"`
	Outbounds []Outbound     `json:"outbounds"`
	Policy    *PolicyConfig  `json:"policy,omitempty"`
	Routing   *RoutingConfig `json:"routing,omitempty"`
}

type LogConfig struct {
	LogLevel string `json:"loglevel,omitempty"`
	Access   string `json:"access,omitempty"`
	Error    string `json:"error,omitempty"`
}

type APIConfig struct {
	Tag      string   `json:"tag"`
	Services []string `json:"services"`
}

type PolicyConfig struct {
	Levels map[string]LevelPolicy `json:"levels,omitempty"`
	System *SystemPolicy          `json:"system,omitempty"`
}

type LevelPolicy struct {
	StatsUserUplink   bool `json:"statsUserUplink,omitempty"`
	StatsUserDownlink bool `json:"statsUserDownlink,omitempty"`
}

type SystemPolicy struct {
	StatsInboundUplink    bool `json:"statsInboundUplink,omitempty"`
	StatsInboundDownlink  bool `json:"statsInboundDownlink,omitempty"`
	StatsOutboundUplink   bool `json:"statsOutboundUplink,omitempty"`
	StatsOutboundDownlink bool `json:"statsOutboundDownlink,omitempty"`
}

type RoutingConfig struct {
	Rules []RoutingRule `json:"rules,omitempty"`
}

type RoutingRule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	Domain      []string `json:"domain,omitempty"`
	IP          []string `json:"ip,omitempty"`
	Protocol    []string `json:"protocol,omitempty"`
	OutboundTag string   `json:"outboundTag"`
}

type Inbound struct {
	Tag            string          `json:"tag"`
	Listen         string          `json:"listen,omitempty"`
	Port           int             `json:"port"`
	Protocol       string          `json:"protocol"`
	Settings       json.RawMessage `json:"settings,omitempty"`
	StreamSettings json.RawMessage `json:"streamSettings,omitempty"`
	Sniffing       json.RawMessage `json:"sniffing,omitempty"`
}

type Outbound struct {
	Tag      string          `json:"tag"`
	Protocol string          `json:"protocol"`
	Settings json.RawMessage `json:"settings,omitempty"`
}

// ── VLESS inbound settings ─────────────────────────────────────────────────

type VlessInboundSettings struct {
	Clients    []VlessClient `json:"clients"`
	Decryption string        `json:"decryption"`
}

type VlessClient struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	Flow       string `json:"flow,omitempty"`
	TotalGB    int64  `json:"totalGB,omitempty"`
	ExpiryTime int64  `json:"expiryTime,omitempty"`
	Enable     bool   `json:"enable"`
	// ShortId is stored per-client for REALITY shortIds synchronization.
	// Xray ignores unknown fields, so this is safe to embed in the client JSON.
	ShortId string `json:"shortId,omitempty"`
}

// ── RealityStreamSettings ──────────────────────────────────────────────────

type RealityStreamSettings struct {
	Network         string          `json:"network"`
	Security        string          `json:"security"`
	RealitySettings RealitySettings `json:"realitySettings"`
	TCPSettings     *TCPSettings    `json:"tcpSettings,omitempty"`
	XhttpSettings   *XhttpSettings  `json:"xhttpSettings,omitempty"`
}

type RealitySettings struct {
	Show        bool     `json:"show"`
	Dest        string   `json:"dest"`
	ServerNames []string `json:"serverNames"`
	PrivateKey  string   `json:"privateKey"`
	ShortIds    []string `json:"shortIds"`
}

type TCPSettings struct {
	Header struct {
		Type string `json:"type"`
	} `json:"header"`
}

type XhttpSettings struct {
	Host string `json:"host,omitempty"`
	Path string `json:"path,omitempty"`
	Mode string `json:"mode,omitempty"`
}

// ── ConfigManager ──────────────────────────────────────────────────────────

type Manager struct {
	mu         sync.Mutex
	configPath string
}

func NewManager(configPath string) *Manager {
	return &Manager{configPath: configPath}
}

func (m *Manager) Read() (*Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.read()
}

// WriteConfig replaces the entire managed config and writes it to disk.
func (m *Manager) WriteConfig(cfg *Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.write(cfg)
}

func (m *Manager) read() (*Config, error) {
	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

func (m *Manager) write(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	// Backup existing config before overwriting.
	if _, statErr := os.Stat(m.configPath); statErr == nil {
		_ = os.WriteFile(m.configPath+".bak", func() []byte {
			b, _ := os.ReadFile(m.configPath)
			return b
		}(), 0644)
	}
	// Write atomically via temp file.
	tmp := m.configPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	return os.Rename(tmp, m.configPath)
}

// Rollback restores the last backup if present.
func (m *Manager) Rollback() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	bak := m.configPath + ".bak"
	if _, err := os.Stat(bak); err != nil {
		return fmt.Errorf("no backup found")
	}
	return os.Rename(bak, m.configPath)
}

// syncRealityShortIds rebuilds the shortIds list for a Reality inbound from
// active clients' ShortId fields and writes the config. Must be called with lock held.
func (m *Manager) syncRealityShortIds(cfg *Config, inboundTag string) error {
	for i, inb := range cfg.Inbounds {
		if inb.Protocol != "vless" {
			continue
		}
		if inboundTag != "" && inb.Tag != inboundTag {
			continue
		}
		var stream RealityStreamSettings
		if err := json.Unmarshal(inb.StreamSettings, &stream); err != nil {
			continue
		}
		if stream.Security != "reality" {
			continue
		}
		var settings VlessInboundSettings
		if err := json.Unmarshal(inb.Settings, &settings); err != nil {
			continue
		}
		ids := make([]string, 0, len(settings.Clients))
		seen := map[string]bool{}
		for _, c := range settings.Clients {
			if c.ShortId != "" && !seen[c.ShortId] {
				ids = append(ids, c.ShortId)
				seen[c.ShortId] = true
			}
		}
		stream.RealitySettings.ShortIds = ids
		raw, err := json.Marshal(stream)
		if err != nil {
			return fmt.Errorf("marshal stream: %w", err)
		}
		cfg.Inbounds[i].StreamSettings = raw
	}
	return nil
}

// UpdateRealityShortIds sets the shortIds list directly for a Reality inbound.
func (m *Manager) UpdateRealityShortIds(inboundTag string, shortIds []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.read()
	if err != nil {
		return err
	}

	found := false
	for i, inb := range cfg.Inbounds {
		if inboundTag != "" && inb.Tag != inboundTag {
			continue
		}
		var stream RealityStreamSettings
		if err := json.Unmarshal(inb.StreamSettings, &stream); err != nil {
			continue
		}
		if stream.Security != "reality" {
			continue
		}
		stream.RealitySettings.ShortIds = shortIds
		raw, _ := json.Marshal(stream)
		cfg.Inbounds[i].StreamSettings = raw
		found = true
	}

	if !found {
		return fmt.Errorf("reality inbound %q not found", inboundTag)
	}
	return m.write(cfg)
}

// ApplyRoutingRules replaces managed non-API routing rules and preserves the
// internal API route required for Xray HandlerService/StatsService.
func (m *Manager) ApplyRoutingRules(rules []RoutingRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.read()
	if err != nil {
		return err
	}

	apiRule := RoutingRule{Type: "field", InboundTag: []string{"api"}, OutboundTag: "api"}
	next := []RoutingRule{apiRule}
	for _, rule := range rules {
		if rule.Type == "" {
			rule.Type = "field"
		}
		if rule.OutboundTag == "" || rule.OutboundTag == "api" {
			continue
		}
		next = append(next, rule)
	}
	if cfg.Routing == nil {
		cfg.Routing = &RoutingConfig{}
	}
	cfg.Routing.Rules = next
	return m.write(cfg)
}

// AddClient adds a VLESS client to the inbound with given tag and synchronizes
// REALITY shortIds to include the client's ShortId.
// If inbound tag is empty, uses the first VLESS inbound found.
func (m *Manager) AddClient(inboundTag string, client VlessClient) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.read()
	if err != nil {
		return err
	}

	for i, inb := range cfg.Inbounds {
		if inb.Protocol != "vless" {
			continue
		}
		if inboundTag != "" && inb.Tag != inboundTag {
			continue
		}

		var settings VlessInboundSettings
		if err := json.Unmarshal(inb.Settings, &settings); err != nil {
			return fmt.Errorf("parse inbound settings: %w", err)
		}

		// Check duplicate
		for _, c := range settings.Clients {
			if c.ID == client.ID {
				return fmt.Errorf("client %s already exists in inbound %s", client.ID, inb.Tag)
			}
		}

		// If the inbound uses xhttp transport, strip flow from all clients
		// (including the new one). XTLS Vision flow is only for raw TCP.
		if inb.StreamSettings != nil {
			var ss RealityStreamSettings
			if json.Unmarshal(inb.StreamSettings, &ss) == nil && ss.Network == "xhttp" {
				client.Flow = ""
				for j := range settings.Clients {
					settings.Clients[j].Flow = ""
				}
			}
		}

		settings.Clients = append(settings.Clients, client)

		raw, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		cfg.Inbounds[i].Settings = raw

		// Sync REALITY shortIds from all clients' ShortId fields.
		if err := m.syncRealityShortIds(cfg, inb.Tag); err != nil {
			return fmt.Errorf("sync shortIds: %w", err)
		}

		return m.write(cfg)
	}

	return fmt.Errorf("inbound %q not found", inboundTag)
}

// RemoveClient removes a client by UUID from a specific inbound (or all if tag="")
// and synchronizes REALITY shortIds accordingly.
func (m *Manager) RemoveClient(inboundTag, clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.read()
	if err != nil {
		return err
	}

	removed := false
	affectedTags := []string{}
	for i, inb := range cfg.Inbounds {
		if inb.Protocol != "vless" {
			continue
		}
		if inboundTag != "" && inb.Tag != inboundTag {
			continue
		}

		var settings VlessInboundSettings
		if err := json.Unmarshal(inb.Settings, &settings); err != nil {
			continue
		}

		before := len(settings.Clients)
		filtered := make([]VlessClient, 0, before)
		for _, c := range settings.Clients {
			if c.ID != clientID {
				filtered = append(filtered, c)
			}
		}
		settings.Clients = filtered

		if len(settings.Clients) < before {
			removed = true
			affectedTags = append(affectedTags, inb.Tag)
			raw, _ := json.Marshal(settings)
			cfg.Inbounds[i].Settings = raw
		}
	}

	if !removed {
		return fmt.Errorf("client %s not found", clientID)
	}

	// Sync REALITY shortIds for all affected inbounds.
	for _, tag := range affectedTags {
		if err := m.syncRealityShortIds(cfg, tag); err != nil {
			return fmt.Errorf("sync shortIds: %w", err)
		}
	}

	return m.write(cfg)
}

// UpdateClient updates expiry/traffic for existing client.
func (m *Manager) UpdateClient(inboundTag, clientID string, expiryMs int64, totalGB int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.read()
	if err != nil {
		return err
	}

	updated := false
	for i, inb := range cfg.Inbounds {
		if inb.Protocol != "vless" {
			continue
		}
		if inboundTag != "" && inb.Tag != inboundTag {
			continue
		}
		var settings VlessInboundSettings
		if err := json.Unmarshal(inb.Settings, &settings); err != nil {
			continue
		}
		for j, c := range settings.Clients {
			if c.ID == clientID {
				settings.Clients[j].ExpiryTime = expiryMs
				settings.Clients[j].TotalGB = totalGB
				updated = true
			}
		}
		raw, _ := json.Marshal(settings)
		cfg.Inbounds[i].Settings = raw
	}

	if !updated {
		return fmt.Errorf("client %s not found", clientID)
	}
	return m.write(cfg)
}

// ListClients returns all clients from all VLESS inbounds.
func (m *Manager) ListClients() (map[string][]VlessClient, error) {
	cfg, err := m.Read()
	if err != nil {
		return nil, err
	}

	result := make(map[string][]VlessClient)
	for _, inb := range cfg.Inbounds {
		if inb.Protocol != "vless" {
			continue
		}
		var settings VlessInboundSettings
		if err := json.Unmarshal(inb.Settings, &settings); err != nil {
			continue
		}
		result[inb.Tag] = settings.Clients
	}
	return result, nil
}

// GetInbounds returns list of all inbounds with basic info.
func (m *Manager) GetInbounds() ([]map[string]interface{}, error) {
	cfg, err := m.Read()
	if err != nil {
		return nil, err
	}

	var result []map[string]interface{}
	for _, inb := range cfg.Inbounds {
		item := map[string]interface{}{
			"tag":      inb.Tag,
			"protocol": inb.Protocol,
			"port":     inb.Port,
		}
		if inb.Protocol == "vless" {
			var settings VlessInboundSettings
			if err := json.Unmarshal(inb.Settings, &settings); err == nil {
				item["clients_count"] = len(settings.Clients)
			}
		}
		result = append(result, item)
	}
	return result, nil
}

// EnsureRealityInbound creates a Reality TCP inbound if it doesn't exist.
func (m *Manager) EnsureRealityInbound(tag string, port int, privateKey string, shortIDs []string, serverNames []string, dest string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.read()
	if err != nil {
		return err
	}

	// Check if exists
	for _, inb := range cfg.Inbounds {
		if inb.Tag == tag {
			return nil // already exists
		}
	}

	settings := VlessInboundSettings{
		Clients:    []VlessClient{},
		Decryption: "none",
	}
	settingsRaw, _ := json.Marshal(settings)

	stream := RealityStreamSettings{
		Network:  "tcp",
		Security: "reality",
		RealitySettings: RealitySettings{
			Show:        false,
			Dest:        dest,
			ServerNames: serverNames,
			PrivateKey:  privateKey,
			ShortIds:    shortIDs,
		},
	}
	streamRaw, _ := json.Marshal(stream)

	sniffing, _ := json.Marshal(map[string]interface{}{
		"enabled":      true,
		"destOverride": []string{"http", "tls", "quic"},
	})

	inbound := Inbound{
		Tag:            tag,
		Port:           port,
		Protocol:       "vless",
		Settings:       settingsRaw,
		StreamSettings: streamRaw,
		Sniffing:       sniffing,
	}

	cfg.Inbounds = append(cfg.Inbounds, inbound)
	return m.write(cfg)
}

// GetRealityKeys reads Reality public/private keys from the first Reality inbound.
func (m *Manager) GetRealityKeys(tag string) (privateKey, publicKey string, shortIDs []string, err error) {
	cfg, err := m.Read()
	if err != nil {
		return
	}
	for _, inb := range cfg.Inbounds {
		if inb.Tag != tag {
			continue
		}
		var stream RealityStreamSettings
		if jsonErr := json.Unmarshal(inb.StreamSettings, &stream); jsonErr != nil {
			continue
		}
		if stream.Security == "reality" {
			return stream.RealitySettings.PrivateKey, "", stream.RealitySettings.ShortIds, nil
		}
	}
	return "", "", nil, fmt.Errorf("reality inbound %q not found", tag)
}

// ── Bootstrap config ───────────────────────────────────────────────────────

// DefaultConfig returns a minimal working Xray config with Reality inbound.
// transport should be "xhttp" (default) or "tcp".
func DefaultConfig(privateKey string, shortIDs []string, serverNames []string, dest string, port int, transport ...string) *Config {
	tr := "xhttp"
	if len(transport) > 0 && transport[0] == "tcp" {
		tr = "tcp"
	}

	settings := VlessInboundSettings{
		Clients:    []VlessClient{},
		Decryption: "none",
	}
	settingsRaw, _ := json.Marshal(settings)

	sni := ""
	if len(serverNames) > 0 {
		sni = serverNames[0]
	}

	stream := RealityStreamSettings{
		Network:  tr,
		Security: "reality",
		RealitySettings: RealitySettings{
			Show:        false,
			Dest:        dest,
			ServerNames: serverNames,
			PrivateKey:  privateKey,
			ShortIds:    shortIDs,
		},
	}
	if tr == "xhttp" {
		stream.XhttpSettings = &XhttpSettings{
			Host: sni,
			Path: "/",
			Mode: "auto",
		}
	}
	streamRaw, _ := json.Marshal(stream)

	sniffing, _ := json.Marshal(map[string]interface{}{
		"enabled":      true,
		"destOverride": []string{"http", "tls"},
	})

	apiSettings, _ := json.Marshal(map[string]interface{}{})
	directSettings, _ := json.Marshal(map[string]interface{}{
		"domainStrategy": "UseIPv4",
	})

	return &Config{
		Log: &LogConfig{LogLevel: "warning"},
		API: &APIConfig{
			Tag:      "api",
			Services: []string{"HandlerService", "StatsService"},
		},
		Policy: &PolicyConfig{
			Levels: map[string]LevelPolicy{
				"0": {StatsUserUplink: true, StatsUserDownlink: true},
			},
			System: &SystemPolicy{
				StatsInboundUplink:   true,
				StatsInboundDownlink: true,
			},
		},
		Inbounds: []Inbound{
			{
				Tag:      "api",
				Listen:   "127.0.0.1",
				Port:     10085,
				Protocol: "dokodemo-door",
				Settings: apiSettings,
			},
			{
				Tag:            "darkline-reality",
				Port:           port,
				Protocol:       "vless",
				Settings:       settingsRaw,
				StreamSettings: streamRaw,
				Sniffing:       sniffing,
			},
		},
		Outbounds: []Outbound{
			{Tag: "direct", Protocol: "freedom", Settings: directSettings},
			{Tag: "blocked", Protocol: "blackhole", Settings: directSettings},
		},
		Routing: &RoutingConfig{
			Rules: []RoutingRule{
				{Type: "field", InboundTag: []string{"api"}, OutboundTag: "api"},
			},
		},
	}
}
