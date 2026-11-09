package xray

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func makeTestConfig(t *testing.T, clients []VlessClient) string {
	t.Helper()
	settings := VlessInboundSettings{Clients: clients, Decryption: "none"}
	settingsRaw, _ := json.Marshal(settings)

	stream := RealityStreamSettings{
		Network:  "tcp",
		Security: "reality",
		RealitySettings: RealitySettings{
			Dest:        "www.nvidia.com:443",
			ServerNames: []string{"www.nvidia.com"},
			PrivateKey:  "test-pk",
			ShortIds:    []string{},
		},
	}
	streamRaw, _ := json.Marshal(stream)

	cfg := Config{
		Inbounds: []Inbound{
			{
				Tag:            "test-reality",
				Port:           443,
				Protocol:       "vless",
				Settings:       settingsRaw,
				StreamSettings: streamRaw,
			},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readShortIds(t *testing.T, path string, tag string) []string {
	t.Helper()
	m := NewManager(path)
	cfg, err := m.Read()
	if err != nil {
		t.Fatal(err)
	}
	for _, inb := range cfg.Inbounds {
		if inb.Tag != tag {
			continue
		}
		var stream RealityStreamSettings
		if err := json.Unmarshal(inb.StreamSettings, &stream); err != nil {
			t.Fatal(err)
		}
		return stream.RealitySettings.ShortIds
	}
	t.Fatalf("inbound %q not found", tag)
	return nil
}

func TestAddClient_syncsShortId(t *testing.T) {
	path := makeTestConfig(t, nil)
	m := NewManager(path)

	err := m.AddClient("test-reality", VlessClient{
		ID:      "aaaaaaaa-0000-0000-0000-000000000001",
		Email:   "user1@test",
		Flow:    "xtls-rprx-vision",
		Enable:  true,
		ShortId: "aabbccdd11223344",
	})
	if err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	ids := readShortIds(t, path, "test-reality")
	if len(ids) != 1 || ids[0] != "aabbccdd11223344" {
		t.Errorf("expected shortIds [aabbccdd11223344], got %v", ids)
	}
}

func TestAddClient_multipleClientsAllShortIds(t *testing.T) {
	path := makeTestConfig(t, nil)
	m := NewManager(path)

	clients := []VlessClient{
		{ID: "aaaaaaaa-0000-0000-0000-000000000001", Email: "u1@test", Enable: true, ShortId: "sid1111111111"},
		{ID: "aaaaaaaa-0000-0000-0000-000000000002", Email: "u2@test", Enable: true, ShortId: "sid2222222222"},
		{ID: "aaaaaaaa-0000-0000-0000-000000000003", Email: "u3@test", Enable: true, ShortId: "sid3333333333"},
	}
	for _, c := range clients {
		if err := m.AddClient("test-reality", c); err != nil {
			t.Fatalf("AddClient %s: %v", c.Email, err)
		}
	}

	ids := readShortIds(t, path, "test-reality")
	if len(ids) != 3 {
		t.Errorf("expected 3 shortIds, got %v", ids)
	}
	for _, c := range clients {
		if !slices.Contains(ids, c.ShortId) {
			t.Errorf("missing shortId %q in %v", c.ShortId, ids)
		}
	}
}

func TestRemoveClient_removesShortId(t *testing.T) {
	initial := []VlessClient{
		{ID: "aaaaaaaa-0000-0000-0000-000000000001", Email: "u1@test", Enable: true, ShortId: "sid1111111111"},
		{ID: "aaaaaaaa-0000-0000-0000-000000000002", Email: "u2@test", Enable: true, ShortId: "sid2222222222"},
	}
	path := makeTestConfig(t, initial)
	// Pre-sync shortIds by writing them fresh.
	m := NewManager(path)
	cfg, _ := m.Read()
	_ = m.syncRealityShortIds(cfg, "test-reality")
	_ = m.WriteConfig(cfg)

	err := m.RemoveClient("test-reality", "aaaaaaaa-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatalf("RemoveClient: %v", err)
	}

	ids := readShortIds(t, path, "test-reality")
	if slices.Contains(ids, "sid1111111111") {
		t.Errorf("removed client's shortId still present: %v", ids)
	}
	if !slices.Contains(ids, "sid2222222222") {
		t.Errorf("remaining client's shortId missing: %v", ids)
	}
}

func TestRemoveLastClientKeepsValidRealityConfig(t *testing.T) {
	const removedID = "aabbccdd11223344"
	path := makeTestConfig(t, []VlessClient{{ID: "last-client", Email: "last@test", Enable: true, ShortId: removedID}})
	m := NewManager(path)
	if err := m.RemoveClient("test-reality", "last-client"); err != nil {
		t.Fatal(err)
	}
	ids := readShortIds(t, path, "test-reality")
	if len(ids) != 1 || ids[0] == removedID || len(ids[0]) != 16 {
		t.Fatalf("expected an unassigned placeholder, got %v", ids)
	}
	if _, err := hex.DecodeString(ids[0]); err != nil {
		t.Fatal(err)
	}
	cfg, err := m.Read()
	if err != nil {
		t.Fatal(err)
	}
	var settings VlessInboundSettings
	if err := json.Unmarshal(cfg.Inbounds[0].Settings, &settings); err != nil {
		t.Fatal(err)
	}
	if len(settings.Clients) != 0 {
		t.Fatal("removed client remained configured")
	}
}

func TestRollback(t *testing.T) {
	path := makeTestConfig(t, nil)
	m := NewManager(path)

	original, _ := os.ReadFile(path)

	// Make a change (creates .bak of original)
	_ = m.AddClient("test-reality", VlessClient{
		ID:      "aaaaaaaa-0000-0000-0000-000000000001",
		Email:   "u1@test",
		Enable:  true,
		ShortId: "aabbccdd11223344",
	})

	// Rollback
	if err := m.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	restored, _ := os.ReadFile(path)
	if string(restored) != string(original) {
		t.Errorf("rollback did not restore original config")
	}
}

func TestSyncRealityShortIds_deduplicates(t *testing.T) {
	initial := []VlessClient{
		{ID: "id1", Email: "u1@test", Enable: true, ShortId: "sameid1234567890"},
		{ID: "id2", Email: "u2@test", Enable: true, ShortId: "sameid1234567890"},
		{ID: "id3", Email: "u3@test", Enable: true, ShortId: "uniqueid12345678"},
	}
	path := makeTestConfig(t, initial)
	m := NewManager(path)

	cfg, _ := m.Read()
	_ = m.syncRealityShortIds(cfg, "test-reality")
	_ = m.WriteConfig(cfg)

	ids := readShortIds(t, path, "test-reality")
	// Duplicate "sameid1234567890" should appear only once
	count := 0
	for _, id := range ids {
		if id == "sameid1234567890" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 occurrence of duplicate shortId, got %d in %v", count, ids)
	}
	if !slices.Contains(ids, "uniqueid12345678") {
		t.Errorf("unique shortId missing: %v", ids)
	}
}

func TestRelayRequiresEncryptionAndPreservesRoutingPriority(t *testing.T) {
	path := makeTestConfig(t, nil)
	m := NewManager(path)
	key := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	security := RelayRealityConfig{PrivateKey: key, PublicKey: key, ServerName: "www.nvidia.com", ShortID: "0123456789abcdef", Dest: "www.nvidia.com:443"}
	original, _ := os.ReadFile(path)
	if err := m.AddRelayOutbound("relay-out-test", "test-reality", "192.0.2.1", 10001, "test-uuid", RelayRealityConfig{}); err == nil {
		t.Fatal("accepted unencrypted relay")
	}
	unchanged, _ := os.ReadFile(path)
	if string(original) != string(unchanged) {
		t.Fatal("rejected relay changed config")
	}
	if err := m.AddRelayInbound("relay-in-test", 10001, "test-uuid", security); err != nil {
		t.Fatal(err)
	}
	if err := m.AddRelayOutbound("relay-out-test", "test-reality", "192.0.2.1", 10001, "test-uuid", security); err != nil {
		t.Fatal(err)
	}
	direct := RoutingRule{Type: "field", IP: []string{"geoip:ru"}, OutboundTag: "direct"}
	if err := m.ApplyRoutingRules([]RoutingRule{direct}); err != nil {
		t.Fatal(err)
	}
	// Reapplying topology must not move its catchall ahead of Russian exceptions.
	if err := m.AddRelayOutbound("relay-out-test", "test-reality", "192.0.2.1", 10001, "test-uuid", security); err != nil {
		t.Fatal(err)
	}
	cfg, err := m.Read()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Routing.Rules[len(cfg.Routing.Rules)-1].OutboundTag != "relay-out-test" {
		t.Fatal("relay catchall lost")
	}
	if cfg.Routing.DomainStrategy != "IPOnDemand" {
		t.Fatal("IP exceptions can be bypassed by the relay catchall")
	}
	if cfg.Routing.Rules[1].OutboundTag != "direct" {
		t.Fatal("specific direct rule moved behind relay")
	}
	var outbound map[string]interface{}
	if err := json.Unmarshal(cfg.Outbounds[0].StreamSettings, &outbound); err != nil {
		t.Fatal(err)
	}
	if outbound["security"] != "reality" {
		t.Fatal("relay outbound is not encrypted")
	}
	params := outbound["realitySettings"].(map[string]interface{})
	if _, exposed := params["privateKey"]; exposed {
		t.Fatal("private key leaked to entry")
	}
	var inbound RealityStreamSettings
	for _, inb := range cfg.Inbounds {
		if inb.Tag == "relay-in-test" {
			if err := json.Unmarshal(inb.StreamSettings, &inbound); err != nil {
				t.Fatal(err)
			}
		}
	}
	if inbound.Security != "reality" || !slices.Contains(inbound.RealitySettings.ShortIds, security.ShortID) {
		t.Fatal("relay inbound security mismatch")
	}
	for _, name := range []string{path, path + ".bak"} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("secret config %s permissions are %v", name, info.Mode().Perm())
		}
	}
}

func TestEnsureInboundCanBindBehindSNIProxy(t *testing.T) {
	path := makeTestConfig(t, nil)
	manager := NewManager(path)
	if err := manager.EnsureRealityInbound("proxied", 1443, "test-key", []string{"aabbccdd"}, []string{"example.com"}, "example.com:443", "xhttp", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	cfg, err := manager.Read()
	if err != nil {
		t.Fatal(err)
	}
	for _, inb := range cfg.Inbounds {
		if inb.Tag == "proxied" {
			if inb.Port != 1443 || inb.Listen != "127.0.0.1" {
				t.Fatal("incorrect local binding")
			}
			return
		}
	}
	t.Fatal("proxied inbound not found")
}
