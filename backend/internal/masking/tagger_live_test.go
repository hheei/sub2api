package masking

import (
	"context"
	"os"
	"testing"
)

// TestTaggerLive 是可选的联调测试：设置以下环境变量后才运行，否则跳过。
//
//	MASKING_TAGGER_URL=http://100.73.121.102:3425
//	MASKING_TAGGER_MODEL=gemini-3.5-flash-lite
//	MASKING_TAGGER_KEY=...
func TestTaggerLive(t *testing.T) {
	baseURL := os.Getenv("MASKING_TAGGER_URL")
	model := os.Getenv("MASKING_TAGGER_MODEL")
	if baseURL == "" || model == "" {
		t.Skip("set MASKING_TAGGER_URL / MASKING_TAGGER_MODEL to run the live tagger test")
	}
	cfg := ParseConfig(`{"enabled":true,"mode":"rules+tagger","tagger":{"timeout_ms":4000,"max_leaves":2}}`)
	cfg.Tagger.BaseURL = baseURL
	cfg.Tagger.Model = model
	cfg.Tagger.APIKey = os.Getenv("MASKING_TAGGER_KEY")

	masker := NewMasker(staticSettings{SettingKeyMaskingConfig: `{"enabled":true,"mode":"rules+tagger"}`})
	body := []byte(`{"messages":[{"role":"user","content":"我是欧阳锋，住在杭州市西湖区文三路 100 号 3 单元 502，工号 A12345，邮箱 zhang.san@corp.example"}]}`)
	masked, registry, hits := masker.Mask(context.Background(), cfg, body)
	t.Logf("hits=%d placeholders=%d", hits, registry.Len())
	t.Logf("masked=%s", masked)
	if registry.Len() == 0 {
		t.Fatalf("tagger produced no placeholder")
	}
}
