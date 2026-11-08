package masking

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type staticSettings map[string]string

func (s staticSettings) GetRawValue(_ context.Context, key string) (string, error) {
	return s[key], nil
}

func newTestMasker(enabled bool) *Masker {
	config := `{"enabled":false}`
	if enabled {
		config = `{"enabled":true}`
	}
	return NewMasker(staticSettings{SettingKeyMaskingConfig: config})
}

func newRuleMasker() *Masker {
	return NewMasker(staticSettings{SettingKeyMaskingConfig: `{"enabled":true,"mode":"rules"}`})
}

func TestMaskRulesMaskAndSkipOpaque(t *testing.T) {
	body := []byte(`{
		"model": "claude-sonnet-4",
		"messages": [{"role": "user", "content": "contact alice@example.com"}],
		"system": "password = hunter2hunter2",
		"tools": [{"name": "lookup", "input_schema": {"properties": {"email": {"description": "alice@example.com"}}}}],
		"signature": "alice@example.com"
	}`)
	masker := newTestMasker(true)
	cfg := masker.Config(context.Background())
	masked, registry, hits := masker.Mask(context.Background(), cfg, body)
	if hits < 2 {
		t.Fatalf("expected at least 2 hits, got %d", hits)
	}
	if !json.Valid(masked) {
		t.Fatalf("masked body is not valid JSON: %s", masked)
	}
	text := string(masked)
	if strings.Contains(text, `"content": "contact alice@example.com`) {
		t.Fatalf("email in message content must be masked: %s", text)
	}
	if strings.Count(text, "alice@example.com") != 2 {
		t.Fatalf("only signature and tools may keep the literal: %s", text)
	}
	if !strings.Contains(text, `"signature": "alice@example.com"`) {
		t.Fatalf("opaque field must stay byte-identical: %s", text)
	}
	if !strings.Contains(text, `"description": "alice@example.com"`) {
		t.Fatalf("tools subtree must stay byte-identical: %s", text)
	}
	if registry.Len() != hits {
		t.Fatalf("registry=%d hits=%d", registry.Len(), hits)
	}
}

func TestMaskIsIdempotent(t *testing.T) {
	body := []byte(`{"messages":[{"content":"mail alice@example.com"}]}`)
	masker := newTestMasker(true)
	cfg := masker.Config(context.Background())
	once, _, _ := masker.Mask(context.Background(), cfg, body)
	twice, registry, _ := masker.Mask(context.Background(), cfg, once)
	if string(once) != string(twice) {
		t.Fatalf("second pass changed placeholders: %s -> %s", once, twice)
	}
	if registry.Len() != 0 {
		t.Fatalf("second pass registered %d placeholders", registry.Len())
	}
}

func TestNonJSONBodyUntouched(t *testing.T) {
	masker := newTestMasker(true)
	body := []byte("alice@example.com -- not json")
	_, registry, hits := masker.Mask(context.Background(), masker.Config(context.Background()), body)
	if hits != 0 || registry.Len() != 0 {
		t.Fatalf("non-JSON body must be skipped, hits=%d", hits)
	}
}

func TestScanJSONLeavesRejectsInvalidJSONWithoutPanic(t *testing.T) {
	if _, ok := scanJSONLeaves([]byte(`{"content":"broken`)); ok {
		t.Fatalf("truncated JSON must be rejected")
	}
	if _, ok := scanJSONLeaves([]byte(`not json at all`)); ok {
		t.Fatalf("non-JSON must be rejected")
	}
}

func TestMaskRulesCoverCredentials(t *testing.T) {
	cases := []string{
		"Bearer abcdefghijklmnopqrstuvwxyz012345",
		"mysql://root:Sup3rS3cret@10.0.0.1:3306/db",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		"sk-live-abcdefghijklmnopqrstuvwx",
	}
	masker := newRuleMasker()
	cfg := masker.Config(context.Background())
	for _, sample := range cases {
		masked, registry, _ := masker.Mask(context.Background(), cfg, []byte(`{"content":"`+sample+`"}`))
		if registry.Len() == 0 {
			t.Fatalf("credential not masked: %s", sample)
		}
		if strings.Contains(string(masked), sample) {
			t.Fatalf("literal survived: %s", masked)
		}
	}
}

func TestPasswordRuleKeepsEnclosingBraces(t *testing.T) {
	masker := newRuleMasker()
	masked, _, _ := masker.Mask(context.Background(), masker.Config(context.Background()), []byte(`{"content":"api_key=abcdef123456"}`))
	pattern := regexp.MustCompile(`^\{"content":"api_key=\{\{PASSWORD_[0-9a-f]{8,}\}\}"\}$`)
	if !pattern.Match(masked) {
		t.Fatalf("unexpected masking result: %s", masked)
	}
}

func TestDisabledConfigMasksNothing(t *testing.T) {
	masker := newTestMasker(false)
	if cfg := masker.Config(context.Background()); cfg.Enabled {
		t.Fatalf("disabled config parsed as enabled")
	}
}

func TestValidatedSpansDropsHallucination(t *testing.T) {
	text := "欧阳锋大侠住在杭州,联系方式 alice@example.com"
	spans := validatedSpans(text, []span{
		{Kind: "PERSON", Text: "欧阳锋大侠"},
		{Kind: "PERSON", Text: "李四少爷"},
		{Kind: "UNKNOWN", Text: "欧阳锋大侠"},
		{Kind: "ADDRESS", Text: "{{EMAIL_abcd1234}}"},
	})
	if len(spans) != 1 || spans[0].Text != "欧阳锋大侠" {
		t.Fatalf("unexpected spans: %+v", spans)
	}
}

func TestRestoreAcrossChunkBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	registry := newRegistry()
	token := registry.Token(KindEmail, "alice@example.com")
	writer := NewRestoreWriter(ctx.Writer, registry)

	payload := "data: {\"delta\":\"hi " + token + " bye\"}\n\n"
	for _, part := range []string{payload[:12], payload[12:20], payload[20:]} {
		if _, err := writer.WriteString(part); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	writer.Finish()
	want := strings.ReplaceAll(payload, token, "alice@example.com")
	if got := recorder.Body.String(); got != want {
		t.Fatalf("restore mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestRestoreLeavesUnknownTextUntouched(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	registry := newRegistry()
	registry.Token(KindEmail, "alice@example.com")
	writer := NewRestoreWriter(ctx.Writer, registry)
	chunk := "{\"partial\":\"{{EMAIL_ab"
	_, _ = writer.WriteString(chunk)
	writer.Finish()
	if got := recorder.Body.String(); got != chunk {
		t.Fatalf("unknown prefix must pass through: %q", got)
	}
}

func TestParseConfigInvalidJSONFailsClosed(t *testing.T) {
	cfg := ParseConfig("{not json")
	if cfg.Enabled {
		t.Fatalf("broken config must stay disabled")
	}
	if cfg.Mode != ModeRules || cfg.MaxBodyBytes <= 0 {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}
