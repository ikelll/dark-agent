package api

import "net/http"

func (s *Server) handleRelayFingerprints(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, 405, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	var req struct {
		Fingerprints map[string]string `json:"fingerprints"`
	}
	if err := decodeBody(r, &req); err != nil {
		jsonErr(w, 400, "invalid body")
		return
	}
	changed, err := s.manager.UpdateRelayFingerprints(req.Fingerprints)
	if err != nil {
		jsonErr(w, 400, "fingerprint batch rejected: "+err.Error())
		return
	}
	if changed {
		if err := s.applyConfig(); err != nil {
			jsonErr(w, http.StatusBadGateway, "fingerprint application failed; check agent health")
			return
		}
	}
	jsonOK(w, map[string]interface{}{"ok": true, "changed": changed, "outbounds": len(req.Fingerprints)})
}
