package decision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJevMapsChoiceContractAndLowConfidence(t *testing.T) {
	requests := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("invalid Jev transport")
		}
		var request struct {
			Model     string          `json:"model"`
			State     json.RawMessage `json:"state"`
			Questions map[string]struct {
				Type     string            `json:"type"`
				Criteria map[string]string `json:"criteria"`
			} `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "test-jev" || request.Questions["action"].Type != "choice" || len(request.Questions["action"].Criteria) != 3 {
			t.Error("internal task was not mapped to choices")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{"action": map[string]any{"type": "choice", "choice": "allow", "confidence": 0.4, "probabilities": map[string]float64{"allow": 0.7, "deny": 0.2, "unknown": 0.1}}}})
	}))
	defer api.Close()
	p := JevProvider{Endpoint: api.URL, Model: "test-jev", APIKey: "test-key", MinConfidence: 0.6, Client: api.Client()}
	answer, err := p.Decide(context.Background(), ProviderRequest{Task: sampleTask(), State: sampleRequest().State})
	if err != nil || answer["action"] != Unknown || requests != 1 {
		t.Fatalf("answer=%v requests=%d err=%v", answer, requests, err)
	}
}

func TestJevRejectsMissingChoiceAndInvalidDistribution(t *testing.T) {
	for _, response := range []string{`{"answers":{}}`, `{"answers":{"action":{"type":"choice","choice":"allow","confidence":1,"probabilities":{"allow":0,"deny":0,"unknown":0}}}}`, `{"answers":{"action":{"type":"choice","choice":"allow","probabilities":{"allow":1,"deny":0,"unknown":0}}}}`} {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(response)) }))
		p := JevProvider{Endpoint: api.URL, APIKey: "test-key", Client: api.Client()}
		if _, err := p.Decide(context.Background(), ProviderRequest{Task: sampleTask(), State: sampleRequest().State}); err == nil {
			t.Fatal("invalid DM response accepted", response)
		}
		api.Close()
	}
}
