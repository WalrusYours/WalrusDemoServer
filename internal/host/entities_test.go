package host

import "testing"

func TestTheLibraryHasAHundredTracksWithUniqueIDs(t *testing.T) {
	if len(catalogue) < 100 {
		t.Fatalf("%d tracks", len(catalogue))
	}
	seen := map[string]bool{}
	for _, tr := range catalogue {
		if seen[tr.ID] {
			t.Errorf("duplicate track id %q", tr.ID)
		}
		seen[tr.ID] = true
		if tr.Title == "" || tr.Artist == "" || len(tr.Genres) == 0 || tr.Seconds <= 0 || tr.Year <= 0 {
			t.Errorf("%s is incomplete: %+v", tr.ID, tr)
		}
		if tr.Energy < 0 || tr.Energy > 1 || tr.Valence < 0 || tr.Valence > 1 {
			t.Errorf("%s: energy %v and valence %v must be in 0..1", tr.ID, tr.Energy, tr.Valence)
		}
	}
	for _, p := range starterPlaylists {
		for _, id := range p.TrackIDs {
			if !seen[id] {
				t.Errorf("playlist %s holds %q, which is not in the library", p.ID, id)
			}
		}
	}
}

func TestTrackEntitiesCarryEveryAttributeTheSchemaNeeds(t *testing.T) {
	want := []string{"artist_id", "album_id", "genres", "language", "explicit", "release_date", "duration_ms",
		"available_in", "energy", "valence", "danceability", "acousticness", "tempo"}
	ents := TrackEntities()
	if len(ents) != len(catalogue) {
		t.Fatalf("%d entities for %d tracks", len(ents), len(catalogue))
	}
	for _, e := range ents {
		if e.Entity != "track" || len(e.Attributes) != len(want) {
			t.Fatalf("%s: %+v", e.ID, e)
		}
		for _, k := range want {
			if _, ok := e.Attributes[k]; !ok {
				t.Errorf("%s has no %s", e.ID, k)
			}
		}
		for _, k := range []string{"danceability", "acousticness"} {
			if v := e.Attributes[k].(float64); v < 0 || v > 1 {
				t.Errorf("%s %s = %v", e.ID, k, v)
			}
		}
	}
	first := ents[0].Attributes
	if first["artist_id"] != "ac_dc" || first["duration_ms"] != 255000 || first["release_date"] != "1980-01-01T00:00:00Z" {
		t.Errorf("first track = %+v", first)
	}
}

func TestEveryTrackPointsAtAnArtistEntity(t *testing.T) {
	artists := map[string]bool{}
	for _, a := range ArtistEntities() {
		artists[a.ID] = true
		if a.Entity != "artist" || len(a.Attributes["genres"].([]string)) == 0 {
			t.Errorf("artist %+v", a)
		}
	}
	for _, tr := range TrackEntities() {
		if id := tr.Attributes["artist_id"].(string); !artists[id] {
			t.Errorf("%s points at artist %q, which was not sent", tr.ID, id)
		}
	}
}

func TestFeaturesAreStableAndFollowTheMusic(t *testing.T) {
	loud, _ := TrackByID("thunderstruck")
	calm, _ := TrackByID("weightless")
	folk, _ := TrackByID("blowin_wind")
	d1, a1, t1 := features(loud)
	d2, a2, t2 := features(loud)
	if d1 != d2 || a1 != a2 || t1 != t2 {
		t.Error("features must be the same on every run")
	}
	_, calmAcoustic, calmTempo := features(calm)
	_, folkAcoustic, _ := features(folk)
	if !(t1 > calmTempo) || !(folkAcoustic > a1) || !(calmAcoustic > a1) {
		t.Errorf("loud rock: tempo %v acoustic %v; ambient: tempo %v acoustic %v; folk acoustic %v", t1, a1, calmTempo, calmAcoustic, folkAcoustic)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"AC/DC":                  "ac_dc",
		"Guns N' Roses":          "guns_n_roses",
		"Florence + The Machine": "florence_the_machine",
		"  The xx ":              "the_xx",
		"a  b":                   "a_b",
	} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}
