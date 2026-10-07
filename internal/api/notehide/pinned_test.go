package notehide

import (
	"testing"

	"github.com/elythia-network/elythia/internal/core/notesfilter"
	"github.com/elythia-network/elythia/internal/entity"
	"github.com/elythia-network/elythia/internal/model"
	"github.com/stretchr/testify/require"
)

func TestPinnedPublicNotesBypassOnlyTopLevelTimeLockdown(t *testing.T) {
	cutoff := 0
	signin := true
	for _, visibility := range []string{"public", "home"} {
		for _, pref := range []string{"signin", "hidden", "followers"} {
			t.Run(visibility+"/"+pref, func(t *testing.T) {
				user := entity.UserLite{ID: "author"}
				switch pref {
				case "signin":
					user.RequireSigninToViewContents = &signin
				case "hidden":
					user.MakeNotesHiddenBefore = &cutoff
				case "followers":
					user.MakeNotesFollowersOnlyBefore = &cutoff
				}
				makeNote := func() entity.NoteEntity {
					embed := &entity.NoteEntity{ID: "embed", UserID: "author", User: user, Visibility: visibility, CreatedAt: "2020-01-01T00:00:00.000Z", Text: heStr("embedded secret")}
					return entity.NoteEntity{ID: "pin", UserID: "author", User: user, Visibility: visibility, CreatedAt: "2020-01-01T00:00:00.000Z", Text: heStr("pinned public"), Renote: embed}
				}
				pinned := []entity.NoteEntity{makeNote()}
				hidePinnedNotesAt(nil, pinned, nil, heNowMs)
				if pref == "signin" {
					require.True(t, pinned[0].IsHidden, "current pins still require sign-in")
					require.Nil(t, pinned[0].Text)
				} else {
					require.False(t, pinned[0].IsHidden)
					require.Equal(t, "pinned public", *pinned[0].Text)
				}
				require.Equal(t, visibility, pinned[0].Visibility)
				require.True(t, pinned[0].Renote.IsHidden, "pin exception must not reach quoted notes")
				require.Nil(t, pinned[0].Renote.Text)
				ordinary := []entity.NoteEntity{makeNote()}
				hideEmbedsAt(nil, ordinary, nil, heNowMs)
				require.True(t, ordinary[0].IsHidden, "non-profile paths must retain lockdown")
			})
		}
	}
}

func TestPinnedNotesKeepIntrinsicAccessAndNestedEmbeds(t *testing.T) {
	for _, viewerID := range []string{"", "outsider", "follower", "recipient", "author"} {
		t.Run(viewerID, func(t *testing.T) {
			var viewer *model.User
			if viewerID != "" {
				viewer = &model.User{ID: viewerID}
			}
			follow := *followersEmbed("followers", "author")
			direct := *followersEmbed("direct", "author")
			direct.Visibility = "specified"
			direct.VisibleUserIDs = []string{"recipient"}
			direct.Mentions = []string{"outsider"}
			quote := followersEmbed("quote", "secret-author")
			quote.Renote = followersEmbed("nested", "secret-author")
			public := entity.NoteEntity{ID: "public", UserID: "author", Visibility: "public", Text: heStr("public"), Renote: quote, Reply: followersEmbed("reply", "secret-author")}
			repo := followsRepo([2]string{"follower", "author"})
			// Like 1.5.0's profile path, apply intrinsic ACLs before the preference
			// helper. Disallowed followers/DM notes must not reach the packed list.
			visible := notesfilter.FilterVisible(viewer, []*model.Note{
				{ID: follow.ID, UserID: "author", Visibility: model.NoteVisibilityFollowers},
				{ID: direct.ID, UserID: "author", Visibility: model.NoteVisibilitySpecified, VisibleUserIDs: []string{"recipient"}},
				{ID: public.ID, UserID: "author", Visibility: model.NoteVisibilityPublic},
			}, repo)
			byID := map[string]entity.NoteEntity{follow.ID: follow, direct.ID: direct, public.ID: public}
			packed := make([]entity.NoteEntity, 0, len(visible))
			for _, note := range visible {
				packed = append(packed, byID[note.ID])
			}
			hidePinnedNotesAt(viewer, packed, repo, heNowMs)
			got := make(map[string]entity.NoteEntity, len(packed))
			for _, note := range packed {
				got[note.ID] = note
			}
			_, hasFollow := got[follow.ID]
			_, hasDirect := got[direct.ID]
			require.Equal(t, viewerID == "follower" || viewerID == "author", hasFollow)
			require.Equal(t, viewerID == "recipient" || viewerID == "author", hasDirect)
			pin := got[public.ID]
			require.False(t, pin.IsHidden)
			require.True(t, pin.Renote.IsHidden)
			require.True(t, pin.Renote.Renote.IsHidden)
			require.True(t, pin.Reply.IsHidden)
		})
	}
}

func TestHidePinnedNotesPublicEntryPoint(t *testing.T) {
	signin := true
	packed := []entity.NoteEntity{{ID: "pin", UserID: "author", User: entity.UserLite{ID: "author", RequireSigninToViewContents: &signin}, Visibility: "public", Text: heStr("public pin")}}
	HidePinnedNotes(nil, packed)
	require.True(t, packed[0].IsHidden)
	require.Nil(t, packed[0].Text)
}
