package middleware

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/masking"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type maskingTestSettings map[string]string

func (s maskingTestSettings) GetRawValue(_ context.Context, key string) (string, error) {
	return s[key], nil
}

func newMaskingTestRouter(t *testing.T, config string, upstream *string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	masker := masking.NewMasker(maskingTestSettings{masking.SettingKeyMaskingConfig: config})
	router := gin.New()
	router.POST("/v1/messages", Masking(masker), func(c *gin.Context) {
		body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
		require.NoError(t, err)
		if upstream != nil {
			*upstream = string(body)
		}
		token := regexp.MustCompile(`\{\{EMAIL_[0-9a-f]+\}\}`).FindString(string(body))
		c.Header("Content-Type", "text/event-stream")
		c.Header("Content-Length", "999")
		payload := "data: {\"delta\":\"mail " + token + " now\"}\n\n"
		split := strings.Index(payload, token) + len(token)/2
		_, _ = c.Writer.WriteString(payload[:split])
		c.Writer.Flush()
		_, _ = c.Writer.WriteString(payload[split : len(payload)-4])
		c.Writer.Flush()
		_, _ = c.Writer.WriteString(payload[len(payload)-4:])
	})
	return router
}

const maskingTestBody = `{"model":"claude-sonnet-4","messages":[{"role":"user","content":"mail alice@example.com"}]}`

func TestMaskingMiddlewareMasksUpstreamAndRestoresClient(t *testing.T) {
	var upstream string
	router := newMaskingTestRouter(t, `{"enabled":true,"mode":"rules"}`, &upstream)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(maskingTestBody))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.NotContains(t, upstream, "alice@example.com", "upstream must see placeholders only")
	require.Contains(t, upstream, "{{EMAIL_")
	require.True(t, jsonValid(upstream), "masked upstream body must stay valid JSON: %s", upstream)

	clientBody := recorder.Body.String()
	require.Contains(t, clientBody, "alice@example.com", "client must get the original value back")
	require.NotContains(t, clientBody, "{{EMAIL_", "no placeholder may leak downstream")
	require.Empty(t, recorder.Header().Get("Content-Length"), "content length must be dropped when restoring")
}

func TestMaskingMiddlewareRestoresPlaceholderSplitAcrossSSEChunks(t *testing.T) {
	var upstream string
	router := newMaskingTestRouter(t, `{"enabled":true,"mode":"rules"}`, &upstream)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(maskingTestBody))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Contains(t, upstream, "{{EMAIL_")
	require.Contains(t, recorder.Body.String(), `data: {"delta":"mail alice@example.com now"}`)
}

func TestMaskingMiddlewareHandlesGzippedRequest(t *testing.T) {
	var upstream string
	router := newMaskingTestRouter(t, `{"enabled":true,"mode":"rules"}`, &upstream)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err := writer.Write([]byte(maskingTestBody))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(compressed.Bytes()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Contains(t, upstream, "{{EMAIL_", "handler must receive decompressed, masked JSON")
	require.NotContains(t, upstream, "alice@example.com")
	require.Contains(t, recorder.Body.String(), "alice@example.com")
}

func TestMaskingMiddlewareSkipsWebSocketUpgrade(t *testing.T) {
	var upstream string
	router := newMaskingTestRouter(t, `{"enabled":true,"mode":"rules"}`, &upstream)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(maskingTestBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Upgrade", "websocket")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Contains(t, upstream, "alice@example.com", "ws upgrade must not be rewritten")
}

func TestMaskingMiddlewareSkipsNonJSONAndDisabled(t *testing.T) {
	var upstream string
	router := newMaskingTestRouter(t, `{"enabled":true,"mode":"rules"}`, &upstream)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(maskingTestBody))
	req.Header.Set("Content-Type", "application/octet-stream")
	router.ServeHTTP(httptest.NewRecorder(), req)
	require.Contains(t, upstream, "alice@example.com")

	disabled := newMaskingTestRouter(t, `{"enabled":false,"mode":"rules"}`, &upstream)
	req = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(maskingTestBody))
	req.Header.Set("Content-Type", "application/json")
	disabled.ServeHTTP(httptest.NewRecorder(), req)
	require.Contains(t, upstream, "alice@example.com", "disabled config must pass plaintext through")
}

func jsonValid(value string) bool {
	return json.Valid([]byte(value))
}

func TestMaskingInjectsNoticeEvenWithoutHits(t *testing.T) {
	var upstream string
	router := newMaskingTestRouter(t, `{"enabled":true,"mode":"rules"}`, &upstream)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude","system":"base system","messages":[{"role":"user","content":"no secrets here"}]}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if !strings.Contains(upstream, masking.DefaultNotice) {
		t.Fatalf("notice must be injected on every request, got: %s", upstream)
	}
	if !json.Valid([]byte(upstream)) {
		t.Fatalf("upstream body invalid: %s", upstream)
	}
	if !strings.Contains(upstream, "base system") {
		t.Fatalf("client system prompt must be preserved: %s", upstream)
	}
}

func TestMaskingNoticeCanBeDisabled(t *testing.T) {
	var upstream string
	router := newMaskingTestRouter(t, `{"enabled":true,"mode":"rules","notice":{"enabled":false}}`, &upstream)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude","system":"base","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), req)
	if strings.Contains(upstream, masking.DefaultNotice) {
		t.Fatalf("notice must not be injected when disabled: %s", upstream)
	}
}
