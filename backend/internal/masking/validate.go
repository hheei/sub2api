package masking

import (
	"fmt"
	"net/url"
	"strings"
)

// AllKinds 列出规则层与模型层可命中的分类，供管理台渲染。
func AllKinds() []Kind {
	out := make([]Kind, 0, len(ruleKinds)+len(modelKinds))
	out = append(out, ruleKinds...)
	out = append(out, modelKinds...)
	return out
}

// Validate 校验管理台提交的配置。任何一项不合法都拒绝保存，避免"半开的隐私开关"。
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("配置为空")
	}
	if c.Mode != ModeRules && c.Mode != ModeRulesAndModel {
		return fmt.Errorf("mode 只能是 %s 或 %s", ModeRules, ModeRulesAndModel)
	}
	if c.MaxBodyBytes < 0 || c.MaxBodyBytes > 64<<20 {
		return fmt.Errorf("max_body_bytes 超出范围（0-67108864）")
	}
	known := make(map[string]struct{}, len(AllKinds()))
	for _, kind := range AllKinds() {
		known[string(kind)] = struct{}{}
	}
	for kind, action := range c.Actions {
		if _, ok := known[strings.ToUpper(strings.TrimSpace(kind))]; !ok {
			return fmt.Errorf("未知的 kind：%s", kind)
		}
		switch action {
		case ActionMask, ActionWarn, ActionOff:
		default:
			return fmt.Errorf("kind %s 的动作只能是 mask / warn / off", kind)
		}
	}
	if c.Mode == ModeRulesAndModel {
		base := strings.TrimSpace(c.Tagger.BaseURL)
		if base == "" {
			return fmt.Errorf("rules+tagger 模式必须填写标记模型地址")
		}
		parsed, err := url.Parse(base)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("标记模型地址必须是 http(s) URL")
		}
		if strings.TrimSpace(c.Tagger.Model) == "" {
			return fmt.Errorf("rules+tagger 模式必须填写模型名")
		}
	}
	if c.Tagger.TimeoutMS != 0 && (c.Tagger.TimeoutMS < 100 || c.Tagger.TimeoutMS > 10000) {
		return fmt.Errorf("tagger.timeout_ms 范围 100-10000")
	}
	if c.Tagger.MaxLeaves != 0 && (c.Tagger.MaxLeaves < 1 || c.Tagger.MaxLeaves > 32) {
		return fmt.Errorf("tagger.max_leaves 范围 1-32")
	}
	return nil
}

// Preview 只跑规则层：管理台试跑不该依赖外部模型是否在线。
func Preview(cfg *Config, text string) (string, int, []string) {
	if cfg == nil {
		return text, 0, nil
	}
	registry := newRegistry()
	masked, hits := maskByRules(registry, cfg, text)
	kinds := make([]string, 0, len(registry.order))
	for _, token := range registry.order {
		if index := strings.IndexByte(token[2:], '_'); index > 0 {
			kinds = append(kinds, token[2:2+index])
		}
	}
	return masked, hits, kinds
}
