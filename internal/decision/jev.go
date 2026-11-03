package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"

	"github.com/soleapi/forgebot/internal/operation"
)

// JevProvider 适配 TypeSafe System One 的 choice 原语。
// 核对来源：https://docs.typesafe.ai/api/system-one.md（2026-11-03）。
// 它不是聊天补全协议，也不假定供应商直接接受我们的 JSON Schema。
type JevProvider struct {
	Client        *http.Client
	Endpoint      string
	Model         string
	APIKey        string
	MinConfidence float64
}

func (p JevProvider) Decide(ctx context.Context, request ProviderRequest) (Answer, error) {
	if len(request.Media) > 0 {
		return nil, operation.New(operation.Unsupported, "Jev image assets are not enabled in this adapter; use vision LLM fallback")
	}
	if strings.TrimSpace(p.APIKey) == "" {
		return nil, operation.New(operation.NotConfigured, "decision DM credential is not configured")
	}
	base := p.Endpoint
	if base == "" {
		base = "https://api.typesafe.ai"
	}
	endpoint, err := url.Parse(strings.TrimRight(base, "/") + "/v1/systemone")
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return nil, operation.New(operation.Invalid, "invalid Jev endpoint")
	}
	if p.MinConfidence < 0 || p.MinConfidence > 1 {
		return nil, operation.New(operation.Invalid, "invalid Jev confidence threshold")
	}
	model := p.Model
	if model == "" {
		model = "jev-latest"
	}
	questions := map[string]any{}
	for name, q := range request.Task.Questions {
		questions[name] = map[string]any{"type": "choice", "instructions": q.Instructions, "criteria": q.Options}
	}
	payload, err := json.Marshal(map[string]any{"model": model, "state": request.State, "questions": questions})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, operation.Wrap("decision.jev", operation.Unavailable, operation.EffectUnknown, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, operation.Wrap("decision.jev", operation.HTTPCode(response.StatusCode), operation.EffectUnknown, fmt.Errorf("Jev HTTP status %d", response.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil {
		return nil, err
	}
	if len(data) > 65536 {
		return nil, operation.New(operation.Invalid, "Jev response exceeds limit")
	}
	var wire struct {
		Answers map[string]struct {
			Type          string             `json:"type"`
			Choice        string             `json:"choice"`
			Confidence    json.RawMessage    `json:"confidence"`
			Probabilities map[string]float64 `json:"probabilities"`
		} `json:"answers"`
	}
	if err = json.Unmarshal(data, &wire); err != nil {
		return nil, operation.Wrap("decision.jev.decode", operation.Invalid, operation.NotStarted, err)
	}
	if len(wire.Answers) != len(request.Task.Questions) {
		return nil, operation.New(operation.Invalid, "Jev answer fields do not match questions")
	}
	answer := Answer{}
	for name, q := range request.Task.Questions {
		a, exists := wire.Answers[name]
		var confidence *float64
		if len(a.Confidence) > 0 {
			if err = json.Unmarshal(a.Confidence, &confidence); err != nil {
				return nil, operation.New(operation.Invalid, "invalid Jev confidence")
			}
		}
		if !exists || a.Type != "choice" || len(a.Probabilities) != len(q.Options) || len(a.Confidence) == 0 || (confidence != nil && (*confidence < 0 || *confidence > 1)) {
			return nil, operation.New(operation.Invalid, "invalid Jev choice structure")
		}
		if _, ok := q.Options[a.Choice]; !ok {
			return nil, operation.New(operation.Invalid, "Jev returned an unknown choice")
		}
		sum := 0.0
		for option := range q.Options {
			probability, ok := a.Probabilities[option]
			if !ok || probability < 0 || probability > 1 {
				return nil, operation.New(operation.Invalid, "invalid Jev probability distribution")
			}
			sum += probability
		}
		if math.Abs(sum-1) > 0.02 {
			return nil, operation.New(operation.Invalid, "Jev probabilities do not sum to one")
		}
		for _, probability := range a.Probabilities {
			if probability > a.Probabilities[a.Choice]+0.000001 {
				return nil, operation.New(operation.Invalid, "Jev selected choice does not match probabilities")
			}
		}
		answer[name] = a.Choice
		if confidence == nil || *confidence < p.MinConfidence {
			answer[name] = Unknown
		}
	}
	return answer, request.Task.ValidateAnswer(answer)
}
