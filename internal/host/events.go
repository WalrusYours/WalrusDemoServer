package host

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/timurcravtov/demo-host-server/internal/walrus"
)

// playlistAdd is the event WALRUS learns "songs that go together" from. The playlist id is made
// unique across users, since every user has a playlist called rock_anthems.
func playlistAdd(userID, playlistID, trackID string, at time.Time) walrus.Interaction {
	return walrus.Interaction{
		User: userID, Type: "add_to_playlist", Target: trackID, TS: at.UTC().Format(time.RFC3339),
		Fields: map[string]string{"playlist_id": userID + ":" + playlistID},
	}
}

// CommunityEvents are the other people's playlists as add_to_playlist events, spread over the last
// few weeks so they look like history.
func CommunityEvents(now time.Time) []walrus.Interaction {
	var out []walrus.Interaction
	for p, tracks := range communityPlaylists {
		user := fmt.Sprintf("community_%02d", p+1)
		created := now.Add(-time.Duration(p+1) * 36 * time.Hour)
		for k, id := range tracks {
			out = append(out, playlistAdd(user, user, id, created.Add(time.Duration(k)*time.Minute)))
		}
	}
	return out
}

// report tells WALRUS about an event without making the user wait for it. A failure is logged and
// dropped: a lost event costs a little accuracy, never a failed request.
func (s *Server) report(e walrus.Interaction) {
	if s.engine == nil {
		return
	}
	s.events.Add(1)
	go func() {
		defer s.events.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.engine.SendInteractions(ctx, []walrus.Interaction{e}); err != nil {
			log.Printf("walrus: could not send %s event: %v", e.Type, err)
		}
	}()
}

// WaitEvents blocks until every event reported so far has been sent. Tests use it; the server
// never needs to.
func (s *Server) WaitEvents() { s.events.Wait() }
