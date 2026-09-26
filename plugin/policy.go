package plugin

import (
	"context"
	"fmt"
)

// ActiveRoleAssignment is one active manual role assignment paired with the
// role it grants.
//
// **conditional role は含まれない。** 条件つきロールには assignment row が
// 無いので、plugin が見えるのは手動で割り当てられた active なロールだけになる。
// [EffectivePolicyRequest.RoleIDs] は conditional を含むので、plugin は
// 「何の権限があるか」と「どの assignment に紐づくか」を別々に読む。
type ActiveRoleAssignment struct {
	// RoleID is the role the assignment grants. RoleIDs に必ず含まれる。
	RoleID string
	// AssignmentID is the native `role_assignment.id`。
	//
	// **不透明な ID として扱うこと。** plugin はこれを Plugin storage の行と
	// 対応させるだけで、内容を解釈しない。unassign → re-assign で別 ID に
	// なるので、assignment に紐づく状態は復活しない。
	AssignmentID string
}

// EffectivePolicyRequest is the input to an effective policy resolver.
type EffectivePolicyRequest struct {
	// UserID is empty when the user is anonymous.
	UserID string
	// RoleIDs are active role IDs, sorted without duplicates. Anonymous
	// requests receive a non-nil empty slice.
	RoleIDs []string
	// ActiveAssignments are the user's active manual role assignments, sorted
	// by RoleID. Each role appears at most once.
	//
	// - expired / deleted assignments are excluded
	// - conditional roles are never included (they have no assignment)
	// - anonymous requests receive a non-nil empty slice
	//
	// **RoleIDs を置き換えない。** 既存providerはこのsliceだけを見ている。
	// 追加contributionだけ返す実装には RoleIDs があれば十分。
	ActiveAssignments []ActiveRoleAssignment
}

// EffectivePolicyContribution is one policy key's contribution.
type EffectivePolicyContribution struct {
	Key        string
	Priority   int
	UseDefault bool
	Value      any
	// Order distinguishes multiple contributions for the same key and controls
	// their deterministic provider-local order. Each (Key, Order) pair must be
	// unique within one resolver result.
	Order int
}

// EffectivePolicyResolver computes effective policy contributions for a user.
type EffectivePolicyResolver func(context.Context, EffectivePolicyRequest) ([]EffectivePolicyContribution, error)

// EffectivePolicyRegistration declares an effective-policy provider.
type EffectivePolicyRegistration struct {
	Keys    []string
	Resolve EffectivePolicyResolver
}

// Validate reports whether the registration is usable.
func (r EffectivePolicyRegistration) Validate() error {
	if r.Resolve == nil {
		return fmt.Errorf("plugin: EffectivePolicies の Resolve が nil です")
	}
	if len(r.Keys) == 0 {
		return fmt.Errorf("plugin: EffectivePolicies の Keys が空です")
	}
	seen := make(map[string]bool, len(r.Keys))
	for _, key := range r.Keys {
		if key == "" {
			return fmt.Errorf("plugin: EffectivePolicies の Keys に空のキーが含まれています")
		}
		if seen[key] {
			return fmt.Errorf("plugin: EffectivePolicies の Keys に重複があります (%q)", key)
		}
		seen[key] = true
	}
	return nil
}

// EffectivePolicyInvalidator drops cached policy inputs and successful
// provider output after committed state changes.
type EffectivePolicyInvalidator interface {
	InvalidateUser(context.Context, string) error
	// InvalidateRole is intentionally broader than one role because conditional
	// role holders cannot be enumerated from assignments.
	InvalidateRole(context.Context, string) error
}
