//go:build integration

package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

// TestIsDynamicRateRoundTripAndSnapshotIsolation 钉死 usage_logs.is_dynamic_rate 的三态
// 语义：NULL = 历史行未知，true/false = 落库快照。回读必须按行取值，绝不因分组配置
// 之后被修改或分组被删除而变化。
func (s *UsageLogRepoSuite) TestIsDynamicRateRoundTripAndSnapshotIsolation() {
	flagTrue := true
	flagFalse := false

	user := mustCreateUser(s.T(), s.client, &service.User{Email: "dyn-rate-" + uuid.NewString() + "@test.com"})
	apiKey := mustCreateApiKey(s.T(), s.client, &service.APIKey{UserID: user.ID, Key: "sk-dyn-rate-" + uuid.NewString(), Name: "k"})
	account := mustCreateAccount(s.T(), s.client, &service.Account{Name: "acc-dyn-rate-" + uuid.NewString()})

	dynamicGroup := mustCreateGroup(s.T(), s.client, &service.Group{Name: "dyn-rate-group-" + uuid.NewString()})
	_, err := s.client.Group.UpdateOneID(dynamicGroup.ID).SetRateMultiplierExpr("$up * 1.05").Save(s.ctx)
	s.Require().NoError(err, "make the group dynamic after creation")

	insert := func(requestID string, flag *bool) *service.UsageLog {
		log := &service.UsageLog{
			UserID:         user.ID,
			APIKeyID:       apiKey.ID,
			AccountID:      account.ID,
			RequestID:      requestID,
			Model:          "claude-3",
			InputTokens:    10,
			OutputTokens:   20,
			TotalCost:      0.5,
			ActualCost:     0.4,
			RateMultiplier: 0.63,
			IsDynamicRate:  flag,
			GroupID:        &dynamicGroup.ID,
		}
		_, err := s.repo.Create(s.ctx, log)
		s.Require().NoError(err, "Create")
		s.Require().NotZero(log.ID)
		return log
	}

	unknown := insert("dyn-rate-unknown-"+uuid.NewString(), nil)
	storedFalse := insert("dyn-rate-false-"+uuid.NewString(), &flagFalse)
	storedTrue := insert("dyn-rate-true-"+uuid.NewString(), &flagTrue)

	// 回读：三态各自保持。
	gotUnknown, err := s.repo.GetByID(s.ctx, unknown.ID)
	s.Require().NoError(err)
	s.Require().Nil(gotUnknown.IsDynamicRate, "NULL must round-trip as unknown")

	gotFalse, err := s.repo.GetByID(s.ctx, storedFalse.ID)
	s.Require().NoError(err)
	s.Require().NotNil(gotFalse.IsDynamicRate, "stored false must not be read back as unknown")
	s.Require().False(*gotFalse.IsDynamicRate)

	gotTrue, err := s.repo.GetByID(s.ctx, storedTrue.ID)
	s.Require().NoError(err)
	s.Require().NotNil(gotTrue.IsDynamicRate)
	s.Require().True(*gotTrue.IsDynamicRate)

	// 列表路径同样按行返回，不加载 Group。
	logs, _, err := s.repo.ListWithFilters(s.ctx,
		pagination.PaginationParams{Page: 1, PageSize: 50},
		usagestats.UsageLogFilters{APIKeyID: apiKey.ID, ExactTotal: true})
	s.Require().NoError(err)
	byRequestID := make(map[string]*bool, len(logs))
	for i := range logs {
		byRequestID[logs[i].RequestID] = logs[i].IsDynamicRate
	}
	s.Require().Nil(byRequestID[unknown.RequestID], "list: NULL must stay unknown")
	s.Require().NotNil(byRequestID[storedFalse.RequestID])
	s.Require().False(*byRequestID[storedFalse.RequestID])
	s.Require().NotNil(byRequestID[storedTrue.RequestID])
	s.Require().True(*byRequestID[storedTrue.RequestID])

	// 把分组从动态改回静态后，历史行的快照不变。
	_, err = s.client.Group.UpdateOneID(dynamicGroup.ID).SetRateMultiplierExpr("").Save(s.ctx)
	s.Require().NoError(err)

	for _, tc := range []struct {
		id   int64
		want *bool
	}{
		{unknown.ID, nil},
		{storedFalse.ID, &flagFalse},
		{storedTrue.ID, &flagTrue},
	} {
		got, err := s.repo.GetByID(s.ctx, tc.id)
		s.Require().NoError(err)
		if tc.want == nil {
			s.Require().Nil(got.IsDynamicRate, "group edit must not invent a value for historical rows")
			continue
		}
		s.Require().NotNil(got.IsDynamicRate)
		s.Require().Equal(*tc.want, *got.IsDynamicRate, "group edit must not rewrite stored snapshots")
	}

	// 分组删除后同样不变。
	s.Require().NoError(s.client.Group.DeleteOneID(dynamicGroup.ID).Exec(s.ctx))
	gotAfterDelete, err := s.repo.GetByID(s.ctx, storedTrue.ID)
	s.Require().NoError(err)
	s.Require().NotNil(gotAfterDelete.IsDynamicRate)
	s.Require().True(*gotAfterDelete.IsDynamicRate, "group deletion must not clear the stored flag")
}
