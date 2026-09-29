package rolelevel

import (
	"context"

	"github.com/shiroha-a/mk/plugin"
)

func effectivePolicies(pctx plugin.Context, inv plugin.EffectivePolicyInvalidator) (plugin.EffectivePolicyRegistration, error) {
	invalidatorHandle.set(inv)
	svc, err := newService(pctx)
	if err != nil {
		return plugin.EffectivePolicyRegistration{}, err
	}
	return plugin.EffectivePolicyRegistration{Keys: defaultCatalog.Keys(), Resolve: svc.resolveReplacements}, nil
}

func (s *service) resolveReplacements(ctx context.Context, req plugin.EffectivePolicyRequest) ([]plugin.EffectivePolicyContribution, error) {
	if len(req.ActiveAssignments) == 0 {
		return []plugin.EffectivePolicyContribution{}, nil
	}
	roleIDs := make([]string, 0, len(req.ActiveAssignments))
	assignmentIDs := make([]string, 0, len(req.ActiveAssignments))
	for _, a := range req.ActiveAssignments {
		roleIDs = append(roleIDs, a.RoleID)
		assignmentIDs = append(assignmentIDs, a.AssignmentID)
	}
	configs, err := s.store.LoadConfigsForRoles(ctx, roleIDs)
	if err != nil {
		return nil, s.storageError(ctx, "level 設定の読み込み", err)
	}
	exps, err := s.store.ExperienceForAssignments(ctx, assignmentIDs)
	if err != nil {
		return nil, s.storageError(ctx, "experience の読み込み", err)
	}
	out := make([]plugin.EffectivePolicyContribution, 0)
	seen := make(map[string]struct{})
	for _, a := range req.ActiveAssignments {
		cfg, ok := configs[a.RoleID]
		if !ok {
			continue
		}
		if err := cfg.Validate(defaultCatalog); err != nil {
			return nil, statusError(err)
		}
		level, err := cfg.Experience(exps[a.AssignmentID])
		if err != nil {
			return nil, statusError(err)
		}
		for _, r := range rangesForStage(cfg.PolicyRanges, level.ProgressionStage) {
			value, err := rangeValue(r, level.ProgressionStage)
			if err != nil {
				return nil, statusError(err)
			}
			if value == nil || r.Key == "" {
				continue
			}
			dedupe := r.Key + "\x00" + a.RoleID
			if _, exists := seen[dedupe]; exists {
				continue
			}
			seen[dedupe] = struct{}{}
			out = append(out, plugin.EffectivePolicyContribution{Key: r.Key, Value: value, Priority: 0, Order: 0, ReplaceRoleID: a.RoleID})
		}
	}
	return out, nil
}
