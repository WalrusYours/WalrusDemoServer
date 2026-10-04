package walrus

import "testing"

func TestKeyFor(t *testing.T) {
	for _, c := range []struct{ url, key, want string }{
		{"http://localhost:8080", "", "key"},
		{"http://LOCALHOST:8080/", "", "key"},
		{"http://127.0.0.1:8080", "", "key"},
		{"http://[::1]:8080", "", "key"},
		{"http://localhost:8080", "secret", "secret"}, // a key that was given always wins
		{"http://walrus:8080", "", ""},                // another machine: never guess a key
		{"https://walrus.example.com", "", ""},
		{"http://10.0.0.5:8080", "", ""},
		{"not a url", "", ""},
	} {
		if got := KeyFor(c.url, c.key); got != c.want {
			t.Errorf("KeyFor(%q, %q) = %q, want %q", c.url, c.key, got, c.want)
		}
	}
}
