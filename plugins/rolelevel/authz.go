package rolelevel

import (
	"context"
	"net/http"

	"github.com/shiroha-a/mk/plugin"
)

// requireAdmin gates every level-configuration route.
//
// A role's curve and per-level policies decide who may post and who may not, so only
// an administrator may change them. Hiding the editor in the frontend is not a
// boundary.
func requireAdmin(req plugin.Request) error {
	if req.UserID() == "" {
		return codedErrorf(http.StatusUnauthorized, CodeUnauthenticated, "ログインが必要です")
	}
	if !req.IsAdministrator() {
		return codedErrorf(http.StatusForbidden, CodeForbidden, "この操作は管理者だけが行えます")
	}
	return nil
}

// requireModerator gates the administrator read routes.
func requireModerator(req plugin.Request) error {
	if req.UserID() == "" {
		return codedErrorf(http.StatusUnauthorized, CodeUnauthenticated, "ログインが必要です")
	}
	if !req.IsModerator() {
		return codedErrorf(http.StatusForbidden, CodeForbidden, "この操作にはモデレーター以上の権限が必要です")
	}
	return nil
}

// AuthorizeXPChange applies the experience-mutation rule: an administrator may change
// any role's experience, and a moderator only the roles whose native
// canEditMembersByModerator is true.
//
// This function is only a direct preflight for `IsAdministrator` and
// `canEditMembersByModerator`; it does not claim full native assign/unassign parity.
// If an XP operation needs assignment creation, Task 8 invokes native Assign as
// persisted operation.ActorID. Core remains the final authority for indirect
// privilege grants.
//
// **管理者ロールは moderator にも閉ざす。** native の assign / unassign と同じ方針で
// (handler.go の requireCanEditRoleMembers)、`canEditMembersByModerator` の
// チェックボックス1つで管理者へ昇格できる穴を作らない (#3037)。
func (s *service) AuthorizeXPChange(req plugin.Request, roleID string) error {
	if req.UserID() == "" {
		return codedErrorf(http.StatusUnauthorized, CodeUnauthenticated, "ログインが必要です")
	}
	if !req.IsAdministrator() && !req.IsModerator() {
		return codedErrorf(http.StatusForbidden, CodeForbidden,
			"この操作にはモデレーター以上の権限が必要です")
	}
	native, err := s.nativeFor(req.UserID())
	if err != nil {
		return err
	}
	if req.IsAdministrator() {
		return nil
	}
	info, err := native.Show(req.Context(), roleID)
	if err != nil {
		return err
	}
	if info.IsAdministrator {
		return codedErrorf(http.StatusForbidden, CodeRoleNotAssignable,
			"管理者ロールの experience は管理者だけ変更できます")
	}
	if !info.CanEditMembersByModerator {
		return codedErrorf(http.StatusForbidden, CodeRoleNotAssignable,
			"このロールの experience は管理者だけ変更できます (canEditMembersByModerator が false です)")
	}
	return nil
}

// RequireConfigAdmin rejects anything but a manual role for level configuration.
//
// **level は manual role にしか付けない。** conditional role には assignment が無いので、
// XP を紐づけられる対象が存在しない。target を確認せずに保存すると、あとから
// policy 置換の先が壊れた状態で保存される。
func (s *service) RequireConfigAdmin(ctx context.Context, actorID, roleID string) error {
	native, err := s.nativeFor(actorID)
	if err != nil {
		return err
	}
	_, err = native.RequireManual(ctx, roleID)
	return err
}
