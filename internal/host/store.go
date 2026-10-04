// Package host is the demo platform's own backend: users, sessions and posts. It knows
// nothing about WALRUS itself: recommendations come through the Recommender interface.
package host

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"hash/fnv"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const MaxPostRunes = 280

var (
	ErrBadUsername = errors.New("username must be 3 to 20 characters: letters, digits or underscore")
	ErrBadContent  = errors.New("post must be 1 to 280 characters")
	ErrNoUser      = errors.New("unknown user")
)

var usernameRe = regexp.MustCompile(`^[a-z0-9_]{3,20}$`)

// Tailwind classes the client already ships, so avatars need no new CSS.
var avatarColors = []string{
	"bg-violet-600", "bg-emerald-600", "bg-rose-600", "bg-amber-600", "bg-fuchsia-600",
}

type User struct {
	ID          string // equals Username; usernames are unique and immutable
	Username    string
	AvatarColor string
	CreatedAt   time.Time
}

type Post struct {
	ID        string
	AuthorID  string
	Content   string
	CreatedAt time.Time
}

// Store is in-memory and safe for concurrent use. Everything is lost on restart, which is
// fine for the demo; seed/ recreates the personas.
type Store struct {
	mu        sync.RWMutex
	users     map[string]User
	posts     []Post // append order, oldest first
	postByID  map[string]int
	sessions  map[string]string     // token -> user id
	playlists map[string][]Playlist // user id -> playlists, created on first use
}

func NewStore() *Store {
	return &Store{
		users:    map[string]User{},
		postByID: map[string]int{},
		sessions: map[string]string{},
	}
}

// NormalizeUsername lowercases and trims, then validates.
func NormalizeUsername(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !usernameRe.MatchString(s) {
		return "", ErrBadUsername
	}
	return s, nil
}

// Login returns the user with that username, creating it on first use, plus a new session
// token. There is no password: this is the "just introduce yourself" auth of the demo.
func (s *Store) Login(username string, now time.Time) (User, string, error) {
	name, err := NormalizeUsername(username)
	if err != nil {
		return User{}, "", err
	}
	token, err := newToken()
	if err != nil {
		return User{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[name]
	if !ok {
		u = User{ID: name, Username: name, AvatarColor: colorFor(name), CreatedAt: now.UTC()}
		s.users[name] = u
	}
	s.sessions[token] = u.ID
	return u, token, nil
}

func (s *Store) Logout(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

func (s *Store) UserByToken(token string) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.sessions[token]
	if !ok {
		return User{}, false
	}
	u, ok := s.users[id]
	return u, ok
}

func (s *Store) User(id string) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	return u, ok
}

// AddPost validates and stores a post. Content is trimmed; length counts characters, not bytes.
func (s *Store) AddPost(authorID, content string, now time.Time) (Post, error) {
	content = strings.TrimSpace(content)
	if n := utf8.RuneCountInString(content); n == 0 || n > MaxPostRunes {
		return Post{}, ErrBadContent
	}
	id, err := newToken()
	if err != nil {
		return Post{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[authorID]; !ok {
		return Post{}, ErrNoUser
	}
	p := Post{ID: id[:16], AuthorID: authorID, Content: content, CreatedAt: now.UTC()}
	s.postByID[p.ID] = len(s.posts)
	s.posts = append(s.posts, p)
	return p, nil
}

func (s *Store) Post(id string) (Post, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.postByID[id]
	if !ok {
		return Post{}, false
	}
	return s.posts[i], true
}

// Recent returns up to limit posts, newest first. This is the plain chronological timeline,
// not a recommendation: the ranked feed will come from WALRUS.
func (s *Store) Recent(limit int) []Post {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := len(s.posts)
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]Post, 0, limit)
	for i := n - 1; i >= n-limit; i-- {
		out = append(out, s.posts[i])
	}
	return out
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func colorFor(username string) string {
	h := fnv.New32a()
	h.Write([]byte(username))
	return avatarColors[h.Sum32()%uint32(len(avatarColors))]
}
