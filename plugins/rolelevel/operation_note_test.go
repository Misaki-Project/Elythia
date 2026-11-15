package rolelevel

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestOperationNoteSurvivesReconciliationAndConflictingRetry(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(map[bool]string{false: "reconciliation", true: "concurrent-retry"}[concurrent], func(t *testing.T) {
			api := &stubAPI{
				roles:       map[string]roleInfo{"r1": {ID: "r1", Target: "manual"}},
				assignments: map[string][]assignment{"r1": {mkAssignment("asg1", "u1")}},
			}
			svc, _ := newXPService(t, api)
			ctx := context.Background()
			// operationの保存直後に停止した状態を再現する。
			original := strings.Repeat("報", 501)
			if _, err := svc.store.InsertOperation(ctx, operation{
				IdempotencyKey: "note-key", ActorID: "original-actor", UserID: "u1", RoleID: "r1",
				Mode: string(ModeAdd), Operand: 50, Status: string(StatusPending), Note: original,
			}); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			wg.Go(func() { errs <- svc.ResumePendingOperations(ctx) })
			if concurrent {
				wg.Go(func() {
					req := xpRequestAs("retry-actor", "note-key", "u1", "r1", ModeAdd, 50)
					req.Note = "different retry note"
					_, err := svc.ChangeExp(ctx, req)
					errs <- err
				})
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			op, found, err := svc.store.LoadOperation(ctx, "note-key")
			// 既存の500バイト上限処理は完全なUTF-8文字まで戻して省略記号を付ける。
			want := strings.Repeat("報", 166) + "..."
			if err != nil || !found || op.Note != want || op.DesiredExp == nil || *op.DesiredExp != 50 {
				t.Fatalf("operation = %+v, found=%t, err=%v", op, found, err)
			}
			entries, err := svc.store.RecentAudit(ctx, "r1", "u1", 10)
			if err != nil || len(entries) != 1 || entries[0].Note != want || entries[0].ActorID != "original-actor" {
				t.Fatalf("audit = %+v, err=%v", entries, err)
			}
		})
	}
}

func TestOperationNoteMigrationPreservesExistingRows(t *testing.T) {
	db := testDBRequired(t)
	h := newHarness(t, db, nil)
	ctx := h.Context()
	if err := ctx.Storage().Migrate(t.Context(), migrations[:6]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO role_level_operation
		(idempotency_key, actor_id, user_id, role_id, mode, operand, status)
		VALUES ('legacy', 'actor', 'user', 'role', 'add', 50, 'pending')`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := ctx.Storage().Migrate(t.Context(), migrations); err != nil {
			t.Fatal(err)
		}
	}
	op, found, err := (&store{db: db}).LoadOperation(t.Context(), "legacy")
	if err != nil || !found || op.Note != "" || op.Operand != 50 || op.ActorID != "actor" {
		t.Fatalf("legacy operation = %+v, found=%t, err=%v", op, found, err)
	}
}
