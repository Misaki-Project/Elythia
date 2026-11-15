package rolelevel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/elythia-network/elythia/plugin"
)

// nativePageSize is the per-page limit of admin/roles/users.
//
// 上限は 100（pagination.ResolveLimit）。これより大きい値は mk-go 側で拒否されるため、ページングでは常にこの上限値を使用する。
const nativePageSize = 100

// nativeRole は Task 1 の plugin.go で宣言済みの adapter state であり、ここでは
// nativePageSize と methods だけを追加する。role / assignment の正本は native API
// だけで、core table は直接読まない。
// apiFailure converts a plugin.APIError into a coded status error.
//
// **素の error を返さない。** host は 500 に丸めて「内部エラー」しか返さないので、
// frontend が「native 側の問題」と「plugin の問題」を区別できない。
func (n *nativeRole) apiFailure(ctx context.Context, what string, err error) error {
	var apiErr *plugin.APIError
	if errors.As(err, &apiErr) {
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if apiErr.Status == http.StatusBadRequest &&
			json.Unmarshal(apiErr.Body, &body) == nil && body.Error.Code == "NO_SUCH_ROLE" {
			return codedErrorf(http.StatusNotFound, CodeNativeRoleNotFound,
				"%s: その role は存在しません", what)
		}
		return codedErrorf(http.StatusBadGateway, CodeNativeAPIFailed,
			"%s に失敗しました (native status %d)", what, apiErr.Status)
	}
	return codedErrorf(http.StatusBadGateway, CodeNativeAPIFailed, "%s に失敗しました", what)
}

// Show reads one role.
func (n *nativeRole) Show(ctx context.Context, roleID string) (roleInfo, error) {
	raw, err := n.caller.Call(ctx, "admin/roles/show", map[string]any{"roleId": roleID})
	if err != nil {
		return roleInfo{}, n.apiFailure(ctx, "admin/roles/show", err)
	}
	var info roleInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return roleInfo{}, codedErrorf(http.StatusBadGateway, CodeNativeAPIFailed,
			"admin/roles/show の応答を読めません")
	}
	if info.ID != roleID {
		// **id の完全一致を見る。** 空の role を作ると、以降の権限判定が全部 false に
		// なって「誰も触れない」状態になる。別の role を返されてきたときも、要求して
		// いない role を読み違えて後段へ渡すので、同じ 502 にまとめる。
		return roleInfo{}, codedErrorf(http.StatusBadGateway, CodeNativeAPIFailed,
			"admin/roles/show が id %q を返しましたが、要求した role id は %q です", info.ID, roleID)
	}
	return info, nil
}

// RequireManual reads one role and rejects anything but a manual role.
//
// **conditional role は assignment を持たない。** level は assignment に紐づくので、
// conditional に XP を持たせられると「どの assignment の XP か」が決まらない。
func (n *nativeRole) RequireManual(ctx context.Context, roleID string) (roleInfo, error) {
	info, err := n.Show(ctx, roleID)
	if err != nil {
		return roleInfo{}, err
	}
	if info.Target != "manual" {
		return roleInfo{}, codedErrorf(http.StatusBadRequest, CodeRoleNotManual,
			"level は manual role だけに設定できます (この role の target は %s です)", info.Target)
	}
	return info, nil
}

// Assigned reports whether the user currently holds the role.
//
// **admin/roles/assignment-show を 1 回だけ呼ぶ。** member 一覧を走査するより遥かに
// 安いので、XP 0 の user の membership を確かめる経路ではこちらを使う。
func (n *nativeRole) Assigned(ctx context.Context, roleID, userID string) (bool, error) {
	raw, err := n.caller.Call(ctx, "admin/roles/assignment-show",
		map[string]any{"roleId": roleID, "userId": userID})
	if err != nil {
		return false, n.apiFailure(ctx, "admin/roles/assignment-show", err)
	}
	var res struct {
		// **bool ではなく *bool。** 応答に assigned が無い / null なケースと、
		// 明示的に false なケースを区別しないと「未付与」だと誤読する。
		Assigned *bool `json:"assigned"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return false, codedErrorf(http.StatusBadGateway, CodeNativeAPIFailed,
			"admin/roles/assignment-show の応答を読めません")
	}
	if res.Assigned == nil {
		return false, codedErrorf(http.StatusBadGateway, CodeNativeAPIFailed,
			"admin/roles/assignment-show が assigned を返しませんでした")
	}
	return *res.Assigned, nil
}

// ListAssignments returns one page-bounded scan of a role's assignments.
//
// truncated is true when the scan hit the page cap, so the caller can tell
// "this role has no more members" from "we stopped looking".
func (n *nativeRole) ListAssignments(ctx context.Context, roleID string) ([]assignment, bool, error) {
	out := []assignment{}
	var untilID string
	for page := 0; page < n.pages; page++ {
		params := map[string]any{"roleId": roleID, "limit": nativePageSize}
		if untilID != "" {
			params["untilId"] = untilID
		}
		raw, err := n.caller.Call(ctx, "admin/roles/users", params)
		if err != nil {
			return nil, false, n.apiFailure(ctx, "admin/roles/users", err)
		}
		var batch []assignment
		if err := json.Unmarshal(raw, &batch); err != nil {
			return nil, false, codedErrorf(http.StatusBadGateway, CodeNativeAPIFailed,
				"admin/roles/users の応答を読めません")
		}
		if len(batch) == 0 {
			return out, false, nil
		}
		// 黙って limit まで切ると捨てた行にいる member を見失うため、API 契約違反として 502 にする。
		if len(batch) > nativePageSize {
			return nil, false, codedErrorf(http.StatusBadGateway, CodeNativeAPIFailed,
				"admin/roles/users が上限 (%d) を超える %d 件を返しました",
				nativePageSize, len(batch))
		}
		// **id の無い行は cursor として使えない。** 空の untilId を返すと次の page が
		// 1ページ目と重複して、取得済みの行が積み上がる。
		for _, row := range batch {
			if row.ID == "" || row.UserID() == "" {
				return nil, false, codedErrorf(http.StatusBadGateway, CodeNativeAPIFailed,
					"admin/roles/users が assignment id または user id の無い行を返しました")
			}
		}
		out = append(out, batch...)
		// aidx の id は降順で並ぶので、最後 (最も古い) 1 件を次の untilId にする。
		untilID = batch[len(batch)-1].ID
		if len(batch) < nativePageSize {
			return out, false, nil
		}
		// 最終許可 page がちょうど満杯なら、追加 probe は行わず conservative に
		// truncated とする。取得済み rows 内の target は FindAssignment で成功させる。
		if page == n.pages-1 {
			return out, true, nil
		}
	}
	return out, false, nil
}

// FindAssignment resolves the native assignment id of one (role, user) pair.
//
// found is false only when the scan finished without an upper limit. A target in
// the fetched pages is returned even when the scan is truncated; otherwise a
// truncated scan returns RoleLevelAssignmentScanExhausted because mistaking "we
// stopped looking" for "not assigned" could create XP for an existing member.
func (n *nativeRole) FindAssignment(ctx context.Context, roleID, userID string) (string, bool, error) {
	all, truncated, err := n.ListAssignments(ctx, roleID)
	if err != nil {
		return "", false, err
	}
	for _, a := range all {
		if a.UserID() == userID {
			return a.ID, true, nil
		}
	}
	if truncated {
		return "", false, codedErrorf(http.StatusConflict, CodeAssignmentScanExhausted,
			"この role の member が多すぎて (上限 %d ページ) assignment を確認できません。assignmentScanPages を上げるか、XP を個別に変更してください",
			n.pages)
	}
	return "", false, nil
}

// Assign grants the role. A conflict (already assigned) counts as success so the
// caller can retry without special-casing it.
func (n *nativeRole) Assign(ctx context.Context, userID, roleID string) error {
	_, err := n.caller.Call(ctx, "admin/roles/assign",
		map[string]any{"userId": userID, "roleId": roleID})
	if err == nil {
		return nil
	}
	var apiErr *plugin.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
		return nil
	}
	return n.apiFailure(ctx, "admin/roles/assign", err)
}

// nativeUsersShowBatchSize is users/show's production input limit.
const nativeUsersShowBatchSize = 100

// Usernames resolves display names in batches of at most 100 IDs.
//
// A missing user is simply absent from the map: suspended and deleted users are
// exactly the ones mk-go stops returning, and a list that still shows them is worse
// than a short list.
func (n *nativeRole) Usernames(ctx context.Context, userIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(userIDs) == 0 {
		return out, nil
	}
	for start := 0; start < len(userIDs); start += nativeUsersShowBatchSize {
		end := start + nativeUsersShowBatchSize
		if end > len(userIDs) {
			end = len(userIDs)
		}
		raw, err := n.caller.Call(ctx, "users/show", map[string]any{"userIds": userIDs[start:end]})
		if err != nil {
			return nil, n.apiFailure(ctx, "users/show", err)
		}
		var users []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		}
		if err := json.Unmarshal(raw, &users); err != nil {
			return nil, codedErrorf(http.StatusBadGateway, CodeNativeAPIFailed,
				"users/show の応答を読めません")
		}
		for _, u := range users {
			out[u.ID] = u.Username
		}
	}
	return out, nil
}
