package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/masking"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// maskingConfigView 是管理台读到的视图：api_key 永不下发，只给"是否已配置"。
type maskingConfigView struct {
	Config        masking.Config `json:"config"`
	HasAPIKey     bool           `json:"has_api_key"`
	DefaultNotice string         `json:"default_notice"`
	Kinds         []string       `json:"kinds"`
}

type maskingUpdateRequest struct {
	Config masking.Config `json:"config"`
}

type maskingPreviewRequest struct {
	Text   string          `json:"text"`
	Config *masking.Config `json:"config"`
}

func (h *SettingHandler) loadMaskingConfig(c *gin.Context) (masking.Config, string, error) {
	var cfg masking.Config
	raw, err := h.settingService.GetRawValue(c.Request.Context(), masking.SettingKeyMaskingConfig)
	if err != nil && !errors.Is(err, service.ErrSettingNotFound) {
		return cfg, "", err
	}
	cfg = *masking.ParseConfig(raw)
	apiKey := strings.TrimSpace(cfg.Tagger.APIKey)
	cfg.Tagger.APIKey = ""
	return cfg, apiKey, nil
}

func maskingView(cfg masking.Config, apiKey string) maskingConfigView {
	kinds := make([]string, 0, len(masking.AllKinds()))
	for _, kind := range masking.AllKinds() {
		kinds = append(kinds, string(kind))
	}
	return maskingConfigView{
		Config:        cfg,
		HasAPIKey:     apiKey != "",
		DefaultNotice: masking.DefaultNotice,
		Kinds:         kinds,
	}
}

// GetMaskingConfig GET /admin/settings/masking/config
func (h *SettingHandler) GetMaskingConfig(c *gin.Context) {
	cfg, apiKey, err := h.loadMaskingConfig(c)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, maskingView(cfg, apiKey))
}

// UpdateMaskingConfig PUT /admin/settings/masking/config
func (h *SettingHandler) UpdateMaskingConfig(c *gin.Context) {
	var request maskingUpdateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "无效的脱敏配置")
		return
	}
	_, existingAPIKey, err := h.loadMaskingConfig(c)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if strings.TrimSpace(request.Config.Tagger.APIKey) == "" {
		request.Config.Tagger.APIKey = existingAPIKey
	}
	if err := request.Config.Validate(); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	raw, err := json.Marshal(request.Config)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.settingService.SetRawValue(c.Request.Context(), masking.SettingKeyMaskingConfig, string(raw)); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	saved, apiKey, err := h.loadMaskingConfig(c)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, maskingView(saved, apiKey))
}

// PreviewMasking POST /admin/settings/masking/preview
func (h *SettingHandler) PreviewMasking(c *gin.Context) {
	var request maskingPreviewRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "无效的试跑请求")
		return
	}
	cfg := request.Config
	if cfg == nil {
		saved, _, err := h.loadMaskingConfig(c)
		if err != nil {
			response.Error(c, http.StatusInternalServerError, err.Error())
			return
		}
		cfg = &saved
	}
	masked, hits, kinds := masking.Preview(cfg, request.Text)
	response.Success(c, gin.H{"masked": masked, "hits": hits, "kinds": kinds})
}
