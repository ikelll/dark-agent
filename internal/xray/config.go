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
	DomainStrategy string        `json:"domainStrategy,omitempty"`
	Balancers      []Balancer    `json:"balancers,omitempty"`
	Rules          []RoutingRule `json:"rules,omitempty"`
}

type Balancer struct {
	Tag      string   `json:"tag"`
	Selector []string `json:"selector"`
}

type RoutingRule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	Domain      []string `json:"domain,omitempty"`
	IP          []string `json:"ip,omitempty"`
	Protocol    []string `json:"protocol,omitempty"`
	OutboundTag string   `json:"outboundTag,omitempty"`
	BalancerTag string   `json:"balancerTag,omitempty"`
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
	Tag            string          `json:"tag"`
	Protocol       string          `json:"protocol"`
	Settings       json.RawMessage `json:"settings,omitempty"`
	StreamSettings json.RawMessage `json:"streamSettings,omitempty"`
}

// ── VLESS outbound structures (for relay) ─────────────────────────────────────

type VlessOutboundSettings struct {
	Vnext []VlessOutboundVnext `json:"vnext"`
}

type VlessOutboundVnext struct {
	Address string              `json:"address"`
	Port    int                 `json:"port"`
	Users   []VlessOutboundUser `json:"users"`
}

type VlessOutboundUser struct {
	ID         string `json:"id"`
	Encryption string `json:"encryption"`
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

// isSystemOutbound returns true for outbound tags managed by the system
// (api, direct, blocked) vs relay/custom outbounds managed by relay setup.
func isSystemOutbound(tag string) bool {
	return tag == "api" || tag == "direct" || tag == "blocked" || tag == ""
}

// ApplyRoutingRules replaces user-managed routing rules while preserving:
//   - The internal API route (api → api)
//   - Any relay outbound rules (relay-out-* etc.) added by relay setup
//
// User rules (direct/blocked) are inserted BEFORE relay catchall rules so
// that specific domain/IP rules take priority over the relay catchall.
func (m *Manager) ApplyRoutingRules(rules []RoutingRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.read()
	if err != nil {
		return err
	}

	if cfg.Routing == nil {
		cfg.Routing = &RoutingConfig{}
	}
	if cfg.Routing.DomainStrategy == "" {
		cfg.Routing.DomainStrategy = "IPIfNonMatch"
	}

	// Collect relay/custom rules already in the config (non-system outbounds or balancers).
	var relayRules []RoutingRule
	for _, existing := range cfg.Routing.Rules {
		if existing.BalancerTag != "" || !isSystemOutbound(existing.OutboundTag) {
			relayRules = append(relayRules, existing)
		}
	}

	// Build new rule list:
	// 1. api → api  (always first)
	// 2. user rules (direct/blocked) — specific domain/IP rules with high priority
	// 3. relay rules — catchall forwarding rules (must come after specific rules)
	next := []RoutingRule{{Type: "field", InboundTag: []string{"api"}, OutboundTag: "api"}}

	for _, rule := range rules {
		if rule.Type == "" {
			rule.Type = "field"
		}
		if rule.OutboundTag == "" || rule.OutboundTag == "api" {
			continue
		}
		if !isSystemOutbound(rule.OutboundTag) {
			continue // skip relay tags from user input — managed by relay setup
		}
		next = append(next, rule)
	}

	next = append(next, relayRules...)

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
			DomainStrategy: "IPIfNonMatch",
			Rules: []RoutingRule{
				{Type: "field", InboundTag: []string{"api"}, OutboundTag: "api"},
			},
		},
	}
}

// ── Relay helpers ──────────────────────────────────────────────────────────────

// AddRelayOutbound adds a VLESS outbound to a worker server and routes traffic
// from clientInboundTag through it. Call reload after this.
func (m *Manager) AddRelayOutbound(outboundTag, clientInboundTag, workerIP string, workerPort int, relayUUID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.read()
	if err != nil {
		return err
	}

	// Build VLESS outbound settings pointing to worker.
	obSettings := VlessOutboundSettings{
		Vnext: []VlessOutboundVnext{{
			Address: workerIP,
			Port:    workerPort,
			Users:   []VlessOutboundUser{{ID: relayUUID, Encryption: "none"}},
		}},
	}
	obSettingsRaw, _ := json.Marshal(obSettings)
	streamRaw, _ := json.Marshal(map[string]interface{}{"network": "tcp", "security": "none"})

	// Replace or append relay outbound.
	replaced := false
	for i, ob := range cfg.Outbounds {
		if ob.Tag == outboundTag {
			cfg.Outbounds[i].Settings = obSettingsRaw
			cfg.Outbounds[i].StreamSettings = streamRaw
			replaced = true
			break
		}
	}
	if !replaced {
		cfg.Outbounds = append(cfg.Outbounds, Outbound{
			Tag:            outboundTag,
			Protocol:       "vless",
			Settings:       obSettingsRaw,
			StreamSettings: streamRaw,
		})
	}

	// Update routing: route clientInboundTag through this outbound.
	if cfg.Routing == nil {
		cfg.Routing = &RoutingConfig{}
	}
	if cfg.Routing.DomainStrategy == "" {
		cfg.Routing.DomainStrategy = "IPIfNonMatch"
	}
	newRules := []RoutingRule{}
	for _, rule := range cfg.Routing.Rules {
		// Remove only the previously managed relay rule for this inbound.
		// User direct/blocked rules for the same inbound must stay before the
		// catchall relay rule so RU/private/etc. can exit from the entry server.
		isForThisInbound := false
		for _, tag := range rule.InboundTag {
			if tag == clientInboundTag {
				isForThisInbound = true
				break
			}
		}
		if !(isForThisInbound && (rule.BalancerTag != "" || !isSystemOutbound(rule.OutboundTag))) {
			newRules = append(newRules, rule)
		}
	}
	newRules = append(newRules, RoutingRule{
		Type:        "field",
		InboundTag:  []string{clientInboundTag},
		OutboundTag: outboundTag,
	})
	cfg.Routing.Rules = newRules

	return m.write(cfg)
}

// SetRelayBalancer routes clientInboundTag through an Xray balancer over the
// supplied relay outbound tags. Outbounds must already exist in the config.
func (m *Manager) SetRelayBalancer(balancerTag, clientInboundTag string, outboundTags []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.read()
	if err != nil {
		return err
	}
	if cfg.Routing == nil {
		cfg.Routing = &RoutingConfig{}
	}
	if cfg.Routing.DomainStrategy == "" {
		cfg.Routing.DomainStrategy = "IPIfNonMatch"
	}

	selectors := make([]string, 0, len(outboundTags))
	seen := map[string]bool{}
	for _, tag := range outboundTags {
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		selectors = append(selectors, tag)
	}
	if len(selectors) == 0 {
		return fmt.Errorf("balancer requires at least one outbound tag")
	}

	balancers := make([]Balancer, 0, len(cfg.Routing.Balancers)+1)
	for _, balancer := range cfg.Routing.Balancers {
		if balancer.Tag != balancerTag {
			balancers = append(balancers, balancer)
		}
	}
	balancers = append(balancers, Balancer{Tag: balancerTag, Selector: selectors})
	cfg.Routing.Balancers = balancers

	newRules := []RoutingRule{}
	for _, rule := range cfg.Routing.Rules {
		isForThisInbound := false
		for _, tag := range rule.InboundTag {
			if tag == clientInboundTag {
				isForThisInbound = true
				break
			}
		}
		if !(isForThisInbound && (rule.BalancerTag != "" || !isSystemOutbound(rule.OutboundTag))) {
			newRules = append(newRules, rule)
		}
	}
	newRules = append(newRules, RoutingRule{
		Type:        "field",
		InboundTag:  []string{clientInboundTag},
		BalancerTag: balancerTag,
	})
	cfg.Routing.Rules = newRules

	return m.write(cfg)
}

// RemoveRelayOutbound removes the relay outbound and restores direct routing
// for clientInboundTag. Call reload after this.
func (m *Manager) RemoveRelayOutbound(outboundTag, clientInboundTag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.read()
	if err != nil {
		return err
	}

	// Remove the relay outbound.
	filtered := make([]Outbound, 0, len(cfg.Outbounds))
	for _, ob := range cfg.Outbounds {
		if ob.Tag != outboundTag {
			filtered = append(filtered, ob)
		}
	}
	cfg.Outbounds = filtered

	// Remove routing rule for clientInboundTag (falls back to default direct).
	if cfg.Routing != nil {
		newRules := []RoutingRule{}
		for _, rule := range cfg.Routing.Rules {
			isForThisInbound := false
			for _, tag := range rule.InboundTag {
				if tag == clientInboundTag {
					isForThisInbound = true
					break
				}
			}
			if !(isForThisInbound && (rule.BalancerTag != "" || !isSystemOutbound(rule.OutboundTag))) {
				newRules = append(newRules, rule)
			}
		}
		cfg.Routing.Rules = newRules
	}

	return m.write(cfg)
}

// AddRelayInbound adds a plain VLESS inbound on port that accepts relay traffic
// from the entry server. Call reload after this.
func (m *Manager) AddRelayInbound(tag string, port int, relayUUID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.read()
	if err != nil {
		return err
	}

	// Remove existing inbound with same tag.
	filtered := make([]Inbound, 0, len(cfg.Inbounds))
	for _, inb := range cfg.Inbounds {
		if inb.Tag != tag {
			filtered = append(filtered, inb)
		}
	}

	settings := VlessInboundSettings{
		Clients:    []VlessClient{{ID: relayUUID, Email: "relay-" + tag, Enable: true}},
		Decryption: "none",
	}
	settingsRaw, _ := json.Marshal(settings)
	streamRaw, _ := json.Marshal(map[string]interface{}{"network": "tcp", "security": "none"})

	filtered = append(filtered, Inbound{
		Tag:            tag,
		Port:           port,
		Protocol:       "vless",
		Settings:       settingsRaw,
		StreamSettings: streamRaw,
	})
	cfg.Inbounds = filtered

	// Add routing rule: relay inbound → direct.
	if cfg.Routing == nil {
		cfg.Routing = &RoutingConfig{}
	}
	if cfg.Routing.DomainStrategy == "" {
		cfg.Routing.DomainStrategy = "IPIfNonMatch"
	}
	newRules := []RoutingRule{}
	for _, rule := range cfg.Routing.Rules {
		hasTag := false
		for _, t := range rule.InboundTag {
			if t == tag {
				hasTag = true
				break
			}
		}
		if !hasTag {
			newRules = append(newRules, rule)
		}
	}
	newRules = append(newRules, RoutingRule{
		Type:        "field",
		InboundTag:  []string{tag},
		OutboundTag: "direct",
	})
	cfg.Routing.Rules = newRules

	return m.write(cfg)
}

// RemoveRelayInbound removes the relay inbound and its routing rule.
func (m *Manager) RemoveRelayInbound(tag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.read()
	if err != nil {
		return err
	}

	filtered := make([]Inbound, 0, len(cfg.Inbounds))
	for _, inb := range cfg.Inbounds {
		if inb.Tag != tag {
			filtered = append(filtered, inb)
		}
	}
	cfg.Inbounds = filtered

	if cfg.Routing != nil {
		newRules := []RoutingRule{}
		for _, rule := range cfg.Routing.Rules {
			hasTag := false
			for _, t := range rule.InboundTag {
				if t == tag {
					hasTag = true
					break
				}
			}
			if !hasTag {
				newRules = append(newRules, rule)
			}
		}
		cfg.Routing.Rules = newRules
	}

	return m.write(cfg)
}
