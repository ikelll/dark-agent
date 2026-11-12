package xray

import (
	"encoding/json"
	"fmt"
	"os"
)

func validFingerprint(value string) bool {
	switch value {
	case "firefox", "chrome", "safari", "ios", "android", "edge", "360", "qq", "random", "randomized":
		return true
	default:
		return false
	}
}

// UpdateRelayFingerprints changes only the supplied REALITY outbounds. Raw JSON
// retains DNS, metrics, transport options and other fields outside our typed model.
// It validates the whole batch before touching disk; an unchanged batch needs no reload.
func (m *Manager) UpdateRelayFingerprints(updates map[string]string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(updates) == 0 || len(updates) > 256 {
		return false, fmt.Errorf("expected 1 to 256 outbounds")
	}
	for tag, fp := range updates {
		if tag == "" || !validFingerprint(fp) {
			return false, fmt.Errorf("invalid outbound fingerprint")
		}
	}
	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return false, err
	}
	var cfg map[string]json.RawMessage
	if err = json.Unmarshal(data, &cfg); err != nil {
		return false, err
	}
	var outbounds []map[string]json.RawMessage
	if err = json.Unmarshal(cfg["outbounds"], &outbounds); err != nil {
		return false, err
	}
	found := map[string]bool{}
	changed := false
	for _, outbound := range outbounds {
		var tag, protocol string
		if err = json.Unmarshal(outbound["tag"], &tag); err != nil {
			return false, err
		}
		fp, wanted := updates[tag]
		if !wanted {
			continue
		}
		if found[tag] {
			return false, fmt.Errorf("duplicate outbound tag")
		}
		found[tag] = true
		if err = json.Unmarshal(outbound["protocol"], &protocol); err != nil {
			return false, err
		}
		var stream map[string]json.RawMessage
		if err = json.Unmarshal(outbound["streamSettings"], &stream); err != nil {
			return false, err
		}
		var security string
		if err = json.Unmarshal(stream["security"], &security); err != nil {
			return false, err
		}
		if protocol != "vless" || security != "reality" {
			return false, fmt.Errorf("target is not a VLESS REALITY outbound")
		}
		var reality map[string]json.RawMessage
		if err = json.Unmarshal(stream["realitySettings"], &reality); err != nil {
			return false, err
		}
		if reality == nil {
			return false, fmt.Errorf("missing REALITY settings")
		}
		var old string
		if value, ok := reality["fingerprint"]; ok {
			if err = json.Unmarshal(value, &old); err != nil {
				return false, err
			}
		}
		if old == fp {
			continue
		}
		changed = true
		reality["fingerprint"], err = json.Marshal(fp)
		if err != nil {
			return false, err
		}
		stream["realitySettings"], err = json.Marshal(reality)
		if err != nil {
			return false, err
		}
		outbound["streamSettings"], err = json.Marshal(stream)
		if err != nil {
			return false, err
		}
	}
	if len(found) != len(updates) {
		return false, fmt.Errorf("a requested outbound does not exist")
	}
	if !changed {
		return false, nil
	}
	cfg["outbounds"], err = json.Marshal(outbounds)
	if err != nil {
		return false, err
	}
	data, err = json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, err
	}
	return true, m.writeData(data)
}
