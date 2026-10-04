package host

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type client struct {
	t     *testing.T
	srv   *Server
	token string
}

func newClient(t *testing.T) *client {
	t.Helper()
	srv := NewServer(NewStore(), "")
	tick := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	srv.now = func() time.Time { tick = tick.Add(time.Second); return tick }
	return &client{t: t, srv: srv}
}

func (c *client) do(method, path, body string) *httptest.ResponseRecorder {
	c.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	rec := httptest.NewRecorder()
	c.srv.ServeHTTP(rec, req)
	return rec
}

func (c *client) login(username string) {
	c.t.Helper()
	rec := c.do("POST", "/auth/login", `{"username":"`+username+`"}`)
	if rec.Code != http.StatusOK {
		c.t.Fatalf("login %q: %d %s", username, rec.Code, rec.Body)
	}
	var out struct{ Token string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	c.token = out.Token
}

func TestLoginCreatesAndReusesUser(t *testing.T) {
	c := newClient(t)
	c.login("  Maya ") // trimmed and lowercased
	rec := c.do("GET", "/auth/me", "")
	var u UserView
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	if u.ID != "maya" || u.AvatarColor == "" {
		t.Fatalf("unexpected user: %+v", u)
	}
	first := c.token
	c.login("maya") // same user, new session
	if c.token == first {
		t.Fatal("each login should get its own token")
	}
	if c.do("GET", "/auth/me", "").Code != http.StatusOK {
		t.Fatal("second session should work")
	}
}

func TestLoginRejectsBadUsernames(t *testing.T) {
	c := newClient(t)
	for _, name := range []string{"", "ab", "has space", "way_too_long_username_here", "emoji😀", "a/b"} {
		body, _ := json.Marshal(map[string]string{"username": name})
		if rec := c.do("POST", "/auth/login", string(body)); rec.Code != http.StatusBadRequest {
			t.Errorf("username %q: got %d, want 400", name, rec.Code)
		}
	}
	if rec := c.do("POST", "/auth/login", `{"username":"maya","extra":1}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field should be rejected, got %d", rec.Code)
	}
}

func TestRoutesNeedSession(t *testing.T) {
	c := newClient(t)
	for _, r := range [][2]string{{"GET", "/auth/me"}, {"GET", "/posts"}, {"POST", "/posts"}, {"GET", "/posts/x"}} {
		if rec := c.do(r[0], r[1], `{"content":"hi"}`); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: got %d, want 401", r[0], r[1], rec.Code)
		}
	}
	c.token = "not-a-real-token"
	if c.do("GET", "/posts", "").Code != http.StatusUnauthorized {
		t.Error("bad token should be 401")
	}
}

func TestLogoutEndsSession(t *testing.T) {
	c := newClient(t)
	c.login("maya")
	if c.do("POST", "/auth/logout", "").Code != http.StatusNoContent {
		t.Fatal("logout should be 204")
	}
	if c.do("GET", "/auth/me", "").Code != http.StatusUnauthorized {
		t.Fatal("token should be dead after logout")
	}
}

func TestCreateAndListPosts(t *testing.T) {
	c := newClient(t)
	c.login("maya")
	rec := c.do("POST", "/posts", `{"content":"  first post  "}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var p PostView
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p.Content != "first post" || p.Author != "maya" || p.Handle != "maya" || p.ID == "" || p.Comments == nil {
		t.Fatalf("unexpected post: %+v", p)
	}

	c.login("kofi")
	c.do("POST", "/posts", `{"content":"second post"}`)

	rec = c.do("GET", "/posts", "")
	var list struct{ Posts []PostView }
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Posts) != 2 || list.Posts[0].Content != "second post" || list.Posts[1].Author != "maya" {
		t.Fatalf("want newest first: %+v", list.Posts)
	}

	rec = c.do("GET", "/posts?limit=1", "")
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Posts) != 1 {
		t.Fatalf("limit not applied: %d", len(list.Posts))
	}

	if rec := c.do("GET", "/posts/"+p.ID, ""); rec.Code != http.StatusOK {
		t.Fatalf("get one: %d", rec.Code)
	}
	if rec := c.do("GET", "/posts/missing", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing post: %d", rec.Code)
	}
}

func TestRelatedPosts(t *testing.T) {
	c := newClient(t)
	c.login("maya")
	var ids []string
	for _, text := range []string{"one", "two", "three"} {
		var p PostView
		_ = json.Unmarshal(c.do("POST", "/posts", `{"content":"`+text+`"}`).Body.Bytes(), &p)
		ids = append(ids, p.ID)
	}

	rec := c.do("GET", "/posts/"+ids[2]+"/related?limit=2", "")
	var out struct {
		Posts  []PostView
		Ranker string
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || out.Ranker != "placeholder" {
		t.Fatalf("related: %d %s", rec.Code, rec.Body)
	}
	if len(out.Posts) != 2 || out.Posts[0].Content != "two" || out.Posts[1].Content != "one" {
		t.Fatalf("want the other posts, never the post itself: %+v", out.Posts)
	}

	// The only post has no related posts; the field is [], not null.
	solo := newClient(t)
	solo.login("kofi")
	var p PostView
	_ = json.Unmarshal(solo.do("POST", "/posts", `{"content":"alone"}`).Body.Bytes(), &p)
	if body := solo.do("GET", "/posts/"+p.ID+"/related", "").Body.String(); !strings.Contains(body, `"posts":[]`) {
		t.Fatalf("empty related should be []: %s", body)
	}

	if c.do("GET", "/posts/missing/related", "").Code != http.StatusNotFound {
		t.Fatal("unknown post should be 404")
	}
	if c.do("GET", "/posts/"+ids[0]+"/related?limit=0", "").Code != http.StatusBadRequest {
		t.Fatal("bad limit should be 400")
	}
}

func TestPostValidation(t *testing.T) {
	c := newClient(t)
	c.login("maya")
	long := strings.Repeat("é", MaxPostRunes+1) // 281 characters, 562 bytes
	for name, content := range map[string]string{"empty": "", "blank": "   ", "too long": long} {
		body, _ := json.Marshal(map[string]string{"content": content})
		if rec := c.do("POST", "/posts", string(body)); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, rec.Code)
		}
	}
	exact := strings.Repeat("é", MaxPostRunes) // limit counts characters, not bytes
	body, _ := json.Marshal(map[string]string{"content": exact})
	if rec := c.do("POST", "/posts", string(body)); rec.Code != http.StatusCreated {
		t.Errorf("exactly 280 characters should pass, got %d", rec.Code)
	}
	if rec := c.do("GET", "/posts?limit=abc", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad limit: got %d", rec.Code)
	}
}

func TestCORS(t *testing.T) {
	srv := NewServer(NewStore(), "http://localhost:5173")
	req := httptest.NewRequest("OPTIONS", "/posts", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Fatalf("preflight: %d %v", rec.Code, rec.Header())
	}
}
