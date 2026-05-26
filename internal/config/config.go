package config

import (
	"encoding/json"
	"os"
)

type Config struct {
	// Agent
	ListenAddr  string `json:"listen_addr"`  // :7070
	AgentToken  string `json:"agent_token"`  // secret shared with backend
	TLSCert     string `json:"tls_cert"`     // optional path to cert.pem
	TLSKey      string `json:"tls_key"`      // optional path to key.pem

	// Xray
	XrayBin     string `json:"xray_bin"`     // /usr/local/bin/xray
	XrayConfig  string `json:"xray_config"`  // /etc/xray/config.json
	XrayAPIAddr string `json:"xray_api_addr"` // 127.0.0.1:10085 (gRPC API)

	// Server identity
	ServerName  string `json:"server_name"`
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		ListenAddr:  ":7070",
		XrayBin:     "/usr/local/bin/xray",
		XrayConfig:  "/etc/xray/config.json",
		XrayAPIAddr: "127.0.0.1:10085",
	}

	f, err := os.Open(path)
	if err != nil {
		// If file doesn't exist — use env/defaults
		if os.IsNotExist(err) {
			applyEnv(cfg)
			return cfg, nil
		}
		return nil, err
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(cfg); err != nil {
		return nil, err
	}
	applyEnv(cfg)
	return cfg, nil
}

func applyEnv(c *Config) {
	if v := os.Getenv("AGENT_TOKEN");     v != "" { c.AgentToken = v }
	if v := os.Getenv("AGENT_ADDR");      v != "" { c.ListenAddr = v }
	if v := os.Getenv("XRAY_BIN");        v != "" { c.XrayBin = v }
	if v := os.Getenv("XRAY_CONFIG");     v != "" { c.XrayConfig = v }
	if v := os.Getenv("XRAY_API_ADDR");   v != "" { c.XrayAPIAddr = v }
	if v := os.Getenv("SERVER_NAME");     v != "" { c.ServerName = v }
}
