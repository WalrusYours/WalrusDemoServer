package host

import (
	"errors"
	"slices"
	"strings"
	"unicode"
)

const maxPlaylistNameRunes = 60

var (
	ErrBadPlaylistName = errors.New("playlist name must be 1 to 60 characters")
	ErrNoPlaylist      = errors.New("playlist not found")
	ErrNoTrack         = errors.New("track not found")
)

// Track and Playlist match the client's Track and Playlist types (src/types/index.ts).
type Track struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Artist  string   `json:"artist"`
	Year    int      `json:"year"`
	Genres  []string `json:"genres"`
	Energy  float64  `json:"energy"`
	Valence float64  `json:"valence"`
	Seconds int      `json:"seconds"`
	Plays   int      `json:"plays"`
}

type Playlist struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	TrackIDs    []string `json:"trackIds"`
}

var catalogue = slices.Concat(coreTracks, moreTracks)

var trackByID = func() map[string]Track {
	m := make(map[string]Track, len(catalogue))
	for _, t := range catalogue {
		m[t.ID] = t
	}
	return m
}()

// TrackByID looks a song up in the library.
func TrackByID(id string) (Track, bool) {
	t, ok := trackByID[id]
	return t, ok
}

func clonePlaylist(p Playlist) Playlist {
	p.TrackIDs = slices.Clone(p.TrackIDs)
	if p.TrackIDs == nil {
		p.TrackIDs = []string{}
	}
	return p
}

// playlistsOf returns the user's playlists, giving a new user a copy of the starter ones. The
// caller holds s.mu.
func (s *Store) playlistsOf(userID string) []Playlist {
	if s.playlists == nil {
		s.playlists = map[string][]Playlist{}
	}
	ps, ok := s.playlists[userID]
	if !ok {
		for _, p := range starterPlaylists {
			ps = append(ps, clonePlaylist(p))
		}
		s.playlists[userID] = ps
	}
	return ps
}

// Playlists returns the user's playlists, newest first.
func (s *Store) Playlists(userID string) []Playlist {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps := s.playlistsOf(userID)
	out := make([]Playlist, len(ps))
	for i, p := range ps {
		out[i] = clonePlaylist(p)
	}
	return out
}

func (s *Store) Playlist(userID, id string) (Playlist, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.playlistsOf(userID) {
		if p.ID == id {
			return clonePlaylist(p), true
		}
	}
	return Playlist{}, false
}

func (s *Store) CreatePlaylist(userID, name string) (Playlist, error) {
	name = strings.TrimSpace(name)
	if n := len([]rune(name)); n == 0 || n > maxPlaylistNameRunes {
		return Playlist{}, ErrBadPlaylistName
	}
	id, err := newToken()
	if err != nil {
		return Playlist{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := Playlist{ID: "pl_" + id[:12], Name: name, TrackIDs: []string{}}
	ps := s.playlistsOf(userID)
	s.playlists[userID] = append([]Playlist{p}, ps...)
	return clonePlaylist(p), nil
}

// AddTrack appends a song to a playlist; adding one that is already there changes nothing.
func (s *Store) AddTrack(userID, playlistID, trackID string) error {
	if _, ok := TrackByID(trackID); !ok {
		return ErrNoTrack
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ps := s.playlistsOf(userID)
	for i := range ps {
		if ps[i].ID == playlistID {
			if !slices.Contains(ps[i].TrackIDs, trackID) {
				ps[i].TrackIDs = append(ps[i].TrackIDs, trackID)
			}
			return nil
		}
	}
	return ErrNoPlaylist
}

func (s *Store) RemoveTrack(userID, playlistID, trackID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps := s.playlistsOf(userID)
	for i := range ps {
		if ps[i].ID == playlistID {
			ps[i].TrackIDs = slices.DeleteFunc(ps[i].TrackIDs, func(id string) bool { return id == trackID })
			return nil
		}
	}
	return ErrNoPlaylist
}

// titleWords are the lower-case words of a playlist's name, which WALRUS can use when a
// playlist is still empty.
func titleWords(name string) []string {
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	slices.Sort(words)
	return slices.Compact(words)
}
