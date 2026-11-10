package group

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/soleapi/forgebot/internal/decision"
	"github.com/soleapi/forgebot/internal/operation"
)

// ExternalReview 是数据配置，不持有密钥或 HTTP 执行权限。
type ExternalReview struct {
	URL              string            `json:"url"`
	Method           string            `json:"method"`
	Headers          map[string]string `json:"headers,omitempty"`
	Body             string            `json:"body,omitempty"`
	ResultExpression string            `json:"result_expression"`
}

var reviewExpression = regexp.MustCompile(`^\.([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)(?:\s*==\s*(true|false))?$`)
var reviewSecretRef = regexp.MustCompile(`\{([A-Za-z0-9_./-]+)\}`)
var reviewHeaderName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

func (r ExternalReview) Validate() error {
	u, err := url.Parse(r.URL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(r.URL) > 4096 {
		return errors.New("external_review.url must be an absolute HTTPS URL without userinfo or fragment")
	}
	loopback := strings.EqualFold(u.Hostname(), "localhost")
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return errors.New("external_review.url requires HTTPS; HTTP is only allowed on loopback for testing")
	}
	switch r.Method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
	default:
		return errors.New("external_review.method must be a supported uppercase HTTP method")
	}
	if len(r.Headers) > 32 {
		return errors.New("external_review supports at most 32 headers")
	}
	for name, value := range r.Headers {
		if !reviewHeaderName.MatchString(name) || len(name) > 256 || len(value) > 8192 || strings.ContainsAny(value, "\r\n") {
			return errors.New("external_review header name or value is invalid")
		}
		if strings.EqualFold(name, "Host") || strings.EqualFold(name, "Content-Length") || strings.EqualFold(name, "Transfer-Encoding") {
			return errors.New("external_review cannot override transport headers")
		}
		if strings.ContainsAny(reviewSecretRef.ReplaceAllString(value, ""), "{}") {
			return errors.New("external_review header contains an invalid secret reference")
		}
	}
	if len(r.Body) > 65536 {
		return errors.New("external_review.body exceeds 64KiB")
	}
	if r.Body != "" {
		var body any
		if json.Unmarshal([]byte(r.Body), &body) != nil {
			return errors.New("external_review.body must be a JSON template")
		}
	}
	if len(r.ResultExpression) > 1024 || reviewExpression.FindStringSubmatch(strings.TrimSpace(r.ResultExpression)) == nil {
		return errors.New("external_review.result_expression requires an object path and optional == true/false")
	}
	return nil
}

// evaluateEventReview 按成本升序串行执行启用项：正则 → 外部 HTTP → 模型。
// AND 遇 false/unknown、OR 遇 true 时立即短路，避免后续请求成本；未启用项跳过。
func (s *Service) evaluateEventReview(ctx context.Context, p Policy, decider decision.Decider, event Event) (bool, error) {
	if p.ExternalReview == nil {
		return s.evaluateReview(ctx, p, decider, event.Comment)
	}
	and := p.Combine() == "and"
	if p.ReviewRegex != "" {
		pattern, err := regexp.Compile(p.ReviewRegex)
		if err != nil {
			return false, err
		}
		matches := pattern.MatchString(event.Comment)
		if matches != and {
			return matches, nil
		}
	}
	approved, err := s.externalReview(ctx, *p.ExternalReview, event)
	if err != nil {
		s.logger.WarnContext(ctx, "external join review unknown", "conversation_id", event.ConversationID, "failure", operation.Describe(err))
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		approved = false
	}
	if approved != and {
		return approved, nil
	}
	if !p.ModelEnabled() {
		return approved, nil
	}
	modelPolicy := p
	modelPolicy.ReviewRegex = ""
	model, modelErr := s.evaluateReview(ctx, modelPolicy, decider, event.Comment)
	if modelErr != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		s.logger.WarnContext(ctx, "external combined model review unknown", "conversation_id", event.ConversationID, "failure", operation.Describe(modelErr))
		return false, nil
	}
	return model, nil
}

func (s *Service) externalReview(ctx context.Context, config ExternalReview, event Event) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body, err := reviewBody(config.Body, event)
	if err != nil {
		return false, err
	}
	request, err := http.NewRequestWithContext(ctx, config.Method, config.URL, bytes.NewReader(body))
	if err != nil {
		return false, operation.New(operation.Invalid, "invalid external review request")
	}
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	resolved := map[string]string{}
	for name, template := range config.Headers {
		var resolveErr error
		value := reviewSecretRef.ReplaceAllStringFunc(template, func(token string) string {
			ref := strings.TrimSuffix(strings.TrimPrefix(token, "{"), "}")
			if value, exists := resolved[ref]; exists {
				return value
			}
			if s.secrets == nil {
				resolveErr = operation.New(operation.NotConfigured, "external review credential is not configured")
				return ""
			}
			value, err := s.secrets.Get(ctx, ref)
			if err != nil || value == "" || strings.ContainsAny(value, "\r\n") {
				resolveErr = operation.New(operation.NotConfigured, "external review credential is unavailable or invalid")
				return ""
			}
			resolved[ref] = value
			return value
		})
		if resolveErr != nil {
			return false, resolveErr
		}
		request.Header.Set(name, value)
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return false, operation.Wrap("group.external_review", operation.Unavailable, operation.EffectUnknown, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, operation.New(operation.Unavailable, "external review returned a non-success HTTP status")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil {
		return false, operation.Wrap("group.external_review", operation.Unavailable, operation.EffectUnknown, err)
	}
	if len(data) > 65536 {
		return false, operation.New(operation.Invalid, "external review response exceeds 64KiB")
	}
	approved, err := reviewResult(data, config.ResultExpression)
	if err != nil {
		s.logger.WarnContext(ctx, "external join review rejected invalid result", "conversation_id", event.ConversationID)
		return false, nil
	}
	return approved, nil
}

func reviewBody(template string, event Event) ([]byte, error) {
	if template == "" {
		return nil, nil
	}
	var body any
	decoder := json.NewDecoder(strings.NewReader(template))
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		return nil, err
	}
	replacer := strings.NewReplacer("{application}", event.Comment, "{user_id}", event.UserID, "{conversation_id}", event.ConversationID)
	var replace func(any) any
	replace = func(value any) any {
		switch typed := value.(type) {
		case string:
			return replacer.Replace(typed)
		case []any:
			for index := range typed {
				typed[index] = replace(typed[index])
			}
		case map[string]any:
			for key, child := range typed {
				typed[key] = replace(child)
			}
		}
		return value
	}
	return json.Marshal(replace(body))
}

func reviewResult(data []byte, expression string) (bool, error) {
	match := reviewExpression.FindStringSubmatch(strings.TrimSpace(expression))
	if match == nil {
		return false, errors.New("invalid review expression")
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return false, err
	}
	for _, key := range strings.Split(match[1], ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return false, errors.New("review path does not resolve to a boolean")
		}
		value = object[key]
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("review result is not a boolean")
	}
	if match[2] == "false" {
		result = !result
	}
	return result, nil
}
