package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

// new-api / one-api 系的中转没有 GET /v1/sub2api/billing，但公开的
// GET /api/pricing 会声明整站的分组倍率表（group_ratio）和每组每模型的
// 折算系数（group_compose_factor）。这类上游在 sub2api 端点 404/405 或
// 返回不可解析的 body 时，回退到这个端点，把账号上显式配置的分组名
// （accounts.extra.upstream_billing_group）对应的基准倍率，按
// sub2api.key_billing 的形状落进快照——展示与倍率同步因此沿用原有路径，
// 不需要前端或写回逻辑改动。
//
// 为什么分组必须显式配置：/api/pricing 是整站视图，任何公开端点都不会
// 告诉服务器"这把 Key 属于哪个分组"（/api/user/self 只收用户 access
// token，不收 sk- Key）。分组名缺省且整站只有一个分组时取那一个；否则
// 探测记 failed，错误码 newapi_group_unset / newapi_group_missing，并把
// 可选分组写进日志。
const (
	// UpstreamBillingNewAPIGroupExtraKey is accounts.extra key holding the
	// new-api group whose rate the probe reports.
	UpstreamBillingNewAPIGroupExtraKey = "upstream_billing_group"
	upstreamBillingNewAPIPath          = "/api/pricing"
	// upstreamBillingNewAPIGroupMaxLen bounds the group name an admin may type.
	upstreamBillingNewAPIGroupMaxLen = 64
)

var (
	// ErrUpstreamBillingNewAPIGroupInvalid is a group name no upstream declares.
	ErrUpstreamBillingNewAPIGroupInvalid = infraerrors.BadRequest(
		"INVALID_UPSTREAM_BILLING_GROUP",
		"upstream_billing_group must be one line of at most 64 characters",
	)
	// ErrUpstreamBillingGroupsUnsupported is an upstream without new-api pricing.
	ErrUpstreamBillingGroupsUnsupported = infraerrors.BadRequest(
		"UPSTREAM_BILLING_GROUPS_UNSUPPORTED",
		"this upstream does not publish new-api pricing groups",
	)
)

// UpstreamBillingGroup is one group a new-api upstream declares, with the rate
// it charges that group.
type UpstreamBillingGroup struct {
	Name  string  `json:"name"`
	Ratio float64 `json:"ratio"`
}

// newAPIGroupError is the pricing read fine but the account's group cannot be
// resolved: a configuration problem, not a transport or schema one.
type newAPIGroupError struct {
	reason string
	groups []string
}

func (e *newAPIGroupError) Error() string {
	return fmt.Sprintf("%s (groups: %s)", e.reason, strings.Join(e.groups, ", "))
}

// newAPIPricingResponse is the part of new-api's /api/pricing this probe reads.
// Fields new-api versions differ on are optional: only group_ratio and data
// are required.
type newAPIPricingResponse struct {
	Success            *bool                         `json:"success"`
	PricingVersion     string                        `json:"pricing_version"`
	GroupRatio         map[string]float64            `json:"group_ratio"`
	GroupComposeFactor map[string]map[string]float64 `json:"group_compose_factor"`
	Data               []newAPIModelPrice            `json:"data"`
}

type newAPIModelPrice struct {
	ModelName       string   `json:"model_name"`
	ModelRatio      float64  `json:"model_ratio"`
	ModelPrice      float64  `json:"model_price"`
	CompletionRatio float64  `json:"completion_ratio"`
	EnableGroups    []string `json:"enable_groups"`
}

// probeNewAPIFallback reads the account's upstream as a new-api site. handled
// is false when the site doesn't answer /api/pricing as new-api does, so the
// caller keeps its own unsupported/invalid_response verdict.
func (s *UpstreamBillingProbeService) probeNewAPIFallback(
	ctx context.Context,
	account *Account,
	intervalMinutes int,
	now time.Time,
	baseURL string,
	apiKey string,
	proxyURL string,
) (snapshot *UpstreamBillingProbeSnapshot, handled bool, err error) {
	group := strings.TrimSpace(account.GetExtraString(UpstreamBillingNewAPIGroupExtraKey))

	pricing, statusCode, fetchErr := s.fetchNewAPIPricing(ctx, account, baseURL, apiKey, proxyURL)
	if fetchErr != nil {
		return nil, false, nil
	}
	data, parseErr := newAPIPricingSnapshot(pricing, group, now)
	if parseErr != nil {
		var groupErr *newAPIGroupError
		if !errors.As(parseErr, &groupErr) {
			return nil, false, nil
		}
		logger.LegacyPrintf("service.upstream_billing_probe",
			"newapi_group_unresolved: account_id=%d reason=%s groups=%s",
			account.ID, groupErr.reason, strings.Join(groupErr.groups, ","))
		snapshot, err = s.persistProbeFailure(ctx, account, intervalMinutes, now, 0, groupErr.reason, 0)
		return snapshot, true, err
	}
	snapshot, err = s.recordProbeData(ctx, account, intervalMinutes, now, statusCode, data)
	return snapshot, true, err
}

// fetchNewAPIPricing reads baseURL's /api/pricing with the account's own
// transport, proxy and headers. Any answer that isn't a 2xx JSON document is
// an error: callers keep their own verdict about the upstream.
func (s *UpstreamBillingProbeService) fetchNewAPIPricing(
	ctx context.Context,
	account *Account,
	baseURL string,
	apiKey string,
	proxyURL string,
) (*newAPIPricingResponse, int, error) {
	probeCtx, cancel := context.WithTimeout(ctx, upstreamBillingProbeRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, newAPIPricingURL(baseURL), bytes.NewReader(nil))
	if err != nil {
		return nil, 0, err
	}
	profile := HTTPUpstreamProfileDefault
	if account.Platform == PlatformOpenAI {
		profile = HTTPUpstreamProfileOpenAI
	}
	req = req.WithContext(WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamProfile(req.Context(), profile)))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	account.ApplyHeaderOverrides(req.Header)
	var tlsProfile *tlsfingerprint.Profile
	if s.accountTestService.tlsFPProfileService != nil {
		tlsProfile = s.accountTestService.tlsFPProfileService.ResolveTLSProfile(account)
	}
	resp, err := s.accountTestService.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
	if err != nil {
		return nil, 0, err
	}
	if resp == nil || resp.Body == nil {
		return nil, 0, errors.New("new-api pricing: empty response")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, upstreamBillingProbeMaxBodyBytes+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if len(body) > upstreamBillingProbeMaxBodyBytes {
		return nil, resp.StatusCode, errors.New("new-api pricing: response too large")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("new-api pricing: http %d", resp.StatusCode)
	}
	var pricing newAPIPricingResponse
	if err := json.Unmarshal(body, &pricing); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("parse new-api pricing: %w", err)
	}
	return &pricing, resp.StatusCode, nil
}

// ListNewAPIGroups reads the groups the account's upstream publishes, for the
// admin to pick one from instead of typing its name.
func (s *UpstreamBillingProbeService) ListNewAPIGroups(ctx context.Context, accountID int64) ([]UpstreamBillingGroup, error) {
	if s == nil || s.accountRepo == nil || s.accountTestService == nil || s.accountTestService.httpUpstream == nil {
		return nil, ErrUpstreamBillingProbeUnavailable
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !isUpstreamBillingProbeAccount(account) {
		return nil, ErrUpstreamBillingProbeAccountInvalid
	}
	apiKey := account.GetCredential("api_key")
	baseURL := account.GetCredential("base_url")
	if account.RoutesProtocolByInbound() && account.IsAdaptiveAPIProtocol() {
		baseURL = account.GetCNProtocolBaseURL(APIProtocolChatCompletions)
	}
	if apiKey == "" || baseURL == "" {
		return nil, ErrUpstreamBillingGroupsUnsupported
	}
	normalizedBaseURL, err := s.accountTestService.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, ErrUpstreamBillingGroupsUnsupported
	}
	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil {
			return nil, ErrUpstreamBillingGroupsUnsupported
		}
		proxyURL = account.Proxy.URL()
	}
	pricing, _, err := s.fetchNewAPIPricing(ctx, account, normalizedBaseURL, apiKey, proxyURL)
	if err != nil || len(pricing.GroupRatio) == 0 {
		return nil, ErrUpstreamBillingGroupsUnsupported
	}
	groups := make([]UpstreamBillingGroup, 0, len(pricing.GroupRatio))
	for name, ratio := range pricing.GroupRatio {
		groups = append(groups, UpstreamBillingGroup{Name: name, Ratio: ratio})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return groups, nil
}

// newAPIPricingURL is baseURL's /api/pricing, whether baseURL names a version
// root (`…/v1`) or the site root.
func newAPIPricingURL(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return trimmed + upstreamBillingNewAPIPath
	}
	path := strings.TrimRight(parsed.Path, "/")
	for _, suffix := range []string{"/v1", "/v1beta"} {
		if strings.HasSuffix(strings.ToLower(path), suffix) {
			path = path[:len(path)-len(suffix)]
			break
		}
	}
	parsed.Path = path + upstreamBillingNewAPIPath
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

// parseNewAPIPricing turns a new-api pricing page into the snapshot data of a
// sub2api.key_billing declaration, so nothing downstream has to know the
// upstream speaks a different protocol.
func parseNewAPIPricing(body []byte, group string, now time.Time) (map[string]any, error) {
	var pricing newAPIPricingResponse
	if err := json.Unmarshal(body, &pricing); err != nil {
		return nil, fmt.Errorf("parse new-api pricing: %w", err)
	}
	return newAPIPricingSnapshot(&pricing, group, now)
}

// newAPIPricingSnapshot builds the snapshot data for group from an already
// decoded pricing document.
func newAPIPricingSnapshot(pricing *newAPIPricingResponse, group string, now time.Time) (map[string]any, error) {
	if pricing.Success != nil && !*pricing.Success {
		return nil, errors.New("new-api pricing reported success=false")
	}
	if len(pricing.Data) == 0 {
		return nil, errors.New("new-api pricing lists no model")
	}
	if len(pricing.GroupRatio) == 0 {
		return nil, errors.New("new-api pricing declares no group_ratio")
	}
	group = strings.TrimSpace(group)
	if group == "" {
		if len(pricing.GroupRatio) != 1 {
			return nil, &newAPIGroupError{reason: "newapi_group_unset", groups: sortedGroupNames(pricing.GroupRatio)}
		}
		for name := range pricing.GroupRatio {
			group = name
		}
	}
	ratio, ok := pricing.GroupRatio[group]
	if !ok {
		return nil, &newAPIGroupError{reason: "newapi_group_missing", groups: sortedGroupNames(pricing.GroupRatio)}
	}
	declared := upstreamBillingProbeResponse{
		Object:                  "sub2api.key_billing",
		SchemaVersion:           1,
		BillingScope:            "token",
		GroupRateMultiplier:     &ratio,
		ResolvedRateMultiplier:  &ratio,
		PeakRateEnabled:         boolPtr(false),
		EffectiveRateMultiplier: &ratio,
		ObservedAt:              now.UTC().Format(time.RFC3339Nano),
	}
	raw, err := json.Marshal(declared)
	if err != nil {
		return nil, fmt.Errorf("marshal new-api billing declaration: %w", err)
	}
	data, err := parseUpstreamBillingProbeResponse(raw)
	if err != nil {
		return nil, err
	}
	data["source"] = "newapi"
	data["upstream_group"] = group
	data["model_count"] = len(pricing.Data)
	if pricing.PricingVersion != "" {
		data["pricing_version"] = pricing.PricingVersion
	}
	// 有的分组按模型再打一次折（国模统一分组、视频统一分组）：一个标量
	// 表达不了，照原样记进快照，供人核对，不参与写回。
	if compose := pricing.GroupComposeFactor[group]; len(compose) > 0 {
		data["group_compose_factor"] = compose
	}
	return data, nil
}

func sortedGroupNames(ratios map[string]float64) []string {
	names := make([]string, 0, len(ratios))
	for name := range ratios {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
