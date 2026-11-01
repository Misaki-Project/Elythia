package role_test

import (
	"context"
	"testing"
	"time"

	"github.com/elythia-network/elythia/internal/core/role"
	"github.com/elythia-network/elythia/internal/misc/id"
	"github.com/elythia-network/elythia/internal/model"
	"github.com/elythia-network/elythia/internal/repository"
	"github.com/elythia-network/elythia/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestStrictUserRolesRejectsPartialConditionalResolution(t *testing.T) {
	db, err := testutil.OpenTestDBSchema("strict_roles")
	require.NoError(t, err, "isolated PostgreSQL is required")
	testutil.ApplyMigrations(db)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	const viewer = "strict-role-viewer"
	roleIDs := []string{"strict-manual", "strict-conditional", "strict-expired"}
	cleanup := func() {
		require.NoError(t, db.Where(`"userId" = ?`, viewer).Delete(&model.RoleAssignment{}).Error)
		require.NoError(t, db.Where("id IN ?", roleIDs).Delete(&model.Role{}).Error)
		require.NoError(t, db.Where("id = ?", viewer).Delete(&model.User{}).Error)
	}
	cleanup()
	t.Cleanup(cleanup)
	require.NoError(t, repository.NewUserRepository(db).Create(&model.User{ID: viewer, Username: viewer, UsernameLower: viewer, IsBot: true}))
	for _, roleID := range roleIDs {
		item := &model.Role{ID: roleID, Name: roleID, Target: model.RoleTargetManual}
		if roleID == roleIDs[1] {
			item.Target = model.RoleTargetConditional
			item.CondFormula = datatypes.JSON(`{"type":"isBot"}`)
		}
		require.NoError(t, repository.NewRoleRepository(db).Create(item))
	}
	expired := time.Now().Add(-time.Hour)
	assignments := repository.NewRoleAssignmentRepository(db)
	require.NoError(t, assignments.Create(&model.RoleAssignment{ID: "strict-assignment", UserID: viewer, RoleID: roleIDs[0]}))
	require.NoError(t, assignments.Create(&model.RoleAssignment{ID: "strict-expired-assignment", UserID: viewer, RoleID: roleIDs[2], ExpiresAt: &expired}))
	active, err := assignments.ListByUser(viewer)
	require.NoError(t, err)
	require.Len(t, active, 1, "assignment lookup must succeed independently of conditional lookups")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failedDB := db.WithContext(ctx)
	for _, scenario := range []struct {
		name                              string
		roleDB, userDB                    *gorm.DB
		failed, warmPartial, omitUserRepo bool
	}{
		{"complete", db, db, false, false, false},
		{"conditional list fails", failedDB, db, true, false, false},
		{"conditional user fails", db, failedDB, true, false, false},
		{"lenient cached list failure", failedDB, db, true, true, false},
		{"lenient cached user failure", db, failedDB, true, true, false},
		{"conditional user repository missing", db, db, true, false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			generator, err := id.NewGenerator("aidx")
			require.NoError(t, err)
			service := role.NewService(repository.NewRoleRepository(scenario.roleDB), assignments, repository.NewMetaRepository(db), generator)
			if !scenario.omitUserRepo {
				service.SetUserRepo(repository.NewUserRepository(scenario.userDB))
			}
			if scenario.warmPartial {
				partial, err := service.GetUserRoles(viewer)
				require.NoError(t, err, "legacy lookup remains lenient")
				require.Len(t, partial, 1)
				require.Equal(t, roleIDs[0], partial[0].ID)
			}
			strict, ok := any(service).(interface {
				GetUserRolesStrict(string) ([]*model.Role, error)
			})
			require.True(t, ok, "strict consumers need an error-aware role lookup")
			resolved, err := strict.GetUserRolesStrict(viewer)
			if scenario.failed {
				require.Error(t, err)
				require.Nil(t, resolved, "partial assignments must not escape as a complete snapshot")
				if scenario.userDB == failedDB {
					service.SetUserRepo(repository.NewUserRepository(db))
					resolved, err = strict.GetUserRolesStrict(viewer)
					require.NoError(t, err, "failed strict lookup must not poison later resolution")
					require.Len(t, resolved, 2)
				}
			} else {
				require.NoError(t, err)
				require.Len(t, resolved, 2)
				require.Equal(t, roleIDs[0], resolved[0].ID)
				require.Equal(t, roleIDs[1], resolved[1].ID)
			}
		})
	}
}
