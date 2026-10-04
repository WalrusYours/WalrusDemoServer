package host

import (
	"fmt"
	"hash/fnv"
	"math"
	"slices"
	"strings"

	"github.com/timurcravtov/demo-host-server/internal/walrus"
)

// ArtistEntities and TrackEntities are the library as WALRUS entities, shaped like the `artist`
// and `track` entities of schema/music.yml.
func ArtistEntities() []walrus.Entity {
	genres := map[string]map[string]bool{}
	for _, t := range catalogue {
		id := slug(t.Artist)
		if genres[id] == nil {
			genres[id] = map[string]bool{}
		}
		for _, g := range t.Genres {
			genres[id][g] = true
		}
	}
	ids := make([]string, 0, len(genres))
	for id := range genres {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	out := make([]walrus.Entity, len(ids))
	for i, id := range ids {
		gs := make([]string, 0, len(genres[id]))
		for g := range genres[id] {
			gs = append(gs, g)
		}
		slices.Sort(gs)
		out[i] = walrus.Entity{Entity: "artist", ID: id, Attributes: map[string]any{"genres": gs}}
	}
	return out
}

func TrackEntities() []walrus.Entity {
	out := make([]walrus.Entity, len(catalogue))
	for i, t := range catalogue {
		dance, acoustic, tempo := features(t)
		out[i] = walrus.Entity{Entity: "track", ID: t.ID, Attributes: map[string]any{
			"title":        t.Title,
			"artist_id":    slug(t.Artist),
			"album_id":     fmt.Sprintf("%s_%d", slug(t.Artist), t.Year),
			"genres":       t.Genres,
			"language":     language(t),
			"explicit":     false,
			"release_date": fmt.Sprintf("%04d-01-01T00:00:00Z", t.Year),
			"duration_ms":  t.Seconds * 1000,
			"available_in": []string{"MD", "RO", "GB", "US"},
			"energy":       t.Energy,
			"valence":      t.Valence,
			"danceability": dance,
			"acousticness": acoustic,
			"tempo":        tempo,
		}}
	}
	return out
}

func slug(s string) string {
	var b strings.Builder
	underscore := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			underscore = false
		case !underscore && b.Len() > 0:
			b.WriteByte('_')
			underscore = true
		}
	}
	return strings.TrimRight(b.String(), "_")
}

func language(t Track) string {
	switch {
	case hasGenre(t, "latin"):
		return "es"
	case hasGenre(t, "classical"), hasGenre(t, "ambient"):
		return "instrumental"
	}
	return "en"
}

func hasGenre(t Track, g string) bool { return slices.Contains(t.Genres, g) }

// features gives a track the audio features the demo library has no data for. A real platform
// sends its own analysis; these are plausible stand-ins that follow energy, mood and genre, with
// a per-track variation that is the same on every run.
func features(t Track) (danceability, acousticness, tempo float64) {
	h := fnv.New32a()
	h.Write([]byte(t.ID))
	sum := h.Sum32()
	jitter := func(shift uint) float64 { return float64((sum>>shift)&0xff)/255 - 0.5 }

	danceability = 0.25 + 0.3*t.Energy + 0.35*t.Valence + 0.16*jitter(0)
	for _, g := range []string{"dance", "disco", "funk", "hip hop", "r&b", "reggae", "latin"} {
		if hasGenre(t, g) {
			danceability += 0.15
			break
		}
	}
	for _, g := range []string{"classical", "ambient", "folk", "metal"} {
		if hasGenre(t, g) {
			danceability -= 0.15
			break
		}
	}

	acousticness = 0.3 * (1 - t.Energy)
	for _, g := range []string{"acoustic", "folk", "classical", "ambient", "country", "jazz", "indie folk"} {
		if hasGenre(t, g) {
			acousticness = 0.8 - 0.5*t.Energy
			break
		}
	}
	acousticness += 0.1 * jitter(8)

	tempo = 70 + 90*t.Energy + 24*jitter(16)
	if hasGenre(t, "classical") || hasGenre(t, "ambient") {
		tempo = 55 + 40*t.Energy + 10*jitter(16)
	}
	return round2(clamp01(danceability)), round2(clamp01(acousticness)), math.Round(tempo)
}

func clamp01(x float64) float64 { return math.Max(0, math.Min(1, x)) }
func round2(x float64) float64  { return math.Round(x*100) / 100 }
