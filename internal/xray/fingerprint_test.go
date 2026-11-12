package xray

import (
	"encoding/json"
	"os"
	"testing"
)

func TestRelayFingerprintBatchPreservesUnknownConfig(t *testing.T) {
	path := makeTestConfig(t, nil)
	raw := map[string]interface{}{
		"dns":     map[string]interface{}{"servers": []string{"https://dns.example/dns-query"}},
		"metrics": map[string]interface{}{"listen": "127.0.0.1:11111"},
		"outbounds": []interface{}{
			map[string]interface{}{"tag": "relay-a", "protocol": "vless", "settings": map[string]interface{}{"identity": "preserve"}, "streamSettings": map[string]interface{}{"network": "tcp", "security": "reality", "sockopt": map[string]interface{}{"tcpKeepAliveIdle": 60}, "realitySettings": map[string]interface{}{"fingerprint": "chrome", "shortId": "unchanged"}}},
			map[string]interface{}{"tag": "direct", "protocol": "freedom"},
		},
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(path)
	changed, err := m.UpdateRelayFingerprints(map[string]string{"relay-a": "firefox"})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err = json.Unmarshal(after, &got); err != nil {
		t.Fatal(err)
	}
	// Undo the only expected mutation, then require exact semantic equivalence.
	got["outbounds"].([]interface{})[0].(map[string]interface{})["streamSettings"].(map[string]interface{})["realitySettings"].(map[string]interface{})["fingerprint"] = "chrome"
	restored, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(data) {
		t.Fatal("unrelated configuration changed")
	}
	changed, err = m.UpdateRelayFingerprints(map[string]string{"relay-a": "firefox"})
	if err != nil || changed {
		t.Fatal("unchanged request must not reload")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, batch := range []map[string]string{{"relay-a": "safari", "missing": "safari"}, {"direct": "firefox"}, {"relay-a": "invalid"}, {}} {
		if _, err = m.UpdateRelayFingerprints(batch); err == nil {
			t.Fatal("accepted invalid batch")
		}
		after, err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatal("rejected batch modified config")
		}
	}
}
