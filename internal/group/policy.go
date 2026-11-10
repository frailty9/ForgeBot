package group

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

func (p Policy) ModelEnabled() bool {
	if p.ReviewModelEnabled != nil {
		return *p.ReviewModelEnabled
	}
	return strings.TrimSpace(p.ReviewRule) != ""
}

func (p Policy) Combine() string {
	if p.ReviewCombine == "" {
		return "and"
	}
	return p.ReviewCombine
}

// Equal 比较有效策略内容，不依赖反序列化后的布尔指针地址。
func (p Policy) Equal(other Policy) bool {
	if p.ModelEnabled() != other.ModelEnabled() || p.Combine() != other.Combine() {
		return false
	}
	if !reflect.DeepEqual(p.ExternalReview, other.ExternalReview) {
		return false
	}
	p.ExternalReview, other.ExternalReview = nil, nil
	p.ReviewModelEnabled, other.ReviewModelEnabled = nil, nil
	p.ReviewCombine, other.ReviewCombine = "", ""
	return p == other
}

func (p Policy) Validate() error {
	if len(p.ReviewRule) > 4096 || len(p.ReviewRegex) > 4096 || len(p.RejectReason) > 2048 {
		return fmt.Errorf("group review rule/regex must be at most 4096 bytes and reject_reason at most 2048 bytes")
	}
	if p.Combine() != "and" && p.Combine() != "or" {
		return fmt.Errorf("group review_combine must be and or or")
	}
	if p.ReviewRegex != "" {
		if _, err := regexp.Compile(p.ReviewRegex); err != nil {
			return fmt.Errorf("invalid group review_regex: %w", err)
		}
	}
	if p.ModelEnabled() && strings.TrimSpace(p.ReviewRule) == "" {
		return fmt.Errorf("group model review requires a nonblank review_rule")
	}
	if p.ExternalReview != nil {
		if err := p.ExternalReview.Validate(); err != nil {
			return err
		}
	}
	if p.ReviewRegex == "" && !p.ModelEnabled() && p.ExternalReview == nil {
		return fmt.Errorf("automatic group review requires regex, model or external review")
	}
	return nil
}
