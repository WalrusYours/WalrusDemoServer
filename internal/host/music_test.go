package host

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/timurcravtov/demo-host-server/internal/walrus"
)

// fakeEngine records what the host sent and answers with a fixed list.
type fakeEngine struct {
	name  string
	got   walrus.RecommendRequest
	calls int
	res   *walrus.Response
	err   error
}

func (f *fakeEngine) PushSchema(context.Context, []byte, bool) (*walrus.SchemaResult, error) {
	return &walrus.SchemaResult{OK: true, Version: 1}, nil
}

func (f *fakeEngine) Recommend(_ context.Context, name string, req walrus.RecommendRequest) (*walrus.Response, error) {
	f.name, f.got, f.calls = name, req, f.calls+1
	return f.res, f.err
}

func musicClient(t *testing.T, e Engine) *client {
	t.Helper()
	c := &client{t: t, srv: NewServer(NewStore(), "", WithEngine(e))}
	c.login("maya")
	return c
}

func decodeJSON[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return v
}

func TestNewUsersGetTheStarterPlaylistsAndOwnThem(t *testing.T) {
	c := musicClient(t, nil)
	rec := c.do("GET", "/playlists", "")
	ps := decodeJSON[[]Playlist](t, rec.Body.Bytes())
	if rec.Code != 200 || len(ps) != len(starterPlaylists) || ps[0].ID != "rock_anthems" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	// maya's changes are hers alone
	if rec := c.do("POST", "/playlists/rock_anthems/tracks", `{"trackId":"paranoid"}`); rec.Code != 204 {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	c.login("omar")
	omar := decodeJSON[struct {
		Playlist Playlist
		Tracks   []Track
	}](t, c.do("GET", "/playlists/rock_anthems", "").Body.Bytes())
	if slices.Contains(omar.Playlist.TrackIDs, "paranoid") {
		t.Error("another user's playlist must not change")
	}
	if len(omar.Tracks) != len(omar.Playlist.TrackIDs) || omar.Tracks[0].Title == "" {
		t.Errorf("the playlist should come with its songs: %+v", omar.Tracks)
	}
	if starterPlaylists[0].TrackIDs[len(starterPlaylists[0].TrackIDs)-1] == "paranoid" {
		t.Error("the starter data must not be shared between users")
	}
}

func TestPlaylistEditing(t *testing.T) {
	c := musicClient(t, nil)

	rec := c.do("POST", "/playlists", `{"name":"  Road trip 2  "}`)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	p := decodeJSON[Playlist](t, rec.Body.Bytes())
	if p.Name != "Road trip 2" || p.TrackIDs == nil || len(p.TrackIDs) != 0 {
		t.Fatalf("new playlist = %+v", p)
	}
	if first := decodeJSON[[]Playlist](t, c.do("GET", "/playlists", "").Body.Bytes())[0]; first.ID != p.ID {
		t.Error("a new playlist is listed first")
	}

	for _, id := range []string{"holocene", "holocene", "fast_car"} { // adding twice changes nothing
		if rec := c.do("POST", "/playlists/"+p.ID+"/tracks", `{"trackId":"`+id+`"}`); rec.Code != 204 {
			t.Fatalf("add %s: %d", id, rec.Code)
		}
	}
	got, _ := c.srv.store.Playlist("maya", p.ID)
	if !slices.Equal(got.TrackIDs, []string{"holocene", "fast_car"}) {
		t.Fatalf("tracks = %v", got.TrackIDs)
	}
	if rec := c.do("DELETE", "/playlists/"+p.ID+"/tracks/holocene", ""); rec.Code != 204 {
		t.Fatalf("remove: %d", rec.Code)
	}
	got, _ = c.srv.store.Playlist("maya", p.ID)
	if !slices.Equal(got.TrackIDs, []string{"fast_car"}) {
		t.Fatalf("tracks after remove = %v", got.TrackIDs)
	}

	for _, bad := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/playlists", `{"name":"   "}`, 400},
		{"POST", "/playlists", `{"name":"` + strings.Repeat("x", 61) + `"}`, 400},
		{"POST", "/playlists/nope/tracks", `{"trackId":"holocene"}`, 404},
		{"POST", "/playlists/" + p.ID + "/tracks", `{"trackId":"no_such_song"}`, 404},
		{"DELETE", "/playlists/nope/tracks/holocene", ``, 404},
		{"GET", "/playlists/nope", ``, 404},
	} {
		if rec := c.do(bad.method, bad.path, bad.body); rec.Code != bad.status {
			t.Errorf("%s %s = %d, want %d", bad.method, bad.path, rec.Code, bad.status)
		}
	}

	c.token = ""
	if rec := c.do("GET", "/playlists", ""); rec.Code != 401 {
		t.Errorf("signed out = %d", rec.Code)
	}
}

type suggestionsBody struct {
	Suggestions []Suggestion `json:"suggestions"`
	Candidates  int          `json:"candidates"`
	TookMs      float64      `json:"tookMs"`
	FromTitle   bool         `json:"fromTitle"`
}

func TestSuggestionsComeFromWalrusInItsOrder(t *testing.T) {
	e := &fakeEngine{res: &walrus.Response{
		Used: "playlist_add", CandidatesConsidered: 31, TookMS: 1.5,
		Items: []walrus.Item{
			{ID: "paranoid", Score: 0.9, Reason: "Often added to playlists with Back in Black"},
			{ID: "enter_sandman", Score: 0.8},
			{ID: "not_in_the_library", Score: 0.7},
			{ID: "crazy_train", Score: 0.6, Reason: "Sounds like Thunderstruck"},
		},
	}}
	c := musicClient(t, e)

	rec := c.do("GET", "/playlists/rock_anthems/suggestions?limit=4&knobs.vibe_vs_branch_out=1&knobs.deep_cuts_vs_hits=0.25", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	body := decodeJSON[suggestionsBody](t, rec.Body.Bytes())

	// WALRUS's order is kept, details are attached, an unknown id is dropped
	var order []string
	for _, s := range body.Suggestions {
		order = append(order, s.Track.ID)
		if s.Track.Title == "" {
			t.Errorf("%s came without its details", s.Track.ID)
		}
	}
	if !slices.Equal(order, []string{"paranoid", "enter_sandman", "crazy_train"}) {
		t.Fatalf("order = %v", order)
	}
	if body.Suggestions[0].Because != "Often added to playlists with Back in Black" || body.Suggestions[0].Score != 0.9 {
		t.Errorf("first = %+v", body.Suggestions[0])
	}
	if body.Candidates != 31 || body.TookMs != 1.5 || body.FromTitle {
		t.Errorf("meta = %+v", body)
	}

	// what the host asked WALRUS
	want, _ := c.srv.store.Playlist("maya", "rock_anthems")
	if e.name != "playlist_add" || e.calls != 1 || !slices.Equal(e.got.Items, want.TrackIDs) ||
		e.got.User != "maya" || e.got.Limit != 4 || !e.got.Explain {
		t.Errorf("request = %s %+v", e.name, e.got)
	}
	if e.got.Knobs["vibe_vs_branch_out"] != 1 || e.got.Knobs["deep_cuts_vs_hits"] != 0.25 || len(e.got.Knobs) != 2 {
		t.Errorf("knobs = %v", e.got.Knobs)
	}
	if words, _ := e.got.Context["title_words"].([]string); !slices.Equal(words, []string{"anthems", "rock"}) {
		t.Errorf("title words = %v", e.got.Context)
	}
}

func TestAnEmptyPlaylistIsSentAsAnEmptySeed(t *testing.T) {
	e := &fakeEngine{res: &walrus.Response{Used: "from_title", Items: []walrus.Item{{ID: "weightless", Score: 0.5}}}}
	c := musicClient(t, e)
	p := decodeJSON[Playlist](t, c.do("POST", "/playlists", `{"name":"Sleepy ambient evenings"}`).Body.Bytes())

	rec := c.do("GET", "/playlists/"+p.ID+"/suggestions", "")
	body := decodeJSON[suggestionsBody](t, rec.Body.Bytes())
	if rec.Code != 200 || !body.FromTitle || len(body.Suggestions) != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if e.got.Items == nil || len(e.got.Items) != 0 || e.got.Limit != defaultSuggestions {
		t.Errorf("items = %#v limit = %d", e.got.Items, e.got.Limit)
	}
	if words, _ := e.got.Context["title_words"].([]string); !slices.Equal(words, []string{"ambient", "evenings", "sleepy"}) {
		t.Errorf("title words = %v", e.got.Context)
	}
}

func TestSuggestionsFailureModes(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		c := musicClient(t, nil)
		if rec := c.do("GET", "/playlists/rock_anthems/suggestions", ""); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%d %s", rec.Code, rec.Body)
		}
	})
	t.Run("unknown playlist", func(t *testing.T) {
		e := &fakeEngine{}
		c := musicClient(t, e)
		if rec := c.do("GET", "/playlists/nope/suggestions", ""); rec.Code != 404 || e.calls != 0 {
			t.Errorf("%d, engine called %d times", rec.Code, e.calls)
		}
	})
	t.Run("bad query", func(t *testing.T) {
		e := &fakeEngine{}
		c := musicClient(t, e)
		for _, q := range []string{"limit=0", "limit=x", "knobs.vibe_vs_branch_out=high"} {
			if rec := c.do("GET", "/playlists/rock_anthems/suggestions?"+q, ""); rec.Code != 400 {
				t.Errorf("%s = %d", q, rec.Code)
			}
		}
		if e.calls != 0 {
			t.Error("a bad request should not reach the engine")
		}
	})
	t.Run("limit is capped", func(t *testing.T) {
		e := &fakeEngine{res: &walrus.Response{}}
		c := musicClient(t, e)
		c.do("GET", "/playlists/rock_anthems/suggestions?limit=500", "")
		if e.got.Limit != maxSuggestions {
			t.Errorf("limit = %d", e.got.Limit)
		}
	})
	t.Run("the engine rejects a knob", func(t *testing.T) {
		e := &fakeEngine{err: &walrus.Error{Status: 400, Code: "validation_error", Message: `knob "taste_vs_crowd" is not offered by playlist_add`}}
		c := musicClient(t, e)
		rec := c.do("GET", "/playlists/rock_anthems/suggestions?knobs.taste_vs_crowd=1", "")
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "not offered by playlist_add") {
			t.Errorf("%d %s", rec.Code, rec.Body)
		}
	})
	for name, err := range map[string]error{
		"the engine is down":      errors.New("dial tcp: connection refused"),
		"no schema pushed yet":    &walrus.Error{Status: 409, Code: "schema_missing", Message: "no schema has been pushed yet"},
		"the engine misbehaves":   &walrus.Error{Status: 500, Code: "internal", Message: "internal error"},
		"the engine key is wrong": &walrus.Error{Status: 401, Code: "unauthorized", Message: "wrong key"},
	} {
		t.Run(name, func(t *testing.T) {
			c := musicClient(t, &fakeEngine{err: err})
			rec := c.do("GET", "/playlists/rock_anthems/suggestions", "")
			if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "connection refused") {
				t.Errorf("%d %s (an internal failure must not leak)", rec.Code, rec.Body)
			}
		})
	}
}

func TestSyncSchemaRetriesUntilTheEngineIsUp(t *testing.T) {
	e := &flakyPush{failures: 2}
	ctx, cancel := context.WithTimeout(context.Background(), 30e9)
	defer cancel()
	if err := SyncSchema(ctx, e, []byte("version: 1")); err != nil || e.calls != 3 {
		t.Fatalf("err = %v after %d pushes", err, e.calls)
	}

	rejected := &flakyPush{reject: true}
	if err := SyncSchema(ctx, rejected, []byte("x")); !errors.Is(err, errSchemaRejected) || rejected.calls != 1 {
		t.Fatalf("a rejected schema is final: err = %v after %d pushes", err, rejected.calls)
	}

	denied := &flakyPush{err: &walrus.Error{Status: 401, Code: "unauthorized", Message: "wrong key"}}
	if err := SyncSchema(ctx, denied, nil); !walrus.IsCode(err, "unauthorized") || denied.calls != 1 {
		t.Fatalf("a wrong key is final: err = %v after %d pushes", err, denied.calls)
	}

	stopped, stop := context.WithCancel(context.Background())
	stop()
	if err := SyncSchema(stopped, &flakyPush{failures: 99}, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled context ends the retries: %v", err)
	}
}

type flakyPush struct {
	fakeEngine
	failures int
	reject   bool
	err      error
	calls    int
}

func (f *flakyPush) PushSchema(context.Context, []byte, bool) (*walrus.SchemaResult, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.reject {
		return &walrus.SchemaResult{OK: false, Message: "bad", Errors: []walrus.Issue{{Path: "a", Message: "b"}}}, nil
	}
	if f.calls <= f.failures {
		return nil, errors.New("connection refused")
	}
	return &walrus.SchemaResult{OK: true, Version: 1}, nil
}
