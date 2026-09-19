//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// userGroupRateSyncStub 记录 SyncUserGroupRates 的入参，用于校验管理端用户倍率写入。
type userGroupRateSyncStub struct {
	UserGroupRateRepository

	syncedUserID int64
	syncedRates  map[int64]*UserGroupRate
	syncErr      error
	syncCalls    int
}

func (s *userGroupRateSyncStub) SyncUserGroupRates(_ context.Context, userID int64, rates map[int64]*UserGroupRate) error {
	s.syncCalls++
	s.syncedUserID = userID
	s.syncedRates = rates
	return s.syncErr
}

func newUpdateUserServiceForRateTest(rateRepo UserGroupRateRepository) *adminServiceImpl {
	return &adminServiceImpl{
		userRepo: &userRepoStub{
			usersByID: map[int64]*User{
				7: {ID: 7, Email: "rate@test.com", Username: "rate", Role: RoleUser, Status: StatusActive, Concurrency: 1},
			},
		},
		userGroupRateRepo: rateRepo,
	}
}

func TestUpdateUser_GroupRates_NormalizesExpressionAndKeepsRPM(t *testing.T) {
	repo := &userGroupRateSyncStub{}
	svc := newUpdateUserServiceForRateTest(repo)

	exprRate := &UserGroupRate{RateMultiplier: 1.2, RateMultiplierExpr: "  $up * 1.05  "}
	_, err := svc.UpdateUser(context.Background(), 7, &UpdateUserInput{
		GroupRates: map[int64]*UserGroupRate{11: exprRate},
	})
	require.NoError(t, err)
	require.Equal(t, 1, repo.syncCalls)
	require.Equal(t, int64(7), repo.syncedUserID)
	// 表达式归一化后再落库，静态数值保留为回退值。
	require.Equal(t, "$up * 1.05", repo.syncedRates[11].RateMultiplierExpr)
	require.Equal(t, 1.2, repo.syncedRates[11].RateMultiplier)
}

func TestUpdateUser_GroupRates_ZeroIsAValidOverride(t *testing.T) {
	repo := &userGroupRateSyncStub{}
	svc := newUpdateUserServiceForRateTest(repo)

	_, err := svc.UpdateUser(context.Background(), 7, &UpdateUserInput{
		GroupRates: map[int64]*UserGroupRate{11: {RateMultiplier: 0}},
	})
	require.NoError(t, err)
	require.NotNil(t, repo.syncedRates[11])
	require.Zero(t, repo.syncedRates[11].RateMultiplier)
	require.False(t, repo.syncedRates[11].IsDynamic())
}

func TestUpdateUser_GroupRates_NilClearsOverride(t *testing.T) {
	repo := &userGroupRateSyncStub{}
	svc := newUpdateUserServiceForRateTest(repo)

	_, err := svc.UpdateUser(context.Background(), 7, &UpdateUserInput{
		GroupRates: map[int64]*UserGroupRate{11: nil},
	})
	require.NoError(t, err)
	require.Contains(t, repo.syncedRates, int64(11))
	require.Nil(t, repo.syncedRates[11])
}

func TestUpdateUser_GroupRates_RejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		rate *UserGroupRate
	}{
		{name: "negative multiplier", rate: &UserGroupRate{RateMultiplier: -0.5}},
		{name: "invalid expression", rate: &UserGroupRate{RateMultiplier: 1, RateMultiplierExpr: "foo($up)"}},
		{name: "boolean expression", rate: &UserGroupRate{RateMultiplier: 1, RateMultiplierExpr: "$up > 1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &userGroupRateSyncStub{}
			svc := newUpdateUserServiceForRateTest(repo)

			_, err := svc.UpdateUser(context.Background(), 7, &UpdateUserInput{
				GroupRates: map[int64]*UserGroupRate{11: tc.rate},
			})
			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
			require.Zero(t, repo.syncCalls, "校验失败不得落库")
		})
	}
}

func TestUpdateUser_GroupRates_RepoErrorPropagates(t *testing.T) {
	repo := &userGroupRateSyncStub{syncErr: errors.New("sync failed")}
	svc := newUpdateUserServiceForRateTest(repo)

	_, err := svc.UpdateUser(context.Background(), 7, &UpdateUserInput{
		GroupRates: map[int64]*UserGroupRate{11: {RateMultiplier: 1}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "sync failed")
}

func TestGetUserGroupRates_ExposesNoRawExpression(t *testing.T) {
	repo := &userGroupRateReadStub{rates: map[int64]UserGroupRate{
		11: {RateMultiplier: 1.5},
		12: {RateMultiplier: 1, RateMultiplierExpr: "$up * 1.05"},
		13: {RateMultiplier: 0},
	}}
	svc := &APIKeyService{userGroupRateRepo: repo}

	out, err := svc.GetUserGroupRates(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, UserGroupRateDisplay{RateMultiplier: 1.5}, out[11])
	require.Equal(t, UserGroupRateDisplay{RateMultiplier: 1, IsDynamic: true}, out[12])
	require.Equal(t, UserGroupRateDisplay{RateMultiplier: 0}, out[13])
}

func TestUserGroupRate_ValidateAndEvaluate(t *testing.T) {
	require.NoError(t, UserGroupRate{RateMultiplier: 0}.Validate())
	require.Error(t, UserGroupRate{RateMultiplier: -1}.Validate())
	require.Error(t, UserGroupRate{RateMultiplier: 1, RateMultiplierExpr: "$up > 1"}.Validate())

	static := UserGroupRate{RateMultiplier: 1.4}
	got, err := static.Evaluate(3)
	require.NoError(t, err)
	require.Equal(t, 1.4, got)

	dyn := UserGroupRate{RateMultiplier: 1, RateMultiplierExpr: "$up * 2"}
	got, err = dyn.Evaluate(3)
	require.NoError(t, err)
	require.Equal(t, 6.0, got)

	broken := UserGroupRate{RateMultiplier: 1, RateMultiplierExpr: "$up / 0"}
	_, err = broken.Evaluate(3)
	require.Error(t, err)
}

// userGroupRateReadStub 只实现读取路径。
type userGroupRateReadStub struct {
	UserGroupRateRepository

	rates map[int64]UserGroupRate
	err   error
}

func (s *userGroupRateReadStub) GetByUserID(_ context.Context, _ int64) (map[int64]UserGroupRate, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.rates, nil
}
