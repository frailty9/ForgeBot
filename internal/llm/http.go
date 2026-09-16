package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type apiError struct {
	StatusCode int
	Body       string
}

func (e apiError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("LLM API request failed with status %d", e.StatusCode)
	}
	return fmt.Sprintf("LLM API request failed with status %d: %s", e.StatusCode, e.Body)
}

func doJSON(ctx context.Context, client *http.Client, method, url string, headers map[string]string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for key, value := range headers {
		if strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	if req.Header.Get(headerContentType) == "" {
		req.Header.Set(headerContentType, contentTypeJSON)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return apiError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func streamJSON(ctx context.Context, client *http.Client, method, url string, headers map[string]string, payload any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		if strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	if req.Header.Get(headerContentType) == "" {
		req.Header.Set(headerContentType, contentTypeJSON)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, apiError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	return resp, nil
}

func mergeHeaders(base map[string]string, overlays ...map[string]string) map[string]string {
	out := make(map[string]string)
	for key, value := range base {
		out[key] = value
	}
	for _, overlay := range overlays {
		for key, value := range overlay {
			out[key] = value
		}
	}
	return out
}
