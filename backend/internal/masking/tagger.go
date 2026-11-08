package masking

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const (
	taggerMinRunes = 4
	// taggerMinSpanRunes 是采信模型片段的最小长度：中文人名/地名 2-3 个字很常见，
	// 规则层不需要这条，只对标记模型放开。
	taggerMinSpanRunes = 2
	taggerMaxRunes     = 2000
	taggerCacheLimit   = 2048
	taggerCacheTTL     = 10 * time.Minute
)

// taggerPrompt 照 magpie 的本机小模型思路：只做"标记"，不改写、不解释。
const taggerPrompt = `你是数据脱敏标注器，只输出 JSON，不要输出任何解释。
在给定文本中找出需要脱敏的个人信息，返回 {"spans":[{"kind":"KIND","text":"原文片段"}]}。
KIND 只能是 PERSON（人名）、ADDRESS（住址/完整门牌）、ACCOUNT（账号、工号、订单号、证件号）。
text 必须是输入文本里逐字出现的片段，不得改写、翻译或补全；没有命中时返回 {"spans":[]}。`

type span struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type taggerEntry struct {
	spans []span
	at    time.Time
}

// taggerClient 是"标记模型"客户端。它不阻断、不重试：失败即返回空标记，
// 规则层已经命中的值不受影响（与 magpie 关掉本机模型后的行为一致）。
type taggerClient struct {
	mu    sync.Mutex
	cache map[string]taggerEntry
}

func newTaggerClient() *taggerClient {
	return &taggerClient{cache: make(map[string]taggerEntry)}
}

func (t *taggerClient) tag(ctx context.Context, cfg *Config, text string) []span {
	if !cfg.TaggingEnabled() || utf8.RuneCountInString(text) < taggerMinRunes {
		return nil
	}
	key := cacheKey(cfg.Tagger.Model, text)
	if spans, ok := t.lookup(key); ok {
		return spans
	}
	spans, err := t.call(ctx, cfg, text)
	if err != nil {
		logger.FromContext(ctx).Warn("masking.tagger.failed", zap.Error(err))
		t.store(key, nil)
		return nil
	}
	spans = validatedSpans(text, spans)
	t.store(key, spans)
	return spans
}

func (t *taggerClient) lookup(key string) ([]span, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.cache[key]
	if !ok {
		return nil, false
	}
	if time.Since(entry.at) > taggerCacheTTL {
		delete(t.cache, key)
		return nil, false
	}
	return entry.spans, true
}

func (t *taggerClient) store(key string, spans []span) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.cache) >= taggerCacheLimit {
		now := time.Now()
		for k, entry := range t.cache {
			if now.Sub(entry.at) > taggerCacheTTL {
				delete(t.cache, k)
			}
		}
		if len(t.cache) >= taggerCacheLimit {
			for k := range t.cache {
				delete(t.cache, k)
				break
			}
		}
	}
	t.cache[key] = taggerEntry{spans: spans, at: time.Now()}
}

func (t *taggerClient) call(ctx context.Context, cfg *Config, text string) ([]span, error) {
	url := strings.TrimRight(strings.TrimSpace(cfg.Tagger.BaseURL), "/") + "/v1/chat/completions"
	payload := map[string]any{
		"model":       cfg.Tagger.Model,
		"temperature": 0,
		"max_tokens":  512,
		"messages": []map[string]string{
			{"role": "system", "content": taggerPrompt},
			{"role": "user", "content": text},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(cfg.Tagger.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = defaultTaggerTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(cfg.Tagger.APIKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	// ponytail: 标记模型是管理员自填的内网端点，不套 securityaudit 的 TLS/拨号护栏；
	// 需要统一出站策略时再把 NewSecureHTTPClient 拿过来。
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errTaggerStatus(resp.StatusCode)
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if len(decoded.Choices) == 0 {
		return nil, errTaggerNoChoice
	}
	var out struct {
		Spans []span `json:"spans"`
	}
	if err := json.Unmarshal([]byte(trimCodeFence(decoded.Choices[0].Message.Content)), &out); err != nil {
		return nil, err
	}
	return out.Spans, nil
}

type taggerError string

func (e taggerError) Error() string { return string(e) }

func errTaggerStatus(status int) error {
	return taggerError("tagger status " + http.StatusText(status))
}

var errTaggerNoChoice = taggerError("tagger returned no choice")

// validatedSpans 丢弃模型幻觉：kind 不在白名单、片段不在原文、片段已是占位符，
// 一律不采信。
func validatedSpans(text string, spans []span) []span {
	allowed := make(map[string]struct{}, len(modelKinds))
	for _, kind := range modelKinds {
		allowed[string(kind)] = struct{}{}
	}
	out := make([]span, 0, len(spans))
	seen := make(map[string]struct{}, len(spans))
	for _, item := range spans {
		kind := strings.ToUpper(strings.TrimSpace(item.Kind))
		fragment := item.Text
		if _, ok := allowed[kind]; !ok {
			continue
		}
		if utf8.RuneCountInString(fragment) < taggerMinSpanRunes || IsPlaceholder(fragment) {
			continue
		}
		if !strings.Contains(text, fragment) {
			continue
		}
		if kind == string(KindPerson) && looksLikeTemporalOrNumeric(fragment) {
			// 模型会把"明天下午三点"这类时间短语标成 PERSON；脱掉会丢上下文。
			continue
		}
		key := kind + "\x00" + fragment
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, span{Kind: kind, Text: fragment})
	}
	return out
}

// temporalWords 是中文时间短语里最常见的词根；命中即认为不是人名。
var temporalWords = []string{"今天", "明天", "后天", "昨天", "上午", "下午", "中午",
	"晚上", "下周", "上周", "本周", "点", "号", "月份", "年底"}

func looksLikeTemporalOrNumeric(fragment string) bool {
	for _, word := range temporalWords {
		if strings.Contains(fragment, word) {
			return true
		}
	}
	for _, r := range fragment {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func cacheKey(model, text string) string {
	sum := sha256.Sum256([]byte(model + "\x00" + text))
	return hex.EncodeToString(sum[:])
}

func trimCodeFence(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "```") {
		return value
	}
	value = strings.TrimPrefix(value, "```")
	value = strings.TrimPrefix(value, "json")
	if index := strings.LastIndex(value, "```"); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(value)
}
