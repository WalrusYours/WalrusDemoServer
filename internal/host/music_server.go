package host

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/timurcravtov/demo-host-server/internal/walrus"
)

// The music endpoints the client's HostApi expects (src/api/http.ts).

func (s *Server) routeMusic() {
	s.mux.HandleFunc("GET /playlists", s.auth(s.listPlaylists))
	s.mux.HandleFunc("POST /playlists", s.auth(s.createPlaylist))
	s.mux.HandleFunc("GET /playlists/{id}", s.auth(s.getPlaylist))
	s.mux.HandleFunc("POST /playlists/{id}/tracks", s.auth(s.addToPlaylist))
	s.mux.HandleFunc("DELETE /playlists/{id}/tracks/{track}", s.auth(s.removeFromPlaylist))
	s.mux.HandleFunc("GET /playlists/{id}/suggestions", s.auth(s.playlistSuggestions))
}

func (s *Server) listPlaylists(w http.ResponseWriter, _ *http.Request, u User) {
	writeJSON(w, http.StatusOK, s.store.Playlists(u.ID))
}

func (s *Server) createPlaylist(w http.ResponseWriter, r *http.Request, u User) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := s.store.CreatePlaylist(u.ID, in.Name)
	if err != nil {
		musicError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) getPlaylist(w http.ResponseWriter, r *http.Request, u User) {
	p, ok := s.store.Playlist(u.ID, r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, ErrNoPlaylist.Error())
		return
	}
	tracks := make([]Track, 0, len(p.TrackIDs))
	for _, id := range p.TrackIDs {
		if t, ok := TrackByID(id); ok {
			tracks = append(tracks, t)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"playlist": p, "tracks": tracks})
}

func (s *Server) addToPlaylist(w http.ResponseWriter, r *http.Request, u User) {
	var in struct {
		TrackID string `json:"trackId"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.store.AddTrack(u.ID, r.PathValue("id"), in.TrackID); err != nil {
		musicError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeFromPlaylist(w http.ResponseWriter, r *http.Request, u User) {
	if err := s.store.RemoveTrack(u.ID, r.PathValue("id"), r.PathValue("track")); err != nil {
		musicError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func musicError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrBadPlaylistName):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrNoPlaylist), errors.Is(err, ErrNoTrack):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		serverError(w, err)
	}
}

// Suggestion matches the client's Suggestion type.
type Suggestion struct {
	Track   Track   `json:"track"`
	Score   float64 `json:"score"`
	Because string  `json:"because"`
}

const (
	suggestRecommender = "playlist_add"
	titleRecommender   = "from_title"
	defaultSuggestions = 8
	maxSuggestions     = 20
)

// playlistSuggestions is "You might want to add here...". The order, the scores and the reasons
// come from WALRUS; this handler sends the playlist as the seed and the user's Tune values as
// knobs, then attaches the song details. It never ranks anything itself.
func (s *Server) playlistSuggestions(w http.ResponseWriter, r *http.Request, u User) {
	if s.engine == nil {
		writeError(w, http.StatusServiceUnavailable, "recommendations are not configured (set WALRUS_URL)")
		return
	}
	p, ok := s.store.Playlist(u.ID, r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, ErrNoPlaylist.Error())
		return
	}

	limit := defaultSuggestions
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(n, maxSuggestions)
	}
	knobs := map[string]float64{}
	for key, vals := range q {
		id, ok := strings.CutPrefix(key, "knobs.")
		if !ok || len(vals) == 0 {
			continue
		}
		f, err := strconv.ParseFloat(vals[0], 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, key+" must be a number")
			return
		}
		knobs[id] = f
	}

	req := walrus.RecommendRequest{
		User:    u.ID,
		Items:   p.TrackIDs, // never nil: an empty playlist is a valid seed
		Limit:   limit,
		Knobs:   knobs,
		Explain: true,
	}
	if words := titleWords(p.Name); len(words) > 0 {
		req.Context = map[string]any{"title_words": words}
	}
	res, err := s.engine.Recommend(r.Context(), suggestRecommender, req)
	if err != nil {
		var e *walrus.Error
		// a bad knob is the caller's mistake; anything else is the engine being unavailable
		if errors.As(err, &e) && e.Status == http.StatusBadRequest {
			writeError(w, http.StatusBadRequest, e.Message)
			return
		}
		log.Printf("walrus recommend: %v", err)
		writeError(w, http.StatusBadGateway, "recommendations are unavailable right now")
		return
	}

	out := make([]Suggestion, 0, len(res.Items))
	for _, it := range res.Items {
		t, ok := TrackByID(it.ID)
		if !ok {
			log.Printf("walrus returned %q, which is not in the library; skipped", it.ID)
			continue
		}
		out = append(out, Suggestion{Track: t, Score: it.Score, Because: it.Reason})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"suggestions": out,
		"candidates":  res.CandidatesConsidered,
		"tookMs":      res.TookMS,
		"fromTitle":   res.Used == titleRecommender,
	})
}
