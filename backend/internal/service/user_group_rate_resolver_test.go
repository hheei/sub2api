package service

import (
	"context"
	"errors"
	"testing"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

type userGroupRateResolverRepoStub struct {
	UserGroupRateRepository

	rate  *UserGroupRate
	err   error
	calls int
}

func (s *userGroupRateResolverRepoStub) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*UserGroupRate, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.rate, nil
}

func TestNewUserGroupRateResolver_Defaults(t *testing.T) {
	resolver := newUserGroupRateResolver(nil, nil, 0, nil, "")

	require.NotNil(t, resolver)
	require.NotNil(t, resolver.cache)
	require.Equal(t, defaultUserGroupRateCacheTTL, resolver.cacheTTL)
	require.NotNil(t, resolver.sf)
	require.Equal(t, "service.gateway", resolver.logComponent)
}

func TestUserGroupRateResolverResolve_FallbackForNilResolverAndInvalidIDs(t *testing.T) {
	var nilResolver *userGroupRateResolver
	require.Nil(t, nilResolver.ResolveCustom(context.Background(), 101, 202))

	resolver := newUserGroupRateResolver(nil, nil, time.Second, nil, "service.test")
	require.Nil(t, resolver.ResolveCustom(context.Background(), 0, 202))
	require.Nil(t, resolver.ResolveCustom(context.Background(), 101, 0))
}

func TestUserGroupRateResolverResolve_InvalidCacheEntryLoadsRepoAndCaches(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	rate := &UserGroupRate{RateMultiplier: 1.7, RateMultiplierExpr: "$up * 2"}
	repo := &userGroupRateResolverRepoStub{rate: rate}
	cache := gocache.New(time.Minute, time.Minute)
	cache.Set("101:202", "bad-cache", time.Minute)
	resolver := newUserGroupRateResolver(repo, cache, time.Minute, nil, "service.test")

	got := resolver.ResolveCustom(context.Background(), 101, 202)
	require.Equal(t, rate, got)
	require.Equal(t, 1, repo.calls)

	// 缓存的是未求值配置本身，不能是某个上游账号求值后的数值。
	cached, ok := cache.Get("101:202")
	require.True(t, ok)
	require.Equal(t, rate, cached)

	hit, miss, load, _, fallback := GatewayUserGroupRateCacheStats()
	require.Equal(t, int64(0), hit)
	require.Equal(t, int64(1), miss)
	require.Equal(t, int64(1), load)
	require.Equal(t, int64(0), fallback)
}

func TestUserGroupRateResolverResolve_CachesAbsenceAsTypedNil(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	repo := &userGroupRateResolverRepoStub{}
	cache := gocache.New(time.Minute, time.Minute)
	resolver := newUserGroupRateResolver(repo, cache, time.Minute, nil, "service.test")

	require.Nil(t, resolver.ResolveCustom(context.Background(), 101, 202))
	require.Nil(t, resolver.ResolveCustom(context.Background(), 101, 202))
	require.Equal(t, 1, repo.calls, "确认无覆盖后必须命中缓存，不再回源")

	hit, miss, _, _, fallback := GatewayUserGroupRateCacheStats()
	require.Equal(t, int64(1), hit)
	require.Equal(t, int64(1), miss)
	require.Equal(t, int64(0), fallback)
}

func TestUserGroupRateResolverResolve_RepoErrorFallsBackToNil(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	repo := &userGroupRateResolverRepoStub{err: errors.New("db down")}
	resolver := newUserGroupRateResolver(repo, nil, time.Minute, nil, "service.test")

	require.Nil(t, resolver.ResolveCustom(context.Background(), 101, 202))

	_, _, _, _, fallback := GatewayUserGroupRateCacheStats()
	require.Equal(t, int64(1), fallback)
}

func TestGatewayServiceResolveEffectiveRate_PrecedenceAndUpstream(t *testing.T) {
	group := &Group{ID: 202, RateMultiplier: 1.5, RateMultiplierExpr: "$up * 2"}

	t.Run("静态用户覆盖压过分组表达式", func(t *testing.T) {
		repo := &userGroupRateResolverRepoStub{rate: &UserGroupRate{RateMultiplier: 1.1}}
		svc := &GatewayService{userGroupRateResolver: newUserGroupRateResolver(repo, nil, time.Minute, nil, "service.test")}

		got, dynamic := svc.ResolveEffectiveRate(context.Background(), 101, 202, group, 0.4)
		require.Equal(t, 1.1, got)
		require.False(t, dynamic)
	})

	t.Run("用户表达式随上游账号变化", func(t *testing.T) {
		repo := &userGroupRateResolverRepoStub{rate: &UserGroupRate{RateMultiplier: 1.1, RateMultiplierExpr: "$up + 0.25"}}
		svc := &GatewayService{userGroupRateResolver: newUserGroupRateResolver(repo, nil, time.Minute, nil, "service.test")}

		got, dynamic := svc.ResolveEffectiveRate(context.Background(), 101, 202, group, 0.5)
		require.InDelta(t, 0.75, got, 1e-12)
		require.True(t, dynamic)

		// 同一 (user, group) 换账号必须重新求值，缓存里只有配置没有倍率。
		got, dynamic = svc.ResolveEffectiveRate(context.Background(), 101, 202, group, 2)
		require.InDelta(t, 2.25, got, 1e-12)
		require.True(t, dynamic)
		require.Equal(t, 1, repo.calls)
	})

	t.Run("无用户覆盖时继承分组表达式", func(t *testing.T) {
		repo := &userGroupRateResolverRepoStub{}
		svc := &GatewayService{userGroupRateResolver: newUserGroupRateResolver(repo, nil, time.Minute, nil, "service.test")}

		got, dynamic := svc.ResolveEffectiveRate(context.Background(), 101, 202, group, 0.4)
		require.InDelta(t, 0.8, got, 1e-12)
		require.True(t, dynamic)
	})

	t.Run("用户覆盖为 0 与等于分组默认都保持优先", func(t *testing.T) {
		zero := &UserGroupRate{}
		repo := &userGroupRateResolverRepoStub{rate: zero}
		svc := &GatewayService{userGroupRateResolver: newUserGroupRateResolver(repo, nil, time.Minute, nil, "service.test")}
		got, dynamic := svc.ResolveEffectiveRate(context.Background(), 101, 202, group, 0.4)
		require.Equal(t, 0.0, got)
		require.False(t, dynamic)

		equal := &UserGroupRate{RateMultiplier: 1.5}
		repo = &userGroupRateResolverRepoStub{rate: equal}
		svc = &GatewayService{userGroupRateResolver: newUserGroupRateResolver(repo, nil, time.Minute, nil, "service.test")}
		got, dynamic = svc.ResolveEffectiveRate(context.Background(), 101, 202, group, 0.4)
		require.Equal(t, 1.5, got)
		require.False(t, dynamic)
	})

	t.Run("非法用户表达式回退到用户静态值而不是分组", func(t *testing.T) {
		repo := &userGroupRateResolverRepoStub{rate: &UserGroupRate{RateMultiplier: 1.2, RateMultiplierExpr: "$up / 0"}}
		svc := &GatewayService{userGroupRateResolver: newUserGroupRateResolver(repo, nil, time.Minute, nil, "service.test")}

		got, dynamic := svc.ResolveEffectiveRate(context.Background(), 101, 202, group, 0.4)
		require.Equal(t, 1.2, got)
		require.False(t, dynamic)
	})
}

func TestGatewayServiceResolveStaticRate_ReportsOverrideExistence(t *testing.T) {
	group := &Group{ID: 202, RateMultiplier: 1.5, RateMultiplierExpr: "$up * 2"}

	t.Run("覆盖为 0 仍报告存在", func(t *testing.T) {
		repo := &userGroupRateResolverRepoStub{rate: &UserGroupRate{}}
		svc := &GatewayService{userGroupRateResolver: newUserGroupRateResolver(repo, nil, time.Minute, nil, "service.test")}

		rate, hasCustom := svc.ResolveStaticRate(context.Background(), 101, 202, group)
		require.Equal(t, 0.0, rate)
		require.True(t, hasCustom)
	})

	t.Run("覆盖等于分组默认仍报告存在", func(t *testing.T) {
		repo := &userGroupRateResolverRepoStub{rate: &UserGroupRate{RateMultiplier: 1.5}}
		svc := &GatewayService{userGroupRateResolver: newUserGroupRateResolver(repo, nil, time.Minute, nil, "service.test")}

		rate, hasCustom := svc.ResolveStaticRate(context.Background(), 101, 202, group)
		require.Equal(t, 1.5, rate)
		require.True(t, hasCustom)
	})

	t.Run("动态用户表达式取声明的静态回退值且不编造求值结果", func(t *testing.T) {
		repo := &userGroupRateResolverRepoStub{rate: &UserGroupRate{RateMultiplier: 1.2, RateMultiplierExpr: "$up * 2"}}
		svc := &GatewayService{userGroupRateResolver: newUserGroupRateResolver(repo, nil, time.Minute, nil, "service.test")}

		rate, hasCustom := svc.ResolveStaticRate(context.Background(), 101, 202, group)
		require.Equal(t, 1.2, rate)
		require.True(t, hasCustom)
	})

	t.Run("无覆盖时用分组静态值且报告不存在", func(t *testing.T) {
		repo := &userGroupRateResolverRepoStub{}
		svc := &GatewayService{userGroupRateResolver: newUserGroupRateResolver(repo, nil, time.Minute, nil, "service.test")}

		rate, hasCustom := svc.ResolveStaticRate(context.Background(), 101, 202, group)
		require.Equal(t, 1.5, rate)
		require.False(t, hasCustom)
	})
}
