// Package walrus is this server's own small HTTP client for a WALRUS engine: push the schema,
// send entities, ask for recommendations. It speaks the engine's public /v1 API and shares no code
// with it; any platform in any language would do the same.
package walrus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to one WALRUS engine. It is safe for concurrent use.
type Client struct {
	base string
	key  string
	http *http.Client
}

// New returns a client for the engine at baseURL (for example http://walrus:8080), sending key as
// a bearer token.
func New(baseURL, key string) *Client {
	return &Client{
		base: strings.TrimRight(baseURL, "/"),
		key:  key,
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

// Error is an error answer from the engine. Code is the machine-readable code, for example
// "seed_mismatch" or "schema_missing".
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("walrus: %d %s: %s", e.Status, e.Code, e.Message)
}

// IsCode reports whether err is an engine error with that code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// RecommendRequest is what a recommend call carries. Set the seed field the recommender's
// `seed:` names and leave the others empty. Items is sent when it is not nil, so an empty,
// non-nil slice means "an empty playlist", which a recommender can answer with a fallback.
type RecommendRequest struct {
	User    string
	Item    string
	Items   []string
	Session []string
	Users   []string

	Context map[string]any
	Limit   int
	Exclude []string
	Knobs   map[string]float64
	Preset  string
	Locale  string
	Explain bool
}

func (r RecommendRequest) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	set := func(k string, v any, ok bool) {
		if ok {
			m[k] = v
		}
	}
	set("user", r.User, r.User != "")
	set("item", r.Item, r.Item != "")
	set("items", orEmpty(r.Items), r.Items != nil)
	set("session", orEmpty(r.Session), r.Session != nil)
	set("users", orEmpty(r.Users), r.Users != nil)
	set("context", r.Context, len(r.Context) > 0)
	set("limit", r.Limit, r.Limit > 0)
	set("exclude", r.Exclude, len(r.Exclude) > 0)
	set("knobs", r.Knobs, len(r.Knobs) > 0)
	set("preset", r.Preset, r.Preset != "")
	set("locale", r.Locale, r.Locale != "")
	set("explain", true, r.Explain)
	return json.Marshal(m)
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

type Item struct {
	ID     string  `json:"id"`
	Type   string  `json:"type"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason,omitempty"`
}

// Response is a recommendation. Items are in the order to show them; do not re-sort.
type Response struct {
	Recommender string `json:"recommender"`
	// Used differs from Recommender when a fallback answered.
	Used                 string             `json:"used"`
	RecID                string             `json:"rec_id"`
	Items                []Item             `json:"items"`
	Weights              map[string]float64 `json:"weights"`
	Knobs                map[string]float64 `json:"knobs"`
	CandidatesConsidered int                `json:"candidates_considered"`
	TookMS               float64            `json:"took_ms"`
}

// Recommend asks a named recommender of the pushed schema for a list.
func (c *Client) Recommend(ctx context.Context, recommender string, req RecommendRequest) (*Response, error) {
	var out Response
	path := "/v1/recommenders/" + url.PathEscape(recommender) + "/recommend"
	if err := c.do(ctx, http.MethodPost, path, "application/json", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// SchemaResult is the engine's answer to a schema push.
type SchemaResult struct {
	OK           bool    `json:"ok"`
	Errors       []Issue `json:"errors"`
	Version      int     `json:"version"`
	NeedsConfirm bool    `json:"needsConfirm"`
	Message      string  `json:"message"`
}

// PushSchema sends the YAML schema. The engine validates it and applies it. A schema it rejects
// is not an error value: check SchemaResult.OK and Errors. A breaking change is applied only when
// confirmBreaking is true; otherwise NeedsConfirm is set.
func (c *Client) PushSchema(ctx context.Context, yaml []byte, confirmBreaking bool) (*SchemaResult, error) {
	path := "/v1/schema"
	if confirmBreaking {
		path += "?confirm_breaking=true"
	}
	status, raw, err := c.send(ctx, http.MethodPut, path, "application/yaml", yaml)
	if err != nil {
		return nil, err
	}
	// 200 applied, 400 rejected and 409 needs confirming all answer with a result
	if status == http.StatusOK || status == http.StatusBadRequest || status == http.StatusConflict {
		var out SchemaResult
		if json.Unmarshal(raw, &out) == nil && (out.Message != "" || out.OK) {
			return &out, nil
		}
	}
	return nil, apiError(status, raw)
}

type Entity struct {
	Entity     string         `json:"entity"`
	ID         string         `json:"id"`
	Attributes map[string]any `json:"attributes"`
}

type Rejection struct {
	Index  int    `json:"index"`
	Entity string `json:"entity"`
	ID     string `json:"id"`
	Error  string `json:"error"`
}

type IngestResult struct {
	Accepted int         `json:"accepted"`
	Rejected []Rejection `json:"rejected"`
}

// UpsertEntities sends one batch (the engine takes up to 1000). Entities the engine rejects come
// back in IngestResult.Rejected; the rest are stored.
func (c *Client) UpsertEntities(ctx context.Context, entities []Entity) (*IngestResult, error) {
	var out IngestResult
	body := struct {
		Entities []Entity `json:"entities"`
	}{entities}
	if err := c.do(ctx, http.MethodPost, "/v1/entities", "application/json", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type KnobOption struct {
	Value float64 `json:"value"`
	Label string  `json:"label"`
}

type Knob struct {
	ID        string       `json:"id"`
	Kind      string       `json:"kind"`
	Label     string       `json:"label"`
	Low       string       `json:"low"`
	High      string       `json:"high"`
	Group     string       `json:"group"`
	Help      string       `json:"help"`
	Min       float64      `json:"min"`
	Max       float64      `json:"max"`
	Default   float64      `json:"default"`
	DependsOn string       `json:"depends_on"`
	Options   []KnobOption `json:"options"`
	// Recommenders are the recommenders that offer this knob.
	Recommenders []string `json:"recommenders"`
}

type Preset struct {
	ID    string             `json:"id"`
	Knobs map[string]float64 `json:"knobs"`
}

type KnobCatalog struct {
	Knobs   []Knob   `json:"knobs"`
	Presets []Preset `json:"presets"`
}

// Knobs returns the knobs and presets of the active schema, with texts in the given locale (the
// schema's own when empty or not offered).
func (c *Client) Knobs(ctx context.Context, locale string) (*KnobCatalog, error) {
	path := "/v1/schema/knobs"
	if locale != "" {
		path += "?locale=" + url.QueryEscape(locale)
	}
	var out KnobCatalog
	if err := c.do(ctx, http.MethodGet, path, "", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type Interaction struct {
	User   string            `json:"user"`
	Type   string            `json:"type"`
	Target string            `json:"target"`
	TS     string            `json:"ts,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
}

// SendInteractions sends one batch of events (up to 1000). Events the engine rejects come back in
// IngestResult.Rejected.
func (c *Client) SendInteractions(ctx context.Context, events []Interaction) (*IngestResult, error) {
	var out IngestResult
	body := struct {
		Interactions []Interaction `json:"interactions"`
	}{events}
	if err := c.do(ctx, http.MethodPost, "/v1/interactions", "application/json", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EntityCounts returns how many entities of each type the engine holds.
func (c *Client) EntityCounts(ctx context.Context) (map[string]int, error) {
	var out struct {
		Counts map[string]int `json:"counts"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/entities", "", nil, &out); err != nil {
		return nil, err
	}
	return out.Counts, nil
}

// do sends one request and decodes a 2xx answer into out. Anything else becomes *Error.
func (c *Client) do(ctx context.Context, method, path, contentType string, body, out any) error {
	status, raw, err := c.send(ctx, method, path, contentType, body)
	if err != nil {
		return err
	}
	if status >= 200 && status < 300 {
		if out == nil || len(raw) == 0 {
			return nil
		}
		return json.Unmarshal(raw, out)
	}
	return apiError(status, raw)
}

// send does the HTTP exchange. A body that is []byte goes as is; anything else is JSON-encoded.
func (c *Client) send(ctx context.Context, method, path, contentType string, body any) (int, []byte, error) {
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	return res.StatusCode, raw, err
}

func apiError(status int, raw []byte) *Error {
	e := &Error{Status: status, Code: fmt.Sprintf("http_%d", status), Message: http.StatusText(status)}
	var b struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &b) == nil && b.Error.Code != "" {
		e.Code, e.Message = b.Error.Code, b.Error.Message
	}
	return e
}
