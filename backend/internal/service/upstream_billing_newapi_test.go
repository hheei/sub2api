package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const newAPIPricingFixture = `{
	"success": true,
	"pricing_version": "a42d372c",
	"group_ratio": {"Kiro正价": 0.33, "Codex-pro": 0.14, "国模统一分组": 1},
	"group_compose_factor": {"国模统一分组": {"kimi-k3": 0.15}},
	"usable_group": {"Kiro正价": "正价", "Codex-pro": "pro"},
	"data": [
		{"model_name": "gpt-6-astra", "model_ratio": 5, "completion_ratio": 6, "enable_groups": ["Codex-pro"]},
		{"model_name": "claude-sonnet-4.5", "model_ratio": 1.5, "completion_ratio": 5, "enable_groups": ["Kiro正价"]}
	]
}`

func TestNewAPIPricingURL(t *testing.T) {
	cases := map[string]string{
		"https://cf.a-piko.top":        "https://cf.a-piko.top/api/pricing",
		"https://cf.a-piko.top/":       "https://cf.a-piko.top/api/pricing",
		"https://cf.a-piko.top/v1":     "https://cf.a-piko.top/api/pricing",
		"https://cf.a-piko.top/v1/":    "https://cf.a-piko.top/api/pricing",
		"https://cf.a-piko.top/v1beta": "https://cf.a-piko.top/api/pricing",
		"https://cf.a-piko.top/sub/v1": "https://cf.a-piko.top/sub/api/pricing",
		"https://cf.a-piko.top/?x=1":   "https://cf.a-piko.top/api/pricing",
	}
	for in, want := range cases {
		require.Equal(t, want, newAPIPricingURL(in), in)
	}
}

func TestParseNewAPIPricingDeclaresConfiguredGroupRate(t *testing.T) {
	now := time.Date(2026, time.November, 7, 3, 0, 0, 0, time.UTC)

	data, err := parseNewAPIPricing([]byte(newAPIPricingFixture), "Kiro正价", now)
	require.NoError(t, err)
	require.Equal(t, "sub2api.key_billing", data["object"])
	require.Equal(t, "newapi", data["source"])
	require.Equal(t, "Kiro正价", data["upstream_group"])
	require.Equal(t, 0.33, data["resolved_rate_multiplier"])
	require.Equal(t, 0.33, data["effective_rate_multiplier"])
	require.Equal(t, 2, data["model_count"])
	require.Equal(t, "a42d372c", data["pricing_version"])
	require.Equal(t, now.Format(time.RFC3339Nano), data["observed_at"])
	require.NotContains(t, data, "group_compose_factor")

	// 快照照旧能被同步逻辑读成写回值——前端与写回路径无需知道上游换了协议。
	rate, ok := upstreamBillingProbeSyncRate(data)
	require.True(t, ok)
	require.Equal(t, 0.33, rate)
}

func TestParseNewAPIPricingKeepsGroupComposeFactor(t *testing.T) {
	now := time.Date(2026, time.November, 7, 3, 0, 0, 0, time.UTC)

	data, err := parseNewAPIPricing([]byte(newAPIPricingFixture), "国模统一分组", now)
	require.NoError(t, err)
	require.Equal(t, 1.0, data["resolved_rate_multiplier"])
	require.Equal(t, map[string]float64{"kimi-k3": 0.15}, data["group_compose_factor"])
}

func TestParseNewAPIPricingUnknownGroupNamesTheChoices(t *testing.T) {
	now := time.Date(2026, time.November, 7, 3, 0, 0, 0, time.UTC)

	data, err := parseNewAPIPricing([]byte(newAPIPricingFixture), "不存在的分组", now)
	var groupErr *newAPIGroupError
	require.ErrorAs(t, err, &groupErr)
	require.Equal(t, "newapi_group_missing", groupErr.reason)
	require.Equal(t, []string{"Codex-pro", "Kiro正价", "国模统一分组"}, groupErr.groups)
	require.Nil(t, data)
}

func TestParseNewAPIPricingGroupResolution(t *testing.T) {
	now := time.Date(2026, time.November, 7, 3, 0, 0, 0, time.UTC)
	single := `{"group_ratio":{"only":0.42},"data":[{"model_name":"m"}]}`

	data, err := parseNewAPIPricing([]byte(single), "", now)
	require.NoError(t, err)
	require.Equal(t, "only", data["upstream_group"])
	require.Equal(t, 0.42, data["resolved_rate_multiplier"])

	_, err = parseNewAPIPricing([]byte(newAPIPricingFixture), "", now)
	var groupErr *newAPIGroupError
	require.ErrorAs(t, err, &groupErr)
	require.Equal(t, "newapi_group_unset", groupErr.reason)
}

func TestParseNewAPIPricingRejectsNonNewAPIBodies(t *testing.T) {
	now := time.Date(2026, time.November, 7, 3, 0, 0, 0, time.UTC)

	for name, body := range map[string]string{
		"html":          "<!doctype html><html></html>",
		"success false": `{"success":false,"group_ratio":{"g":1},"data":[{"model_name":"m"}]}`,
		"no models":     `{"group_ratio":{"g":1},"data":[]}`,
		"no group":      `{"data":[{"model_name":"m"}]}`,
	} {
		_, err := parseNewAPIPricing([]byte(body), "g", now)
		require.Error(t, err, name)
		var groupErr *newAPIGroupError
		require.False(t, errors.As(err, &groupErr), name)
	}
}

func TestUpstreamBillingProbeFallsBackToNewAPIPricing(t *testing.T) {
	initialRate := 0.5
	account := &Account{
		ID:          62,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 2,
		Credentials: map[string]any{
			"api_key":  "sk-apiko",
			"base_url": "https://cf.a-piko.top",
		},
		Extra: map[string]any{
			UpstreamBillingProbeEnabledExtraKey:    true,
			UpstreamBillingRateSyncEnabledExtraKey: true,
			UpstreamBillingNewAPIGroupExtraKey:     "Kiro正价",
		},
		RateMultiplier: &initialRate,
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusNotFound,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"Invalid URL (GET /v1/sub2api/billing)"}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(newAPIPricingFixture)),
		},
	}}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	svc.now = func() time.Time { return time.Date(2026, time.November, 7, 3, 0, 0, 0, time.UTC) }

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusOK, snapshot.Status)
	require.Equal(t, "newapi", snapshot.Data["source"])
	require.Equal(t, "Kiro正价", snapshot.Data["upstream_group"])
	require.Equal(t, http.StatusOK, snapshot.HTTPStatus)
	require.Len(t, upstream.requests, 2)
	require.True(t, strings.HasSuffix(upstream.requests[1].URL.Path, "/api/pricing"))
	require.NotNil(t, account.RateMultiplier)
	require.Equal(t, 0.33, *account.RateMultiplier)
}

func TestUpstreamBillingProbeNewAPIWithoutGroupMarksFailed(t *testing.T) {
	account := &Account{
		ID:          63,
		Platform:    PlatformAnthropic,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 2,
		Credentials: map[string]any{
			"api_key":  "sk-apiko",
			"base_url": "https://cf.a-piko.top/v1",
		},
		Extra: map[string]any{UpstreamBillingProbeEnabledExtraKey: true},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusNotFound,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"not found"}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(newAPIPricingFixture)),
		},
	}}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})
	svc.now = func() time.Time { return time.Date(2026, time.November, 7, 3, 0, 0, 0, time.UTC) }

	snapshot, err := svc.ProbeAccount(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, UpstreamBillingProbeStatusFailed, snapshot.Status)
	require.Equal(t, "newapi_group_unset", snapshot.LastError)
	require.Nil(t, account.RateMultiplier)
}

func TestListNewAPIGroupsReadsUpstreamGroups(t *testing.T) {
	account := &Account{
		ID:          62,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 2,
		Credentials: map[string]any{
			"api_key":  "sk-apiko",
			"base_url": "https://cf.a-piko.top/v1",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(newAPIPricingFixture)),
	}}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})

	groups, err := svc.ListNewAPIGroups(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, []UpstreamBillingGroup{
		{Name: "Codex-pro", Ratio: 0.14},
		{Name: "Kiro正价", Ratio: 0.33},
		{Name: "国模统一分组", Ratio: 1},
	}, groups)
	require.True(t, strings.HasSuffix(upstream.requests[0].URL.Path, "/api/pricing"))
}

func TestListNewAPIGroupsRejectsUpstreamWithoutPricingPage(t *testing.T) {
	account := &Account{
		ID:          16,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Concurrency: 2,
		Credentials: map[string]any{
			"api_key":  "sk-aijws",
			"base_url": "https://api.aijws.com",
		},
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{"Content-Type": []string{"text/html"}},
		Body:       io.NopCloser(strings.NewReader("<html></html>")),
	}}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{})

	_, err := svc.ListNewAPIGroups(context.Background(), account.ID)
	require.ErrorIs(t, err, ErrUpstreamBillingGroupsUnsupported)
}
