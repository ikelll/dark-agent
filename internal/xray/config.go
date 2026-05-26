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
}

// ── RealityStreamSettings ──────────────────────────────────────────────────

type RealityStreamSettings struct {
	Network         string          `json:"network"`
	Security        string          `json:"security"`
	RealitySettings RealitySettings `json:"realitySettings"`
	TCPSettings     *TCPSettings    `json:"tcpSettings,omitempty"`
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
	// Write atomically via temp file
	tmp := m.configPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	return os.Rename(tmp, m.configPath)
}

// AddClient adds a VLESS client to the inbound with given tag.
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

		settings.Clients = append(settings.Clients, client)

		raw, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		cfg.Inbounds[i].Settings = raw
		return m.write(cfg)
	}

	return fmt.Errorf("inbound %q not found", inboundTag)
}

// RemoveClient removes a client by UUID from a specific inbound (or all if tag="").
func (m *Manager) RemoveClient(inboundTag, clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.read()
	if err != nil {
		return err
	}

	removed := false
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
		filtered := settings.Clients[:0]
		for _, c := range settings.Clients {
			if c.ID != clientID {
				filtered = append(filtered, c)
			}
		}
		settings.Clients = filtered

		if len(settings.Clients) < before {
			removed = true
			raw, _ := json.Marshal(settings)
			cfg.Inbounds[i].Settings = raw
		}
	}

	if !removed {
		return fmt.Errorf("client %s not found", clientID)
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

// DefaultConfig returns a minimal working Xray config with Reality TCP inbound.
func DefaultConfig(privateKey string, shortIDs []string, serverNames []string, dest string, port int) *Config {
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

	apiSettings, _ := json.Marshal(map[string]interface{}{})
	directSettings, _ := json.Marshal(map[string]interface{}{})

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
