package users

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/shiroha-a/mk/internal/model"
	"github.com/shiroha-a/mk/internal/server/middleware"
	"github.com/shiroha-a/mk/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

func TestShowPinnedNotesKeepSigninAndIntrinsicVisibility(t *testing.T) {
	for _, viewerID := range []string{"", "outsider", "follower", "recipient"} {
		t.Run(viewerID, func(t *testing.T) {
			h, users := newTestHandler(t)
			addTestUser(users)
			author := users.Users["user1"]
			author.RequireSigninToViewContents = true
			notes := h.noteRepo.(*testutil.MockNoteRepository)
			pins := testutil.NewMockUserNotePiningRepository()
			h.SetPiningRepo(pins)
			followers := testutil.NewMockFollowingRepository()
			followers.Followings["f"] = &model.Following{FollowerID: "follower", FolloweeID: author.ID}
			h.SetFollowingRepo(followers)
			for _, visibility := range []model.NoteVisibility{model.NoteVisibilityPublic, model.NoteVisibilityHome, model.NoteVisibilityFollowers, model.NoteVisibilitySpecified} {
				id := string(visibility)
				text := "content " + id
				notes.Notes[id] = &model.Note{ID: id, UserID: author.ID, User: author, Text: &text, Visibility: visibility, VisibleUserIDs: model.StringArray{"recipient"}, Reactions: datatypes.JSON([]byte("{}"))}
				require.NoError(t, pins.Create(&model.UserNotePining{ID: "pin_" + id, UserID: author.ID, NoteID: id}))
			}
			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/api/users/show", strings.NewReader(`{"userId":"user1"}`))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			if viewerID != "" {
				ctx.Set(string(middleware.UserContextKey), &model.User{ID: viewerID})
			}
			require.NoError(t, h.Show(ctx))
			require.Equal(t, http.StatusOK, rec.Code)
			var response struct {
				PinnedNotes []struct {
					ID         string  `json:"id"`
					Text       *string `json:"text"`
					Visibility string  `json:"visibility"`
					IsHidden   bool    `json:"isHidden"`
				} `json:"pinnedNotes"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			ids := []string{}
			for _, note := range response.PinnedNotes {
				if viewerID == "" {
					require.True(t, note.IsHidden, "1.5.0 pins do not bypass sign-in")
					require.Nil(t, note.Text)
				} else {
					require.False(t, note.IsHidden)
					require.NotNil(t, note.Text)
					require.Equal(t, "content "+note.ID, *note.Text)
				}
				require.Equal(t, note.ID, note.Visibility)
				ids = append(ids, note.ID)
			}
			want := []string{"public", "home"}
			if viewerID == "follower" {
				want = append(want, "followers")
			}
			if viewerID == "recipient" {
				want = append(want, "specified")
			}
			require.ElementsMatch(t, want, ids)
		})
	}
}
