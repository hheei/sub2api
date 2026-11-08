package masking

import (
	"regexp"
	"strings"
)

type rule struct {
	kind  Kind
	expr  *regexp.Regexp
	group int // 需要替换的捕获组（0 = 整个匹配）
}

// 顺序有意义：先替掉长且特征强的值，避免银行卡/身份证规则把已脱敏文本再切一遍。
// 规则层只处理"确定是机密"的形态；人名/地址一类只能靠标记模型，交给 tagger。
var rules = []rule{
	{KindPrivateKey, regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`), 0},
	{KindAPIKey, regexp.MustCompile(`\b(?:sk|rk|pk)-[A-Za-z0-9_\-]{16,}|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{20,}|AIza[0-9A-Za-z_\-]{35}|xox[bap]-[A-Za-z0-9\-]{10,}`), 0},
	{KindEmail, regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`), 0},
	{KindIDCard, regexp.MustCompile(`\b[1-9]\d{5}(?:19|20)\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])\d{3}[\dXx]\b`), 0},
	{KindPhone, regexp.MustCompile(`(?:\+?86[ \-]?)?1[3-9]\d{9}\b|\+[1-9]\d{6,14}\b`), 0},
	{KindBankCard, regexp.MustCompile(`\b(?:\d{4}[ \-]?){3}\d{4,7}\b`), 0},
	{KindAPIKey, regexp.MustCompile(`(?i)\bbearer\s+([A-Za-z0-9\-._~+/]{16,}=*)`), 1},
	{KindAPIKey, regexp.MustCompile(`\beyJ[A-Za-z0-9\-_]{8,}\.[A-Za-z0-9\-_]{8,}\.[A-Za-z0-9\-_]{8,}\b`), 0},
	{KindPassword, regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.\-]*://[^\s:/@]+:([^\s/@]{1,})@`), 1},
	{KindPassword, regexp.MustCompile(`(?i)(?:password|passwd|pwd|secret|token|api[_\-]?key)\s*["']?\s*[:=]\s*["']?([^\s"',;{}]{4,})`), 1},
}

const placeholderPattern = `^\{\{[A-Z_]+_[0-9a-f]{6,}\}\}$`

var placeholderRe = regexp.MustCompile(placeholderPattern)

// IsPlaceholder 判断一段文本是否已经是占位符（用于幂等，防止二次脱敏）。
func IsPlaceholder(value string) bool {
	return placeholderRe.MatchString(strings.TrimSpace(value))
}

// maskByRules 在单个字符串内按规则替换，返回新文本与替换次数。
func maskByRules(reg *Registry, cfg *Config, text string) (string, int) {
	hits := 0
	for _, r := range rules {
		action := cfg.Action(r.kind)
		if action == ActionOff {
			continue
		}
		matches := r.expr.FindAllStringSubmatchIndex(text, -1)
		if len(matches) == 0 {
			continue
		}
		out := make([]byte, 0, len(text)+32)
		last := 0
		changed := false
		for _, match := range matches {
			group := r.group * 2
			if group+1 >= len(match) || match[group] < 0 || match[group] < last {
				continue
			}
			start, end := match[group], match[group+1]
			value := text[start:end]
			if IsPlaceholder(value) || strings.HasPrefix(value, "{{") || cfg.Allowed(value) {
				continue
			}
			if action == ActionWarn {
				hits++
				continue
			}
			token := reg.Token(r.kind, value)
			if token == "" {
				continue
			}
			out = append(out, text[last:start]...)
			out = append(out, token...)
			last = end
			changed = true
			hits++
		}
		if !changed {
			continue
		}
		out = append(out, text[last:]...)
		text = string(out)
	}
	return text, hits
}
