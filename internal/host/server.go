package host

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxBody = 16 << 10

type Server struct {
	store  *Store
	now    func() time.Time
	cors   string // allowed origin for browsers calling directly; empty when behind the nginx proxy
	mux    *http.ServeMux
	engine Engine // WALRUS; nil when not configured
}

// NewServer wires the routes. corsOrigin may be empty.
// Option configures a Server.
type Option func(*Server)

// WithEngine connects the server to WALRUS for recommendations.
func WithEngine(e Engine) Option { return func(s *Server) { s.engine = e } }

func NewServer(store *Store, corsOrigin string, opts ...Option) *Server {
	s := &Server{store: store, now: time.Now, cors: corsOrigin, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /health", s.health)
	s.mux.HandleFunc("POST /auth/login", s.login)
	s.mux.HandleFunc("POST /auth/logout", s.auth(s.logout))
	s.mux.HandleFunc("GET /auth/me", s.auth(s.me))
	s.mux.HandleFunc("GET /posts", s.auth(s.listPosts))
	s.mux.HandleFunc("POST /posts", s.auth(s.createPost))
	s.mux.HandleFunc("GET /posts/{id}", s.auth(s.getPost))
	s.mux.HandleFunc("GET /posts/{id}/related", s.auth(s.relatedPosts))
	s.routeMusic()
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.cors != "" {
		w.Header().Set("Access-Control-Allow-Origin", s.cors)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

// JSON shapes. PostView matches the client's Post type (src/types/index.ts), so the client
// can render it as is; the counters and comments are placeholders until reactions exist.
type UserView struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	AvatarColor string `json:"avatarColor"`
}

type CommentView struct {
	ID          string `json:"id"`
	Author      string `json:"author"`
	AvatarColor string `json:"avatarColor"`
	Content     string `json:"content"`
	CreatedAt   string `json:"createdAt"`
}

type PostView struct {
	ID          string        `json:"id"`
	Author      string        `json:"author"`
	Handle      string        `json:"handle"`
	AvatarColor string        `json:"avatarColor"`
	Content     string        `json:"content"`
	CreatedAt   string        `json:"createdAt"`
	Likes       int           `json:"likes"`
	Dislikes    int           `json:"dislikes"`
	UserVote    *string       `json:"userVote"`
	Comments    []CommentView `json:"comments"`
}

func (s *Server) postView(p Post) PostView {
	u, _ := s.store.User(p.AuthorID)
	return PostView{
		ID: p.ID, Author: u.Username, Handle: u.Username, AvatarColor: u.AvatarColor,
		Content: p.Content, CreatedAt: p.CreatedAt.Format(time.RFC3339),
		Comments: []CommentView{},
	}
}

func userView(u User) UserView {
	return UserView{ID: u.ID, Username: u.Username, AvatarColor: u.AvatarColor}
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, token, err := s.store.Login(in.Username, s.now())
	if err != nil {
		if errors.Is(err, ErrBadUsername) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": userView(u)})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, _ User) {
	s.store.Logout(bearer(r))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request, u User) {
	writeJSON(w, http.StatusOK, userView(u))
}

func (s *Server) createPost(w http.ResponseWriter, r *http.Request, u User) {
	var in struct {
		Content string `json:"content"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := s.store.AddPost(u.ID, in.Content, s.now())
	if err != nil {
		if errors.Is(err, ErrBadContent) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.postView(p))
}

func (s *Server) listPosts(w http.ResponseWriter, r *http.Request, _ User) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(n, 100)
	}
	posts := s.store.Recent(limit)
	views := make([]PostView, len(posts))
	for i, p := range posts {
		views[i] = s.postView(p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"posts": views})
}

func (s *Server) getPost(w http.ResponseWriter, r *http.Request, _ User) {
	p, ok := s.store.Post(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "post not found")
		return
	}
	writeJSON(w, http.StatusOK, s.postView(p))
}

// relatedPosts answers "more like this" for the post page. PLACEHOLDER: WALRUS has no
// recommend endpoint yet, so this returns the newest other posts and says so in `ranker`.
// The ranking must come from WALRUS (item-to-item on the post), never from this server;
// when the engine is wired in, only this handler changes.
func (s *Server) relatedPosts(w http.ResponseWriter, r *http.Request, _ User) {
	id := r.PathValue("id")
	if _, ok := s.store.Post(id); !ok {
		writeError(w, http.StatusNotFound, "post not found")
		return
	}
	limit := 5
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(n, 20)
	}
	views := []PostView{}
	for _, p := range s.store.Recent(limit + 1) { // one extra: the post itself may be among them
		if p.ID == id || len(views) == limit {
			continue
		}
		views = append(views, s.postView(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"posts": views, "ranker": "placeholder"})
}

type authed func(http.ResponseWriter, *http.Request, User)

// auth resolves the bearer token to a user or answers 401.
func (s *Server) auth(next authed) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.store.UserByToken(bearer(r))
		if !ok {
			writeError(w, http.StatusUnauthorized, "sign in first")
			return
		}
		next(w, r, u)
	}
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if t, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(t)
	}
	return ""
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
