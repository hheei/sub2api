package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// profitControlJSONFields 是分组利润控制的三个 JSON 字段。它们与同响应中的
// rate_multiplier 相乘即可反推出运营方的上游采购成本上限，属于内部经营信息，
// 只能出现在管理员 DTO 中。
var profitControlJSONFields = []string{
	"profit_control_enabled",
	"profit_min_margin",
	"profit_safety_buffer",
}

func profitControlServiceGroup() *service.Group {
	return &service.Group{
		ID:                   7,
		Name:                 "profit-gated",
		Platform:             service.PlatformAnthropic,
		RateMultiplier:       2.0,
		RateMultiplierExpr:   "$up * 1.05",
		Status:               service.StatusActive,
		ProfitControlEnabled: true,
		ProfitMinMargin:      0.3,
		ProfitSafetyBuffer:   0.05,
	}
}

func marshalToMap(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// TestGroupFromServiceOmitsProfitControl 钉死普通用户侧的分组 DTO 不泄露利润控制配置。
func TestGroupFromServiceOmitsProfitControl(t *testing.T) {
	for name, got := range map[string]any{
		"GroupFromService":        GroupFromService(profitControlServiceGroup()),
		"GroupFromServiceShallow": GroupFromServiceShallow(profitControlServiceGroup()),
	} {
		fields := marshalToMap(t, got)
		for _, f := range profitControlJSONFields {
			if _, ok := fields[f]; ok {
				t.Errorf("%s: 普通用户 DTO 不得包含 %q", name, f)
			}
		}
		if _, ok := fields["rate_multiplier"]; !ok {
			t.Errorf("%s: 应仍返回 rate_multiplier", name)
		}
		if _, ok := fields["rate_multiplier_expr"]; ok {
			t.Errorf("%s: 普通用户 DTO 不得包含 rate_multiplier_expr", name)
		}
	}
}

// TestGroupFromServiceAdminIncludesProfitControl 钉死管理端仍能读写利润控制配置。
func TestGroupFromServiceAdminIncludesProfitControl(t *testing.T) {
	admin := GroupFromServiceAdmin(profitControlServiceGroup())
	if admin.ProfitControlEnabled != true || admin.ProfitMinMargin != 0.3 || admin.ProfitSafetyBuffer != 0.05 {
		t.Fatalf("管理员 DTO 未透传利润控制配置: %+v", admin)
	}
	if admin.RateMultiplierExpr != "$up * 1.05" {
		t.Fatalf("管理员 DTO 未透传 rate_multiplier_expr: %+v", admin)
	}
	fields := marshalToMap(t, admin)
	for _, f := range profitControlJSONFields {
		if _, ok := fields[f]; !ok {
			t.Errorf("管理员 DTO 应包含 %q", f)
		}
	}
}

func TestGroupAndUsageLogDynamicRateFlags(t *testing.T) {
	dynamicGroup := profitControlServiceGroup()
	dtoGroup := GroupFromService(dynamicGroup)
	if !dtoGroup.IsDynamic {
		t.Fatal("Group with rate_multiplier_expr should have IsDynamic=true")
	}

	staticGroup := &service.Group{
		ID:             8,
		Name:           "static-group",
		RateMultiplier: 1.5,
	}
	dtoStaticGroup := GroupFromService(staticGroup)
	if dtoStaticGroup.IsDynamic {
		t.Fatal("Group without rate_multiplier_expr should have IsDynamic=false")
	}

}

// TestUsageLogDynamicRateComesFromStoredFlag 钉死 is_dynamic_rate 是行内快照：
// 只读 stored 字段，历史行（nil）保持未知，绝不从当前（可变）分组配置反推。
func TestUsageLogDynamicRateComesFromStoredFlag(t *testing.T) {
	flagTrue := true
	flagFalse := false

	// 分组是动态的，但落库标记说这次不是 —— 必须以落库为准。
	storedFalse := &service.UsageLog{
		ID:             100,
		RateMultiplier: 0.63,
		IsDynamicRate:  &flagFalse,
		Group:          profitControlServiceGroup(),
	}
	dtoStoredFalse := UsageLogFromService(storedFalse)
	if dtoStoredFalse.IsDynamicRate == nil || *dtoStoredFalse.IsDynamicRate {
		t.Fatal("stored false must win over a dynamic group")
	}
	if dtoStoredFalse.RateMultiplier != 0.63 {
		t.Fatalf("UsageLog should preserve actual rate multiplier, got %v", dtoStoredFalse.RateMultiplier)
	}

	// 分组是静态的（甚至分组已被删除），但落库标记说这次是动态的。
	storedTrue := &service.UsageLog{
		ID:             101,
		RateMultiplier: 1.5,
		IsDynamicRate:  &flagTrue,
		Group:          &service.Group{ID: 8, Name: "static-group", RateMultiplier: 1.5},
	}
	dtoStoredTrue := UsageLogFromService(storedTrue)
	if dtoStoredTrue.IsDynamicRate == nil || !*dtoStoredTrue.IsDynamicRate {
		t.Fatal("stored true must win over a static group")
	}

	deletedGroup := &service.UsageLog{
		ID:             102,
		RateMultiplier: 1.5,
		IsDynamicRate:  &flagTrue,
	}
	if dtoDeleted := UsageLogFromService(deletedGroup); dtoDeleted.IsDynamicRate == nil || !*dtoDeleted.IsDynamicRate {
		t.Fatal("a deleted group must not clear the stored flag")
	}

	// 历史行：无 stored 值 → 未知，且不得由当前动态分组推断为 true。
	legacy := &service.UsageLog{
		ID:             103,
		RateMultiplier: 0.63,
		Group:          profitControlServiceGroup(),
	}
	dtoLegacy := UsageLogFromService(legacy)
	if dtoLegacy.IsDynamicRate != nil {
		t.Fatalf("historical row must stay unknown, got %v", *dtoLegacy.IsDynamicRate)
	}
	if raw := marshalToMap(t, dtoLegacy); raw["is_dynamic_rate"] != nil {
		t.Fatalf("unknown is_dynamic_rate must be omitted from JSON, got %v", raw["is_dynamic_rate"])
	}

	// false 必须显式出现在 JSON 中，不能因 omitempty 丢失。
	if raw := marshalToMap(t, dtoStoredFalse); raw["is_dynamic_rate"] != false {
		t.Fatalf("explicit false must be serialized, got %v", raw["is_dynamic_rate"])
	}

	adminDynamic := UsageLogFromServiceAdmin(storedTrue)
	if adminDynamic.IsDynamicRate == nil || !*adminDynamic.IsDynamicRate {
		t.Fatal("admin DTO must expose the stored flag")
	}
	adminLegacy := UsageLogFromServiceAdmin(legacy)
	if adminLegacy.IsDynamicRate != nil {
		t.Fatal("admin DTO must keep historical rows unknown")
	}
}
