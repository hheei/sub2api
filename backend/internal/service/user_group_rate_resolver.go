package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	gocache "github.com/patrickmn/go-cache"
	"golang.org/x/sync/singleflight"
)

// userGroupRateResolver 缓存用户在分组上的专属倍率配置（未求值），绝不缓存求值结果：
// 动态表达式依赖每次请求实际选中的上游账号，同一 (user, group) 在不同账号/时刻
// 的有效倍率不同，缓存求值结果会把一个账号的报价泄漏给另一个账号。
type userGroupRateResolver struct {
	repo         UserGroupRateRepository
	cache        *gocache.Cache
	cacheTTL     time.Duration
	sf           *singleflight.Group
	logComponent string
}

func newUserGroupRateResolver(repo UserGroupRateRepository, cache *gocache.Cache, cacheTTL time.Duration, sf *singleflight.Group, logComponent string) *userGroupRateResolver {
	if cacheTTL <= 0 {
		cacheTTL = defaultUserGroupRateCacheTTL
	}
	if cache == nil {
		cache = gocache.New(cacheTTL, time.Minute)
	}
	if logComponent == "" {
		logComponent = "service.gateway"
	}
	if sf == nil {
		sf = &singleflight.Group{}
	}

	return &userGroupRateResolver{
		repo:         repo,
		cache:        cache,
		cacheTTL:     cacheTTL,
		sf:           sf,
		logComponent: logComponent,
	}
}

// ResolveCustom 读取 (user, group) 的专属倍率配置。无覆盖返回 nil，这与
// "覆盖为 0/等于分组默认" 是两回事：调用方必须用 resolveRateForUpstream 区分。
// 仓储故障时返回 nil（回退分组口径的既有降级语义）并计入 fallback 指标。
func (r *userGroupRateResolver) ResolveCustom(ctx context.Context, userID, groupID int64) *UserGroupRate {
	if r == nil || userID <= 0 || groupID <= 0 {
		return nil
	}

	key := fmt.Sprintf("%d:%d", userID, groupID)
	if r.cache != nil {
		if cached, ok := r.cache.Get(key); ok {
			if custom, castOK := cached.(*UserGroupRate); castOK {
				userGroupRateCacheHitTotal.Add(1)
				return custom
			}
		}
	}
	if r.repo == nil {
		return nil
	}
	userGroupRateCacheMissTotal.Add(1)

	value, err, shared := r.sf.Do(key, func() (any, error) {
		if r.cache != nil {
			if cached, ok := r.cache.Get(key); ok {
				if custom, castOK := cached.(*UserGroupRate); castOK {
					userGroupRateCacheHitTotal.Add(1)
					return custom, nil
				}
			}
		}

		userGroupRateCacheLoadTotal.Add(1)
		custom, repoErr := r.repo.GetByUserAndGroup(ctx, userID, groupID)
		if repoErr != nil {
			return nil, repoErr
		}
		// 缓存未求值配置；无覆盖缓存 typed-nil，让 "查过且确认没有" 与 "没查过"
		// 区分开，避免每次请求都回源（缺省态同样不做负缓存失效管理）。
		if r.cache != nil {
			r.cache.Set(key, custom, r.cacheTTL)
		}
		return custom, nil
	})
	if shared {
		userGroupRateCacheSFSharedTotal.Add(1)
	}
	if err != nil {
		userGroupRateCacheFallbackTotal.Add(1)
		logger.LegacyPrintf(r.logComponent, "get user group rate failed, fallback to group config: user=%d group=%d err=%v", userID, groupID, err)
		return nil
	}

	custom, ok := value.(*UserGroupRate)
	if !ok {
		userGroupRateCacheFallbackTotal.Add(1)
		return nil
	}
	return custom
}
