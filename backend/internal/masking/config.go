package masking

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// SettingKeyMaskingConfig 是脱敏配置在 settings 表中的键。
const SettingKeyMaskingConfig = "masking_config"

const (
	ActionMask = "mask"
	ActionWarn = "warn"
	ActionOff  = "off"

	ModeRules         = "rules"
	ModeRulesAndModel = "rules+tagger"
)

// 默认值：总开关关闭（与仓库"新能力默认关闭"的约定一致）。
const (
	defaultMaxBodyBytes    = 2 << 20
	defaultTaggerTimeout   = 1500 * time.Millisecond
	defaultTaggerMaxLeaves = 8
	configCacheTTL         = 5 * time.Second
)

// Kind 是脱敏值分类，占位符里用大写 kind 前缀。
type Kind string

const (
	KindEmail      Kind = "EMAIL"
	KindPhone      Kind = "PHONE"
	KindIDCard     Kind = "ID_CARD"
	KindBankCard   Kind = "BANK_CARD"
	KindAPIKey     Kind = "API_KEY"
	KindPrivateKey Kind = "PRIVATE_KEY"
	KindPassword   Kind = "PASSWORD"
	KindPerson     Kind = "PERSON"
	KindAddress    Kind = "ADDRESS"
	KindAccount    Kind = "ACCOUNT"
)

// ruleKinds 是规则层能识别的分类；modelKinds 只能由小模型给出。
var (
	ruleKinds  = []Kind{KindEmail, KindPhone, KindIDCard, KindBankCard, KindAPIKey, KindPrivateKey, KindPassword}
	modelKinds = []Kind{KindPerson, KindAddress, KindAccount}
)

// TaggerConfig 描述标记用小模型的接入点（OpenAI 兼容 /v1/chat/completions）。
type TaggerConfig struct {
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	APIKey    string `json:"api_key"`
	TimeoutMS int    `json:"timeout_ms"`
	MaxLeaves int    `json:"max_leaves"`
}

// Config 是 masking_config 的解析结果。
type Config struct {
	Enabled      bool              `json:"enabled"`
	Mode         string            `json:"mode"`
	Actions      map[string]string `json:"actions"`
	Allow        []string          `json:"allow"`
	MaxBodyBytes int64             `json:"max_body_bytes"`
	Tagger       TaggerConfig      `json:"tagger"`
	Notice       *NoticeConfig     `json:"notice"`

	allow map[string]struct{}
}

// ParseConfig 解析 settings 中的 JSON 配置。配置损坏时返回全默认（关闭），
// 不做"部分生效"——半开的隐私开关比明确关闭更危险。
func ParseConfig(raw string) *Config {
	cfg := &Config{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), cfg); err != nil {
			cfg = &Config{}
		}
	}
	cfg.normalize()
	return cfg
}

func (c *Config) normalize() {
	if c.Mode != ModeRulesAndModel {
		c.Mode = ModeRules
	}
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = defaultMaxBodyBytes
	}
	if c.Tagger.TimeoutMS <= 0 {
		c.Tagger.TimeoutMS = int(defaultTaggerTimeout / time.Millisecond)
	}
	if c.Tagger.MaxLeaves <= 0 {
		c.Tagger.MaxLeaves = defaultTaggerMaxLeaves
	}
	c.allow = make(map[string]struct{}, len(c.Allow))
	for _, value := range c.Allow {
		if value = strings.TrimSpace(value); value != "" {
			c.allow[strings.ToLower(value)] = struct{}{}
		}
	}
	if c.Actions == nil {
		c.Actions = map[string]string{}
	}
}

// Action 返回某个 kind 的动作，未知 kind 默认 mask。
func (c *Config) Action(kind Kind) string {
	if action, ok := c.Actions[string(kind)]; ok {
		switch action {
		case ActionMask, ActionWarn, ActionOff:
			return action
		}
	}
	return ActionMask
}

// Allowed 是整值相等白名单（不放行子串，避免"路径里的用户名"这类误放行）。
func (c *Config) Allowed(value string) bool {
	if len(c.allow) == 0 {
		return false
	}
	_, ok := c.allow[strings.ToLower(strings.TrimSpace(value))]
	return ok
}

// TaggingEnabled 表示是否需要调用标记模型。
func (c *Config) TaggingEnabled() bool {
	return c.Enabled && c.Mode == ModeRulesAndModel &&
		strings.TrimSpace(c.Tagger.BaseURL) != "" && strings.TrimSpace(c.Tagger.Model) != ""
}

// SettingsReader 只需要 settings 的读取能力。
type SettingsReader interface {
	GetRawValue(ctx context.Context, key string) (string, error)
}

// NoticeConfig 控制"占位符请逐字照抄"这句说明的注入。缺省（notice 字段不存在）
// 视为开启：它是脱敏生效的前提，而不是可选项。
type NoticeConfig struct {
	Enabled *bool  `json:"enabled"`
	Text    string `json:"text"`
}

// NoticeText 返回要注入的说明；空串表示不注入。
func (c *Config) NoticeText() string {
	if c == nil || !c.Enabled {
		return ""
	}
	if c.Notice != nil {
		if c.Notice.Enabled != nil && !*c.Notice.Enabled {
			return ""
		}
		if text := strings.TrimSpace(c.Notice.Text); text != "" {
			return text
		}
	}
	return DefaultNotice
}

// ConfigSource 读取 settings 并做短 TTL 缓存，避免每请求打一次库。
type ConfigSource struct {
	reader SettingsReader
	mu     sync.Mutex
	cached *Config
	at     time.Time
}

func NewConfigSource(reader SettingsReader) *ConfigSource {
	return &ConfigSource{reader: reader}
}

// Get 返回当前配置；任何读取/解析失败都退化为"关闭"。
func (s *ConfigSource) Get(ctx context.Context) *Config {
	if s == nil || s.reader == nil {
		return &Config{Mode: ModeRules, MaxBodyBytes: defaultMaxBodyBytes}
	}
	s.mu.Lock()
	cached, at := s.cached, s.at
	s.mu.Unlock()
	if cached != nil && time.Since(at) < configCacheTTL {
		return cached
	}
	raw, err := s.reader.GetRawValue(ctx, SettingKeyMaskingConfig)
	cfg := &Config{}
	if err == nil {
		cfg = ParseConfig(raw)
	}
	cfg.normalize()
	s.mu.Lock()
	s.cached, s.at = cfg, time.Now()
	s.mu.Unlock()
	return cfg
}

func (s *ConfigSource) invalidate() {
	s.mu.Lock()
	s.cached, s.at = nil, time.Time{}
	s.mu.Unlock()
}
