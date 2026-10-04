package walrus

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeEngine answers like the real one does, and records the last request.
type fakeEngine struct {
	srv      *httptest.Server
	method   string
	path     string
	auth     string
	ctype    string
	body     []byte
	status   int
	response string
}

func start(t *testing.T) (*Client, *fakeEngine) {
	t.Helper()
	f := &fakeEngine{status: 200}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.method, f.path, f.auth, f.ctype = r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		f.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, f.response)
	}))
	t.Cleanup(f.srv.Close)
	return New(f.srv.URL+"/", "the-key"), f // a trailing slash is tolerated
}

func TestRecommendSendsTheRequestAndReadsTheAnswer(t *testing.T) {
	c, f := start(t)
	f.response = `{"recommender":"playlist_add","used":"from_title","rec_id":"rec_1","candidates_considered":9,"took_ms":1.5,
		"items":[{"id":"holocene","type":"track","score":0.8,"reason":"Sounds like Skinny Love"}]}`

	res, err := c.Recommend(context.Background(), "playlist add", RecommendRequest{
		User: "maya", Items: []string{"a", "b"}, Limit: 3, Explain: true,
		Knobs: map[string]float64{"vibe_vs_branch_out": 1}, Context: map[string]any{"title_words": []string{"road"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Used != "from_title" || len(res.Items) != 1 || res.Items[0].Reason == "" || res.CandidatesConsidered != 9 || res.TookMS != 1.5 {
		t.Errorf("response = %+v", res)
	}
	if f.method != "POST" || f.path != "/v1/recommenders/playlist%20add/recommend" || f.auth != "Bearer the-key" || f.ctype != "application/json" {
		t.Errorf("sent %s %s auth=%q type=%q", f.method, f.path, f.auth, f.ctype)
	}
	var sent map[string]any
	_ = json.Unmarshal(f.body, &sent)
	if sent["user"] != "maya" || sent["limit"] != 3.0 || sent["explain"] != true || len(sent["items"].([]any)) != 2 {
		t.Errorf("body = %s", f.body)
	}
}

func TestAnEmptyListIsSentNotOmitted(t *testing.T) {
	for _, c := range []struct {
		req  RecommendRequest
		want string
	}{
		{RecommendRequest{Items: []string{}}, `{"items":[]}`},
		{RecommendRequest{Items: nil, User: "mara", Limit: 5}, `{"limit":5,"user":"mara"}`},
		{RecommendRequest{Session: []string{}}, `{"session":[]}`},
	} {
		raw, _ := json.Marshal(c.req)
		if string(raw) != c.want {
			t.Errorf("got %s, want %s", raw, c.want)
		}
	}
}

func TestErrorsCarryTheEnginesCode(t *testing.T) {
	c, f := start(t)
	f.status = 400
	f.response = `{"error":{"code":"seed_mismatch","message":"playlist_add takes items, not user"}}`
	_, err := c.Recommend(context.Background(), "playlist_add", RecommendRequest{User: "mara"})
	var e *Error
	if !errors.As(err, &e) || e.Status != 400 || e.Code != "seed_mismatch" || e.Message == "" || !IsCode(err, "seed_mismatch") {
		t.Fatalf("got %v", err)
	}

	f.status, f.response = 502, "<html>bad gateway</html>"
	_, err = c.Recommend(context.Background(), "playlist_add", RecommendRequest{})
	if !errors.As(err, &e) || e.Status != 502 || e.Code != "http_502" {
		t.Errorf("a non-JSON failure still becomes an Error: %v", err)
	}
}

func TestPushSchema(t *testing.T) {
	c, f := start(t)
	f.response = `{"ok":true,"version":3,"message":"applied"}`
	res, err := c.PushSchema(context.Background(), []byte("version: 1\n"), true)
	if err != nil || !res.OK || res.Version != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	if f.method != "PUT" || f.path != "/v1/schema?confirm_breaking=true" || f.ctype != "application/yaml" || string(f.body) != "version: 1\n" {
		t.Errorf("sent %s %s type=%q body=%q", f.method, f.path, f.ctype, f.body)
	}

	// a rejected schema is a result with its issues, not an error
	f.status = 400
	f.response = `{"ok":false,"message":"2 problems","errors":[{"path":"signals.a","message":"unknown type"}]}`
	res, err = c.PushSchema(context.Background(), []byte("x"), false)
	if err != nil || res.OK || len(res.Errors) != 1 || res.Errors[0].Path != "signals.a" || f.path != "/v1/schema" {
		t.Errorf("rejected: %+v %v (sent %s)", res, err, f.path)
	}

	f.status = 409
	f.response = `{"ok":false,"needsConfirm":true,"message":"breaking"}`
	if res, err = c.PushSchema(context.Background(), []byte("x"), false); err != nil || !res.NeedsConfirm {
		t.Errorf("needs confirm: %+v %v", res, err)
	}

	// anything else is an error
	f.status, f.response = 401, `{"error":{"code":"unauthorized","message":"wrong key"}}`
	if _, err = c.PushSchema(context.Background(), []byte("x"), false); !IsCode(err, "unauthorized") {
		t.Errorf("got %v", err)
	}
}
