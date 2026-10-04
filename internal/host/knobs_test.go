package host

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/timurcravtov/demo-host-server/internal/walrus"
)

func TestKnobConfigIsDrawnFromTheEnginesSchema(t *testing.T) {
	e := &fakeEngine{}
	c := musicClient(t, e)
	rec := c.do("GET", "/knobs?locale=ro", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	cfg := decodeJSON[KnobConfig](t, rec.Body.Bytes())
	if e.locale != "ro" {
		t.Errorf("the locale should reach the engine, got %q", e.locale)
	}

	byID := map[string]KnobDef{}
	for _, k := range cfg.Knobs {
		byID[k.ID] = k
	}
	vibe := byID["vibe_vs_branch_out"]
	if vibe.Group != "Playlist suggestions" || vibe.Low != "Stick to the vibe" || vibe.High != "Branch out" ||
		vibe.Default != 0.3 || vibe.Min != 0 || vibe.Max != 1 || !slices.Equal(vibe.Scope, []string{"playlist_add"}) || vibe.Kind != "" {
		t.Errorf("vibe_vs_branch_out = %+v", vibe)
	}
	if mix := byID["mix_in_my_taste"]; mix.Kind != "toggle" || mix.Low == "" || mix.High == "" {
		t.Errorf("a toggle keeps its kind and gets end labels: %+v", mix)
	}
	if !slices.Equal(byID["taste_vs_crowd"].Scope, []string{"home"}) {
		t.Errorf("taste_vs_crowd scope = %v", byID["taste_vs_crowd"].Scope)
	}

	// a preset names only what it moves; the panel gets a full set, and the surfaces it belongs to
	var quiet PresetDef
	for _, p := range cfg.Presets {
		if p.ID == "quiet_playlists" {
			quiet = p
		}
	}
	if quiet.Label != "Quiet playlists" || !slices.Equal(quiet.Scope, []string{"playlist_add"}) {
		t.Errorf("preset = %+v", quiet)
	}
	if quiet.Knobs["vibe_vs_branch_out"] != 0 || quiet.Knobs["deep_cuts_vs_hits"] != 0.3 || quiet.Knobs["taste_vs_crowd"] != 0.5 {
		t.Errorf("preset values = %v", quiet.Knobs)
	}
}

func TestKnobConfigWithoutAnEngine(t *testing.T) {
	if rec := musicClient(t, nil).do("GET", "/knobs", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("not configured = %d", rec.Code)
	}
	e := &fakeEngine{knobsErr: errors.New("connection refused")}
	rec := musicClient(t, e).do("GET", "/knobs", "")
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "connection refused") {
		t.Errorf("engine down = %d %s", rec.Code, rec.Body)
	}
	c := musicClient(t, e)
	c.token = ""
	if rec := c.do("GET", "/knobs", ""); rec.Code != 401 {
		t.Errorf("signed out = %d", rec.Code)
	}
}

func TestProfileStartsAtTheDefaultsAndKeepsWhatTheUserSaves(t *testing.T) {
	c := musicClient(t, &fakeEngine{})
	get := func() Profile {
		rec := c.do("GET", "/users/maya/profile", "")
		if rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		return decodeJSON[Profile](t, rec.Body.Bytes())
	}

	p := get()
	if len(p.Knobs) != 4 || p.Knobs["vibe_vs_branch_out"] != 0.3 || p.Knobs["mix_in_my_taste"] != 1 || p.Preset != nil {
		t.Fatalf("first profile = %+v", p)
	}

	if rec := c.do("PUT", "/users/maya/profile", `{"knobs":{"vibe_vs_branch_out":0.9},"preset":"quiet_playlists"}`); rec.Code != 204 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	p = get()
	if p.Knobs["vibe_vs_branch_out"] != 0.9 || p.Knobs["deep_cuts_vs_hits"] != 0.3 || p.Preset == nil || *p.Preset != "quiet_playlists" {
		t.Errorf("after saving = %+v", p)
	}

	// a later save merges into the saved values, and a null preset clears the preset
	c.do("PUT", "/users/maya/profile", `{"knobs":{"deep_cuts_vs_hits":1},"preset":null}`)
	p = get()
	if p.Knobs["vibe_vs_branch_out"] != 0.9 || p.Knobs["deep_cuts_vs_hits"] != 1 || p.Preset != nil {
		t.Errorf("after the second save = %+v", p)
	}

	// a knob the schema no longer has is forgotten, not an error
	c.do("PUT", "/users/maya/profile", `{"knobs":{"retired_knob":1}}`)
	if _, still := get().Knobs["retired_knob"]; still {
		t.Error("a knob the schema does not have should not come back")
	}
}

func TestProfilesBelongToTheirUser(t *testing.T) {
	c := musicClient(t, &fakeEngine{})
	c.do("PUT", "/users/maya/profile", `{"knobs":{"vibe_vs_branch_out":0.9}}`)

	for _, method := range []string{"GET", "PUT"} {
		if rec := c.do(method, "/users/omar/profile", `{}`); rec.Code != http.StatusForbidden {
			t.Errorf("%s someone else's profile = %d", method, rec.Code)
		}
	}
	c.login("omar")
	rec := c.do("GET", "/users/omar/profile", "")
	if p := decodeJSON[Profile](t, rec.Body.Bytes()); p.Knobs["vibe_vs_branch_out"] != 0.3 {
		t.Errorf("omar sees %+v", p)
	}
}

func TestProfileValidation(t *testing.T) {
	c := musicClient(t, &fakeEngine{})
	for name, body := range map[string]string{
		"bad id":        `{"knobs":{"Bad Id":1}}`,
		"not a number":  `{"knobs":{"vibe_vs_branch_out":"high"}}`,
		"unknown field": `{"settings":{}}`,
		"not json":      `{`,
	} {
		if rec := c.do("PUT", "/users/maya/profile", body); rec.Code != 400 {
			t.Errorf("%s = %d", name, rec.Code)
		}
	}
	if rec := c.do("PUT", "/users/maya/profile", `{}`); rec.Code != 204 {
		t.Errorf("an empty save = %d", rec.Code)
	}
}

func TestSuggestionsUseTheSavedTuneAndOnlyWhatThePlaylistRecommenderOffers(t *testing.T) {
	e := &fakeEngine{res: &walrus.Response{Used: "playlist_add"}}
	c := musicClient(t, e)
	c.do("PUT", "/users/maya/profile", `{"knobs":{"vibe_vs_branch_out":0.9,"deep_cuts_vs_hits":0.1,"taste_vs_crowd":0.7}}`)

	// the panel keeps a value for every knob; this request moves one of them
	rec := c.do("GET", "/playlists/rock_anthems/suggestions?knobs.deep_cuts_vs_hits=1&knobs.taste_vs_crowd=0.2", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	got := e.got.Knobs
	if got["vibe_vs_branch_out"] != 0.9 || got["deep_cuts_vs_hits"] != 1 || len(got) != 2 {
		t.Errorf("knobs sent = %v: the saved value, overridden by the request, without the feed knob", got)
	}
}

func TestAnUnknownKnobIsAMistakeButAnotherSurfacesKnobIsNot(t *testing.T) {
	e := &fakeEngine{res: &walrus.Response{}}
	c := musicClient(t, e)
	if rec := c.do("GET", "/playlists/rock_anthems/suggestions?knobs.taste_vs_crowd=1", ""); rec.Code != 200 {
		t.Errorf("a knob of the feed is simply not used here: %d", rec.Code)
	}
	calls := e.calls
	rec := c.do("GET", "/playlists/rock_anthems/suggestions?knobs.no_such_knob=1", "")
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "no_such_knob") || e.calls != calls {
		t.Errorf("%d %s (engine calls %d -> %d)", rec.Code, rec.Body, calls, e.calls)
	}
}

func TestSuggestionsNeedTheKnobCatalogToo(t *testing.T) {
	e := &fakeEngine{knobsErr: errors.New("connection refused"), res: &walrus.Response{}}
	rec := musicClient(t, e).do("GET", "/playlists/rock_anthems/suggestions", "")
	if rec.Code != http.StatusBadGateway {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestTitle(t *testing.T) {
	for in, want := range map[string]string{"discover_weekly": "Discover weekly", "default": "Default", "ünder_x": "Ünder x", "": ""} {
		if got := title(in); got != want {
			t.Errorf("title(%q) = %q, want %q", in, got, want)
		}
	}
}
