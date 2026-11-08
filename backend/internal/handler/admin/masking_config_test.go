package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/masking"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newMaskingTestHandler(t *testing.T) (*SettingHandler, *service.SettingService) {
	t.Helper()
	svc := service.NewSettingService(newTestSettingRepo(), &config.Config{})
	return NewSettingHandler(svc, nil, nil, nil, nil, nil, nil), svc
}

func doMaskingRequest(t *testing.T, h *SettingHandler, method, path, body string, handler gin.HandlerFunc) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Handle(method, path, handler)
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var payload struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload), recorder.Body.String())
	return payload.Data
}

func TestMaskingConfigRoundTrip(t *testing.T) {
	handler, svc := newMaskingTestHandler(t)

	initial := doMaskingRequest(t, handler, http.MethodGet, "/config", "", handler.GetMaskingConfig)
	config, _ := initial["config"].(map[string]any)
	require.NotNil(t, config)
	require.Equal(t, false, config["enabled"], "masking must default to disabled")

	update := `{"config":{"enabled":true,"mode":"rules","max_body_bytes":2097152,"tagger":{"base_url":"","model":"","timeout_ms":1500,"max_leaves":8},"notice":{"enabled":true,"text":""}}}`
	saved := doMaskingRequest(t, handler, http.MethodPut, "/config", update, handler.UpdateMaskingConfig)
	savedConfig, _ := saved["config"].(map[string]any)
	require.Equal(t, true, savedConfig["enabled"])

	raw, err := svc.GetRawValue(httptest.NewRequest(http.MethodGet, "/", nil).Context(), masking.SettingKeyMaskingConfig)
	require.NoError(t, err)
	persisted := masking.ParseConfig(raw)
	require.True(t, persisted.Enabled)
	require.Equal(t, masking.ModeRules, persisted.Mode)
}

func TestMaskingConfigRejectsInvalidMode(t *testing.T) {
	handler, _ := newMaskingTestHandler(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/config", handler.UpdateMaskingConfig)
	req := httptest.NewRequest(http.MethodPut, "/config", bytes.NewReader([]byte(`{"config":{"enabled":true,"mode":"rules+tagger"}}`)))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String(), "tagger mode without endpoint must be rejected")
}

func TestMaskingPreviewMasksRules(t *testing.T) {
	handler, _ := newMaskingTestHandler(t)
	result := doMaskingRequest(t, handler, http.MethodPost, "/preview", `{"text":"联系 alice@example.com 或 13800138000"}`, handler.PreviewMasking)
	require.Contains(t, result["masked"].(string), "{{EMAIL_")
	require.NotContains(t, result["masked"].(string), "alice@example.com")
	require.GreaterOrEqual(t, result["hits"].(float64), float64(2))
}
