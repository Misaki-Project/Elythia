package rolelevel

import (
	"context"
	"testing"

	"github.com/shiroha-a/mk/plugin"
)

func TestEffectivePoliciesRegistration(t *testing.T) {
	if Plugin.EffectivePolicies == nil {
		t.Fatal("EffectivePolicies is nil")
	}
}

func TestResolveReplacementsDBAndHostValidation(t *testing.T) {
	db := testDBRequired(t)
	h := newHarness(t, db, nil)
	h.Routes(Plugin)
	svc, err := newService(h.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mk := func(role string, key string, value any) Config {
		return Config{RoleID: role, BaseLevel: 1, UpdatedBy: "test", ExperienceCurve: []Curve{{Type: CurveConst, LevelUps: 1, Base: 10}}, PolicyRanges: []PolicyRange{{Type: RangeConst, Key: key, Start: 1, End: 2, Value: value}, {Type: RangeBase, Start: 2, End: 3}}}
	}
	if _, err = svc.store.UpsertConfig(ctx, mk("r1", "canDeleteAccount", true), 0); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.store.UpsertConfig(ctx, mk("r2", "antennaLimit", float64(4)), 0); err != nil {
		t.Fatal(err)
	}
	reg := h.EffectivePolicies(Plugin)
	got, err := reg.Resolve(ctx, plugin.EffectivePolicyRequest{RoleIDs: []string{"r1", "r2"}, ActiveAssignments: []plugin.ActiveRoleAssignment{{RoleID: "r1", AssignmentID: "a1"}, {RoleID: "r2", AssignmentID: "a2"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "canDeleteAccount" || got[1].Key != "antennaLimit" {
		t.Fatalf("unexpected contributions: %#v", got)
	}
	for _, c := range got {
		if c.Priority != 0 || c.Order != 0 || c.ReplaceRoleID == "" {
			t.Fatalf("bad replacement: %#v", c)
		}
	}
	svc.store.SetExperience(ctx, svc.store.db, experienceRow{AssignmentID: "a2", RoleID: "r2", UserID: "u", Experience: 10})
	got, err = reg.Resolve(ctx, plugin.EffectivePolicyRequest{ActiveAssignments: []plugin.ActiveRoleAssignment{{RoleID: "r2", AssignmentID: "a2"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("base range must preserve native: %#v", got)
	}
}

func TestResolveReplacementsRejectsMalformedPersistedConfig(t *testing.T) {
	db := testDBRequired(t)
	h := newHarness(t, db, nil)
	h.Routes(Plugin)
	svc, err := newService(h.Context())
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.RoleID = "bad"
	cfg.UpdatedBy = "test"
	cfg.PolicyRanges = []PolicyRange{{Type: RangeConst, Key: "canDeleteAccount", Start: 1, End: 99, Value: true}}
	if _, err := svc.store.UpsertConfig(context.Background(), cfg, 0); err != nil {
		t.Fatal(err)
	}
	_, err = svc.resolveReplacements(context.Background(), plugin.EffectivePolicyRequest{ActiveAssignments: []plugin.ActiveRoleAssignment{{RoleID: "bad", AssignmentID: "missing"}}})
	if err == nil {
		t.Fatal("expected malformed persisted config error")
	}
}

func TestResolveReplacementsMultiplierMissingConfigAndStorageFailure(t *testing.T) {
	db := testDBRequired(t)
	h := newHarness(t, db, nil)
	h.Routes(Plugin)
	svc, err := newService(h.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cfg := Config{RoleID: "multi", BaseLevel: 1, UpdatedBy: "test", ExperienceCurve: []Curve{{Type: CurveConst, LevelUps: 2, Base: 10}}, PolicyRanges: []PolicyRange{{Type: RangeMultiplier, Key: "antennaLimit", Start: 1, End: 4, Base: 2, Additional: 1}}}
	if _, err := svc.store.UpsertConfig(ctx, cfg, 0); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.SetExperience(ctx, svc.store.db, experienceRow{AssignmentID: "ma", RoleID: "multi", UserID: "u", Experience: 10}); err != nil {
		t.Fatal(err)
	}
	req := plugin.EffectivePolicyRequest{ActiveAssignments: []plugin.ActiveRoleAssignment{{RoleID: "multi", AssignmentID: "ma"}, {RoleID: "missing", AssignmentID: "none"}}}
	got, err := svc.resolveReplacements(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "antennaLimit" || got[0].Value != float64(3) {
		t.Fatalf("unexpected multiplier/missing output: %#v", got)
	}
	req.ActiveAssignments = append(req.ActiveAssignments, plugin.ActiveRoleAssignment{RoleID: "multi", AssignmentID: "ma"})
	got, err = svc.resolveReplacements(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("duplicate pair was not removed: %#v", got)
	}
	db.Close()
	if _, err = svc.resolveReplacements(ctx, plugin.EffectivePolicyRequest{ActiveAssignments: []plugin.ActiveRoleAssignment{{RoleID: "multi", AssignmentID: "ma"}}}); err == nil {
		t.Fatal("expected storage failure")
	}
}
