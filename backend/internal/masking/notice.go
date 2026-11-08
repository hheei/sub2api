package masking

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Protocol 是客户端使用的入站协议形态；决定 system prompt 落在哪个字段。
type Protocol string

const (
	ProtocolAnthropic Protocol = "anthropic"
	ProtocolChat      Protocol = "chat"
	ProtocolResponses Protocol = "responses"
	ProtocolGemini    Protocol = "gemini"
	ProtocolUnknown   Protocol = ""
	// ProtocolSkip 表示这条路徑不该注入说明（例如 cachedContents 管理接口，
	// 注入会污染被缓存的内容）。
	ProtocolSkip Protocol = "skip"
)

// DefaultNotice 是注入给模型的说明。必须足够短（每次请求都进 system，计费与
// 缓存都按前缀算），又必须明确"逐字照抄、别翻译别猜"。
const DefaultNotice = "Some values here are opaque placeholders such as {{EMAIL_1a2b3c4d}}; the prefix is a category name. Copy every placeholder verbatim, character for character, and never translate, split, reformat, quote-paraphrase, or guess the real value behind it."

// ProtocolForPath 只按入站路径判断协议：脱敏发生在任何协议改写之前，客户端写
// 什么格式就是什么格式。认不出来的一律不重构消息数组（安全兜底）。
func ProtocolForPath(path string) Protocol {
	path = strings.ToLower(path)
	switch {
	case strings.Contains(path, "/chat/completions"):
		return ProtocolChat
	case strings.Contains(path, "/responses"), strings.HasPrefix(path, "/backend-api/codex"):
		return ProtocolResponses
	case strings.Contains(path, "/cachedcontents"):
		return ProtocolSkip
	case strings.Contains(path, "/v1beta"), strings.Contains(path, "generatecontent"):
		return ProtocolGemini
	case strings.HasSuffix(path, "/messages"):
		return ProtocolAnthropic
	default:
		return ProtocolUnknown
	}
}

// InjectNotice 把"占位符请逐字照抄"的说明追加进 system prompt。
//
// 语义要求：开关打开后每个请求都插入，与本次是否发生脱敏无关，这样多轮会话的
// 系统提示词段保持稳定，不因"这一轮恰好有命中"而抖动。
func InjectNotice(body []byte, protocol Protocol, text string) ([]byte, bool) {
	text = strings.TrimSpace(text)
	if text == "" || len(body) == 0 || !json.Valid(body) {
		return body, false
	}
	if bytes.Contains(body, []byte(text)) {
		return body, false
	}
	switch protocol {
	case ProtocolSkip:
		return body, false
	case ProtocolAnthropic:
		return injectSystemField(body, "system", text, true)
	case ProtocolChat:
		return injectChatMessages(body, text)
	case ProtocolResponses:
		return injectFlatField(body, "instructions", text)
	case ProtocolGemini:
		return injectGemini(body, text)
	default:
		if out, ok := injectSystemField(body, "system", text, false); ok {
			return out, true
		}
		if out, ok := injectFlatField(body, "instructions", text); ok {
			return out, true
		}
		return injectGeminiExistingOnly(body, text)
	}
}

func injectSystemField(body []byte, field, text string, create bool) ([]byte, bool) {
	result := gjson.GetBytes(body, field)
	if !result.Exists() {
		if !create {
			return body, false
		}
		block := `[{"type":"text","text":` + string(quoteJSONString(text)) + `}]`
		out, err := sjson.SetRawBytes(body, field, []byte(block))
		if err != nil {
			return body, false
		}
		return out, true
	}
	switch {
	case result.Type == gjson.String:
		out, err := sjson.SetBytes(body, field, result.String()+"\n\n"+text)
		if err != nil {
			return body, false
		}
		return out, true
	case result.IsArray():
		item := `{"type":"text","text":` + string(quoteJSONString(text)) + `}`
		out, err := appendToRawArray(body, field, result.Raw, item)
		if err != nil {
			return body, false
		}
		return out, true
	default:
		return body, false
	}
}

func injectFlatField(body []byte, field, text string) ([]byte, bool) {
	result := gjson.GetBytes(body, field)
	if result.Exists() && result.Type == gjson.String {
		out, err := sjson.SetBytes(body, field, result.String()+"\n\n"+text)
		if err != nil {
			return body, false
		}
		return out, true
	}
	if result.Exists() {
		return body, false
	}
	if !gjson.GetBytes(body, "input").Exists() {
		return body, false
	}
	out, err := sjson.SetBytes(body, field, text)
	if err != nil {
		return body, false
	}
	return out, true
}

func injectGemini(body []byte, text string) ([]byte, bool) {
	for _, field := range []string{"systemInstruction", "system_instruction"} {
		if out, ok := injectGeminiInstruction(body, field, text, true); ok {
			return out, true
		}
	}
	raw := `{"parts":[{"text":` + string(quoteJSONString(text)) + `}]}`
	out, err := sjson.SetRawBytes(body, "systemInstruction", []byte(raw))
	if err != nil {
		return body, false
	}
	return out, true
}

func injectGeminiExistingOnly(body []byte, text string) ([]byte, bool) {
	for _, field := range []string{"systemInstruction", "system_instruction"} {
		if out, ok := injectGeminiInstruction(body, field, text, false); ok {
			return out, true
		}
	}
	return body, false
}

func injectGeminiInstruction(body []byte, field, text string, create bool) ([]byte, bool) {
	result := gjson.GetBytes(body, field)
	if !result.Exists() || !result.IsObject() {
		return body, false
	}
	parts := result.Get("parts")
	if !parts.IsArray() {
		if !create {
			return body, false
		}
		out, err := sjson.SetRawBytes(body, field+".parts", []byte(`[{"text":`+string(quoteJSONString(text))+`}]`))
		if err != nil {
			return body, false
		}
		return out, true
	}
	item := `{"text":` + string(quoteJSONString(text)) + `}`
	out, err := appendToRawArray(body, field+".parts", parts.Raw, item)
	if err != nil {
		return body, false
	}
	return out, true
}

// injectChatMessages 处理 OpenAI chat 的 messages：首条是 system/developer 就
// 追加，否则在最前面插一条 system。
func injectChatMessages(body []byte, text string) ([]byte, bool) {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body, false
	}
	first := messages.Get("0")
	role := strings.ToLower(strings.TrimSpace(first.Get("role").String()))
	if role == "system" || role == "developer" {
		content := first.Get("content")
		switch {
		case content.Type == gjson.String:
			out, err := sjson.SetBytes(body, "messages.0.content", content.String()+"\n\n"+text)
			if err != nil {
				return body, false
			}
			return out, true
		case content.IsArray():
			item := `{"type":"text","text":` + string(quoteJSONString(text)) + `}`
			out, err := appendToRawArray(body, "messages.0.content", content.Raw, item)
			if err != nil {
				return body, false
			}
			return out, true
		default:
			out, err := sjson.SetBytes(body, "messages.0.content", text)
			if err != nil {
				return body, false
			}
			return out, true
		}
	}
	item := `{"role":"system","content":` + string(quoteJSONString(text)) + `}`
	merged := "[" + item
	if rest := strings.TrimSpace(messages.Raw[1:]); rest != "]" {
		merged += "," + rest
	} else {
		merged += "]"
	}
	out, err := sjson.SetRawBytes(body, "messages", []byte(merged))
	if err != nil {
		return body, false
	}
	return out, true
}

// appendToRawArray 在 JSON 数组末尾追加一项：直接拼 raw，保持其余字节不变。
// 追加而不是插入，是为了让客户端自己的首个 system 块（Claude Code 的计费归因
// 块、缓存断点）留在原位。
func appendToRawArray(body []byte, path, rawArray, item string) ([]byte, error) {
	trimmed := strings.TrimSpace(rawArray)
	merged := "[" + item + "]"
	if len(trimmed) > 2 {
		merged = "[" + strings.TrimSpace(trimmed[1:len(trimmed)-1]) + "," + item + "]"
	}
	return sjson.SetRawBytes(body, path, []byte(merged))
}
