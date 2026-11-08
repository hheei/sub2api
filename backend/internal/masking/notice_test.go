package masking

import (
	"encoding/json"
	"testing"

	"github.com/tidwall/gjson"
)

func TestProtocolForPath(t *testing.T) {
	cases := map[string]Protocol{
		"/v1/messages":                                       ProtocolAnthropic,
		"/antigravity/v1/messages":                           ProtocolAnthropic,
		"/v1/chat/completions":                               ProtocolChat,
		"/v1/responses":                                      ProtocolResponses,
		"/backend-api/codex/responses":                       ProtocolResponses,
		"/v1beta/models/gemini-3:generateContent":            ProtocolGemini,
		"/antigravity/v1beta/models/x:streamGenerateContent": ProtocolGemini,
		"/v1/alpha/search":                                   ProtocolUnknown,
		"/v1beta/cachedContents":                             ProtocolSkip,
	}
	for path, want := range cases {
		if got := ProtocolForPath(path); got != want {
			t.Fatalf("%s => %q, want %q", path, got, want)
		}
	}
}

func TestInjectNoticeAnthropicShapes(t *testing.T) {
	withString := []byte(`{"model":"claude","system":"you are claude","messages":[]}`)
	out, ok := InjectNotice(withString, ProtocolAnthropic, DefaultNotice)
	if !ok || !json.Valid(out) {
		t.Fatalf("string system not injected: %s", out)
	}
	if got := gjsonString(t, out, "system"); got != "you are claude\n\n"+DefaultNotice {
		t.Fatalf("unexpected system: %q", got)
	}

	withBlocks := []byte(`{"model":"claude","system":[{"type":"text","text":"a"}],"messages":[]}`)
	out, ok = InjectNotice(withBlocks, ProtocolAnthropic, DefaultNotice)
	if !ok {
		t.Fatalf("block system not injected")
	}
	if len(gjsonArrayLen(t, out, "system")) != 2 {
		t.Fatalf("system blocks not appended: %s", out)
	}
	if got := gjsonString(t, out, "system.1.text"); got != DefaultNotice {
		t.Fatalf("appended block text wrong: %q", got)
	}

	missing := []byte(`{"model":"claude","messages":[{"role":"user","content":"hi"}]}`)
	out, ok = InjectNotice(missing, ProtocolAnthropic, DefaultNotice)
	if !ok || !json.Valid(out) {
		t.Fatalf("missing system not created: %s", out)
	}
	if got := gjsonString(t, out, "system.0.text"); got != DefaultNotice {
		t.Fatalf("created block wrong: %q", got)
	}
	if mode := gjsonString(t, out, "messages.0.role"); mode != "user" {
		t.Fatalf("messages must stay untouched: %s", out)
	}
}

func TestInjectNoticeChatMessages(t *testing.T) {
	withSystem := []byte(`{"model":"gpt","messages":[{"role":"system","content":"base"},{"role":"user","content":"hi"}]}`)
	out, ok := InjectNotice(withSystem, ProtocolChat, DefaultNotice)
	if !ok || gjsonString(t, out, "messages.0.content") != "base\n\n"+DefaultNotice {
		t.Fatalf("chat system append failed: %s", out)
	}

	arrayContent := []byte(`{"model":"gpt","messages":[{"role":"system","content":[{"type":"text","text":"base"}]}]}`)
	out, ok = InjectNotice(arrayContent, ProtocolChat, DefaultNotice)
	if !ok || len(gjsonArrayLen(t, out, "messages.0.content")) != 2 {
		t.Fatalf("chat array content append failed: %s", out)
	}

	noSystem := []byte(`{"model":"gpt","messages":[{"role":"user","content":"hi"}]}`)
	out, ok = InjectNotice(noSystem, ProtocolChat, DefaultNotice)
	if !ok || !json.Valid(out) {
		t.Fatalf("chat prepend failed: %s", out)
	}
	if role := gjsonString(t, out, "messages.0.role"); role != "system" {
		t.Fatalf("system not prepended: %s", out)
	}
	if role := gjsonString(t, out, "messages.1.role"); role != "user" {
		t.Fatalf("original message lost: %s", out)
	}
}

func TestInjectNoticeResponsesAndGemini(t *testing.T) {
	responses := []byte(`{"model":"gpt","instructions":"be nice","input":[]}`)
	out, ok := InjectNotice(responses, ProtocolResponses, DefaultNotice)
	if !ok || gjsonString(t, out, "instructions") != "be nice\n\n"+DefaultNotice {
		t.Fatalf("responses instructions append failed: %s", out)
	}

	noInstructions := []byte(`{"model":"gpt","input":[{"role":"user","content":"hi"}]}`)
	out, ok = InjectNotice(noInstructions, ProtocolResponses, DefaultNotice)
	if !ok || gjsonString(t, out, "instructions") != DefaultNotice {
		t.Fatalf("responses instructions create failed: %s", out)
	}

	gemini := []byte(`{"contents":[{"parts":[{"text":"hi"}]}],"systemInstruction":{"parts":[{"text":"be nice"}]}}`)
	out, ok = InjectNotice(gemini, ProtocolGemini, DefaultNotice)
	if !ok || len(gjsonArrayLen(t, out, "systemInstruction.parts")) != 2 {
		t.Fatalf("gemini parts append failed: %s", out)
	}

	geminiSnake := []byte(`{"contents":[],"system_instruction":{"parts":[{"text":"be nice"}]}}`)
	if out, ok = InjectNotice(geminiSnake, ProtocolGemini, DefaultNotice); !ok {
		t.Fatalf("snake_case systemInstruction not handled: %s", out)
	}

	geminiMissing := []byte(`{"contents":[{"parts":[{"text":"hi"}]}]}`)
	out, ok = InjectNotice(geminiMissing, ProtocolGemini, DefaultNotice)
	if !ok || gjsonString(t, out, "systemInstruction.parts.0.text") != DefaultNotice {
		t.Fatalf("gemini systemInstruction create failed: %s", out)
	}
}

func TestInjectNoticeIdempotentAndSafe(t *testing.T) {
	body := []byte(`{"model":"claude","system":"base","messages":[]}`)
	once, ok := InjectNotice(body, ProtocolAnthropic, DefaultNotice)
	if !ok {
		t.Fatalf("first injection failed")
	}
	if _, ok := InjectNotice(once, ProtocolAnthropic, DefaultNotice); ok {
		t.Fatalf("notice injected twice")
	}

	unknown := []byte(`{"model":"x","messages":[{"role":"user","content":"hi"}]}`)
	if out, ok := InjectNotice(unknown, ProtocolUnknown, DefaultNotice); ok {
		t.Fatalf("unknown protocol must not restructure messages: %s", out)
	}
	image := []byte(`{"prompt":"a cat","n":1}`)
	if _, ok := InjectNotice(image, ProtocolUnknown, DefaultNotice); ok {
		t.Fatalf("image body must stay untouched")
	}
	broken := []byte(`{"system":`)
	if _, ok := InjectNotice(broken, ProtocolAnthropic, DefaultNotice); ok {
		t.Fatalf("invalid json must stay untouched")
	}
}

func TestNoticeTextDefaults(t *testing.T) {
	enabled := ParseConfig(`{"enabled":true}`)
	if enabled.NoticeText() != DefaultNotice {
		t.Fatalf("notice must default on when masking is enabled")
	}
	off := ParseConfig(`{"enabled":true,"notice":{"enabled":false}}`)
	if off.NoticeText() != "" {
		t.Fatalf("notice must be disableable")
	}
	custom := ParseConfig(`{"enabled":true,"notice":{"text":" custom text "}}`)
	if custom.NoticeText() != "custom text" {
		t.Fatalf("custom text not trimmed: %q", custom.NoticeText())
	}
	disabled := ParseConfig(`{"enabled":false}`)
	if disabled.NoticeText() != "" {
		t.Fatalf("masking disabled must not inject notice")
	}
}

func gjsonString(t *testing.T, body []byte, path string) string {
	t.Helper()
	return gjson.GetBytes(body, path).String()
}

func gjsonArrayLen(t *testing.T, body []byte, path string) []gjson.Result {
	t.Helper()
	return gjson.GetBytes(body, path).Array()
}

func TestValidatedSpansDropsTemporalPERSON(t *testing.T) {
	text := "明天下午三点提醒我开会，客户张伟在建国路 88 号下单"
	spans := validatedSpans(text, []span{
		{Kind: "PERSON", Text: "明天下午三点"},
		{Kind: "PERSON", Text: "张伟"},
		{Kind: "ADDRESS", Text: "建国路 88 号"},
	})
	if len(spans) != 2 {
		t.Fatalf("unexpected spans: %+v", spans)
	}
	for _, item := range spans {
		if item.Text == "明天下午三点" {
			t.Fatalf("temporal phrase must not be masked: %+v", spans)
		}
	}
}

func TestTaggingDisabledWhenAllModelKindsOff(t *testing.T) {
	cfg := ParseConfig(`{"enabled":true,"mode":"rules+tagger","actions":{"PERSON":"off","ADDRESS":"off","ACCOUNT":"off"},"tagger":{"base_url":"http://127.0.0.1:1","model":"m"}}`)
	if cfg.TaggingEnabled() {
		t.Fatalf("tagger must not run when every model kind is off")
	}
	partial := ParseConfig(`{"enabled":true,"mode":"rules+tagger","actions":{"PERSON":"off","ADDRESS":"off"},"tagger":{"base_url":"http://127.0.0.1:1","model":"m"}}`)
	if !partial.TaggingEnabled() {
		t.Fatalf("tagger must still run for the remaining kinds")
	}
	if partial.Action(KindPerson) != ActionOff || partial.Action(KindAddress) != ActionOff {
		t.Fatalf("action override not honoured: %+v", partial.Actions)
	}
}
