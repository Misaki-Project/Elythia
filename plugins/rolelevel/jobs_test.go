package rolelevel

import (
	"context"
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestJobsNativeTruncationFailsClosed(t *testing.T) {
	assignments := make([]assignment, 100)
	for i := range assignments {
		assignments[i] = mkAssignment("asg-"+strconv.Itoa(i), "u")
	}
	api := &stubAPI{roles: map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}}, assignments: map[string][]assignment{"r1": assignments}}
	svc, _ := newXPService(t, api)
	svc.cfg.AssignmentScanPages = 1
	seedXP(t, svc, "not-live", "r1", "u2", 10)

	if _, err := svc.OrphanReport(context.Background()); err == nil {
		t.Fatal("truncated native scan was accepted")
	}
	row, err := svc.store.ExperienceRowsForRole(context.Background(), "r1", 10, 0)
	if err != nil || len(row) != 1 || row[0].OrphanedAt != nil {
		t.Fatalf("native truncation mutated XP: rows=%+v err=%v", row, err)
	}
}

func TestJobsMissingNativeRoleIsAnEmptyLiveSet(t *testing.T) {
	api := &stubAPI{usersErr: http.StatusBadRequest}
	svc, _ := newXPService(t, api)
	seedXP(t, svc, "gone", "deleted", "u1", 10)

	if _, err := svc.OrphanReport(context.Background()); err != nil {
		t.Fatalf("missing native role should be treated as empty: %v", err)
	}
	rows, err := svc.store.ExperienceRowsForRole(context.Background(), "deleted", 10, 0)
	if err != nil || len(rows) != 1 || rows[0].OrphanedAt == nil {
		t.Fatalf("missing role did not mark its row orphan: rows=%+v err=%v", rows, err)
	}
}

func TestJobsEnumeratesAllExperiencePages(t *testing.T) {
	svc, _ := newXPService(t, &stubAPI{roles: map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}}})
	for i := 0; i < orphanPageSize+1; i++ {
		seedXP(t, svc, "asg-"+strconv.Itoa(i), "r1", "u", int64(i))
	}
	rows, err := svc.allExperienceRows(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != orphanPageSize+1 {
		t.Fatalf("rows = %d, want %d", len(rows), orphanPageSize+1)
	}
}

func TestJobsDeletedConfigRoleIsDiscoveredAndMarkersAreMaintained(t *testing.T) {
	svc, _ := newXPService(t, &stubAPI{roles: map[string]roleInfo{"deleted": {ID: "deleted", Target: "manual"}}, assignments: map[string][]assignment{"deleted": {mkAssignment("live", "u1")}}})
	cfg := DefaultConfig()
	cfg.RoleID = "deleted"
	saved, err := svc.store.UpsertConfig(context.Background(), cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if deleted, err := svc.store.DeleteConfig(context.Background(), "deleted", saved.Revision); err != nil || !deleted {
		t.Fatalf("delete config: %v %v", deleted, err)
	}
	seedXP(t, svc, "gone", "deleted", "u2", 3)
	seedXP(t, svc, "live", "deleted", "u1", 4)
	old := time.Now().UTC().Add(-48 * time.Hour)
	if _, err := svc.db.Exec(`UPDATE role_level_experience SET orphaned_at=$1 WHERE assignment_id='gone'`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.OrphanReport(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.store.ExperienceRowsForRole(context.Background(), "deleted", 10, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v, err=%v", rows, err)
	}
	for _, row := range rows {
		if row.AssignmentID == "gone" && row.OrphanedAt == nil {
			t.Fatal("missing orphan marker")
		}
		if row.AssignmentID == "live" && row.OrphanedAt != nil {
			t.Fatal("live assignment still marked orphan")
		}
	}
}

func TestJobsPruneAuditIsExactAndRollsBack(t *testing.T) {
	svc, _ := newXPService(t, &stubAPI{})
	old := time.Now().UTC().Add(-48 * time.Hour)
	seedXP(t, svc, "old-a", "r1", "u1", 11)
	seedXP(t, svc, "old-b", "r1", "u2", 22)
	if _, err := svc.db.Exec(`UPDATE role_level_experience SET orphaned_at=$1`, old); err != nil {
		t.Fatal(err)
	}
	n, err := svc.pruneRoleOrphans(context.Background(), "r1", []string{"old-b", "old-a"}, time.Now().UTC().Add(-24*time.Hour), reconcileTrigger{Source: reconcileSourceRoute, ActorID: "admin"})
	if err != nil || n != 2 {
		t.Fatalf("prune = %d, %v", n, err)
	}
	audit, err := svc.store.RecentAudit(context.Background(), "r1", "", 1)
	if err != nil || len(audit) != 1 || audit[0].Before["rows"] == nil || audit[0].ActorID != "admin" {
		t.Fatalf("audit = %+v, err=%v", audit, err)
	}
	wantRows := []any{
		map[string]any{"assignmentId": "old-a", "userId": "u1", "experience": float64(11)},
		map[string]any{"assignmentId": "old-b", "userId": "u2", "experience": float64(22)},
	}
	if !reflect.DeepEqual(audit[0].Before["rows"], wantRows) {
		t.Fatalf("audit before.rows = %#v, want exactly %#v", audit[0].Before["rows"], wantRows)
	}

	// A retry that has nothing eligible to delete must not leave an audit row.
	n, err = svc.pruneRoleOrphans(context.Background(), "r2", []string{"missing"}, time.Now().UTC().Add(-24*time.Hour), reconcileTrigger{Source: reconcileSourceCron})
	if err != nil || n != 0 {
		t.Fatalf("empty prune = %d, %v", n, err)
	}
	emptyAudit, err := svc.store.RecentAudit(context.Background(), "r2", "", 1)
	if err != nil || len(emptyAudit) != 0 {
		t.Fatalf("audit after deleted=0 prune = %+v, err=%v", emptyAudit, err)
	}
	if _, err := svc.db.Exec(`INSERT INTO role_level_experience (assignment_id, role_id, user_id, experience, orphaned_at) VALUES ('rollback', 'r1', 'u3', 33, $1)`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TABLE role_level_audit`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.pruneRoleOrphans(context.Background(), "r1", []string{"rollback"}, time.Now().UTC().Add(-24*time.Hour), reconcileTrigger{Source: reconcileSourceCron}); err == nil {
		t.Fatal("audit failure was hidden")
	}
	rows, err := svc.store.ExperienceRowsForRole(context.Background(), "r1", 10, 0)
	if err != nil || len(rows) != 1 || rows[0].AssignmentID != "rollback" {
		t.Fatalf("rollback lost XP: %+v, err=%v", rows, err)
	}
}

func TestJobsResumeOnlyNonTerminalOperations(t *testing.T) {
	api := &stubAPI{assignments: map[string][]assignment{
		"r": {
			mkAssignment("asg-pending", "u-pending"),
			mkAssignment("asg-assigning", "u-assigning"),
			mkAssignment("asg-applying", "u-applying"),
		},
	}}
	svc, _ := newXPService(t, api)
	ctx := context.Background()
	active := []operation{
		{IdempotencyKey: "resume-pending", ActorID: "admin1", UserID: "u-pending", RoleID: "r", Mode: string(ModeSet), Operand: 1, Status: string(StatusPending)},
		{IdempotencyKey: "resume-assigning", ActorID: "admin1", UserID: "u-assigning", RoleID: "r", Mode: string(ModeSet), Operand: 2, Status: string(StatusAssigning), AssignmentCreated: true},
		{IdempotencyKey: "resume-applying", ActorID: "admin1", UserID: "u-applying", RoleID: "r", Mode: string(ModeSet), Operand: 3, Status: string(StatusApplying), AssignmentID: "asg-applying", AssignmentCreated: true},
	}
	for _, op := range active {
		if _, err := svc.store.InsertOperation(ctx, op); err != nil {
			t.Fatal(err)
		}
	}
	desiredExp := int64(1)
	terminal := []operation{
		{IdempotencyKey: "resume-failed", ActorID: "admin1", UserID: "u-failed", RoleID: "r", Mode: string(ModeSet), Operand: 1, Status: string(StatusFailed), LastError: "terminal"},
		{IdempotencyKey: "resume-completed", ActorID: "admin1", UserID: "u-completed", RoleID: "r", Mode: string(ModeSet), Operand: 1, DesiredExp: &desiredExp, Status: string(StatusCompleted), AssignmentID: "asg-completed", AssignmentCreated: true},
	}
	// **終端行は遷移 API で作る。** `InsertOperation` は `last_error` と `assignment_id`
	// を書かないので、買読の保存値をそのまま照合すると「保存されていない」を
	// 「再開が壊した」と読んでしまう。
	for _, want := range terminal {
		pending := want
		pending.Status = string(StatusPending)
		if _, err := svc.store.InsertOperation(ctx, pending); err != nil {
			t.Fatal(err)
		}
		if err := svc.store.SetOperationStatus(ctx, svc.db, want.IdempotencyKey,
			want.Status, want.AssignmentID, want.DesiredExp, want.LastError); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.ResumePendingOperations(ctx); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"resume-pending", "resume-assigning", "resume-applying"} {
		op, found, err := svc.store.LoadOperation(ctx, key)
		if err != nil || !found || op.Status != string(StatusCompleted) {
			t.Fatalf("active operation %q = %+v, found=%t, err=%v", key, op, found, err)
		}
	}
	for _, want := range terminal {
		op, found, err := svc.store.LoadOperation(ctx, want.IdempotencyKey)
		if err != nil || !found || op.Status != want.Status || op.LastError != want.LastError || op.AssignmentID != want.AssignmentID || op.AssignmentCreated != want.AssignmentCreated {
			t.Fatalf("terminal operation %q changed: got %+v, want %+v (found=%t, err=%v)", want.IdempotencyKey, op, want, found, err)
		}
	}
	if op, _, err := svc.store.LoadOperation(ctx, "resume-assigning"); err != nil || !op.AssignmentCreated {
		t.Fatalf("persisted assignment_created state was not honored: %+v, err=%v", op, err)
	}
	ops, err := svc.store.ResumableOperations(ctx, 10)
	if err != nil || len(ops) != 0 {
		t.Fatalf("active operations remained resumable: %+v, err=%v", ops, err)
	}
	if _, err := svc.ChangeExp(ctx, xpRequest("resume-failed", "u-failed", "r", ModeSet, 1)); err == nil {
		t.Fatal("failed operation was resumed")
	}
	res, err := svc.ChangeExp(ctx, xpRequest("resume-completed", "u-completed", "r", ModeSet, 1))
	if err != nil || res.Status != StatusCompleted || res.Experience != 1 || !res.Resumed {
		t.Fatalf("completed operation changed or was not returned: %+v, %v", res, err)
	}
}
