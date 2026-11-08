package masking

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"time"
	"unicode/utf8"
)

// registrySecret 只用于派生占位符 id，不用于加密，也不落盘：
// 占位符与原文的映射活在单个请求内，进程重启后不需要还原历史。
var registrySecret = func() []byte {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		sum := sha256.Sum256([]byte(time.Now().String()))
		copy(secret, sum[:])
	}
	return secret
}()

// Registry 是单请求的占位符表：token -> 原文。
type Registry struct {
	byToken map[string]string
	byKey   map[string]string
	order   []string
}

func newRegistry() *Registry {
	return &Registry{byToken: map[string]string{}, byKey: map[string]string{}}
}

func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.order)
}

// Token 返回某个值的占位符。id 由 HMAC 派生，所以同一个值在同一进程内
// 始终得到同一个占位符——重试、切换账号、多轮会话都不会抖动。
func (r *Registry) Token(kind Kind, value string) string {
	key := string(kind) + "\x00" + value
	if token, ok := r.byKey[key]; ok {
		return token
	}
	for size := 8; size <= 32; size += 4 {
		token := "{{" + string(kind) + "_" + tokenDigest(key, size) + "}}"
		if existing, ok := r.byToken[token]; ok && existing != value {
			continue
		}
		r.byToken[token] = value
		r.byKey[key] = token
		r.order = append(r.order, token)
		return token
	}
	return ""
}

func tokenDigest(key string, size int) string {
	mac := hmac.New(sha256.New, registrySecret)
	mac.Write([]byte(key))
	sum := hex.EncodeToString(mac.Sum(nil))
	if size > len(sum) {
		size = len(sum)
	}
	return sum[:size]
}

func (r *Registry) entries() [][2]string {
	if r == nil {
		return nil
	}
	out := make([][2]string, 0, len(r.order))
	for _, token := range r.order {
		out = append(out, [2]string{token, r.byToken[token]})
	}
	return out
}

// Masker 编排规则层与标记模型层。
type Masker struct {
	source *ConfigSource
	tagger *taggerClient
}

func NewMasker(reader SettingsReader) *Masker {
	return &Masker{source: NewConfigSource(reader), tagger: newTaggerClient()}
}

func (m *Masker) Config(ctx context.Context) *Config {
	if m == nil {
		return nil
	}
	return m.source.Get(ctx)
}

// Mask 对 JSON 请求体做脱敏，返回新 body、占位符表与命中数。
// 非 JSON、空 body 原样返回（reg 为空表示无需还原）。
func (m *Masker) Mask(ctx context.Context, cfg *Config, body []byte) ([]byte, *Registry, int) {
	reg := newRegistry()
	if m == nil || cfg == nil || len(body) == 0 {
		return body, reg, 0
	}
	leaves, ok := scanJSONLeaves(body)
	if !ok || len(leaves) == 0 {
		return body, reg, 0
	}
	values := make([]string, len(leaves))
	hits := 0
	tagBudget := cfg.Tagger.MaxLeaves
	for index := range leaves {
		values[index] = leaves[index].value
		if leaves[index].opaque || leaves[index].value == "" {
			continue
		}
		masked, count := maskByRules(reg, cfg, values[index])
		if count > 0 {
			values[index] = masked
			hits += count
		}
		if !cfg.TaggingEnabled() || tagBudget <= 0 {
			continue
		}
		runes := utf8.RuneCountInString(values[index])
		if runes < taggerMinRunes || runes > taggerMaxRunes {
			continue
		}
		tagBudget--
		spans := m.tagger.tag(ctx, cfg, values[index])
		if len(spans) == 0 {
			continue
		}
		tagged, count := maskSpans(reg, cfg, values[index], spans)
		values[index] = tagged
		hits += count
	}
	out, changed := applyLeaves(body, leaves, values)
	if !changed {
		return body, reg, 0
	}
	return out, reg, hits
}

// maskSpans 把标记模型给出的片段换成占位符。
func maskSpans(reg *Registry, cfg *Config, text string, spans []span) (string, int) {
	hits := 0
	out := text
	for _, item := range spans {
		action := cfg.Action(Kind(item.Kind))
		if action == ActionOff || !containsSpan(out, item.Text) {
			continue
		}
		if cfg.Allowed(item.Text) {
			continue
		}
		if action == ActionWarn {
			hits++
			continue
		}
		token := reg.Token(Kind(item.Kind), item.Text)
		if token == "" {
			continue
		}
		next := replaceAll(out, item.Text, token)
		if next == out {
			continue
		}
		hits += countOccurrences(out, item.Text)
		out = next
	}
	return out, hits
}
