package api

import (
	"github.com/darkerline/agent/internal/config"
	"github.com/darkerline/agent/internal/xray"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFingerprintEndpointAuthAndNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"outbounds":[{"tag":"relay-a","protocol":"vless","streamSettings":{"security":"reality","realitySettings":{"fingerprint":"firefox"}}}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: &config.Config{AgentToken: "test-token"}, manager: xray.NewManager(path)}
	handler := s.auth(s.handleRelayFingerprints)
	for _, tt := range []struct {
		method, body, token string
		status              int
	}{
		{"POST", `{"fingerprints":{"relay-a":"firefox"}}`, "", http.StatusUnauthorized},
		{"GET", `{}`, "test-token", http.StatusMethodNotAllowed},
		{"POST", `{"fingerprints":{"relay-a":"invalid"}}`, "test-token", http.StatusBadRequest},
		{"POST", `{"fingerprints":{"relay-a":"firefox"}}`, "test-token", http.StatusOK},
	} {
		req := httptest.NewRequest(tt.method, "/relay/fingerprints", strings.NewReader(tt.body))
		req.Header.Set("X-Agent-Token", tt.token)
		response := httptest.NewRecorder()
		handler(response, req)
		if response.Code != tt.status {
			t.Fatalf("got %d expected %d", response.Code, tt.status)
		}
	}
}
