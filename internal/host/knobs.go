package host

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/timurcravtov/demo-host-server/internal/walrus"
)

// The Tune panel's data. What the knobs are comes from WALRUS (its schema); what a user chose is
// kept here, in the host, and sent with each request.

func (s *Server) routeKnobs() {
	s.mux.HandleFunc("GET /knobs", s.auth(s.knobConfig))
	s.mux.HandleFunc("GET /users/{id}/profile", s.auth(s.getProfile))
	s.mux.HandleFunc("PUT /users/{id}/profile", s.auth(s.putProfile))
}

// KnobDef, Preset and KnobConfig match the client's types (src/api/types.ts).
type KnobDef struct {
	ID        string      `json:"id"`
	Label     string      `json:"label"`
	Low       string      `json:"low"`
	High      string      `json:"high"`
	Min       float64     `json:"min"`
	Max       float64     `json:"max"`
	Default   float64     `json:"default"`
	Group     string      `json:"group,omitempty"`
	Help      string      `json:"help,omitempty"`
	DependsOn string      `json:"dependsOn,omitempty"`
	Kind      string      `json:"kind,omitempty"`
	Options   []OptionDef `json:"options,omitempty"`
	Scope     []string    `json:"scope"`
}

type OptionDef struct {
	Value float64 `json:"value"`
	Label string  `json:"label"`
}

type PresetDef struct {
	ID    string             `json:"id"`
	Label string             `json:"label"`
	Knobs map[string]float64 `json:"knobs"`
	Scope []string           `json:"scope"`
}

type KnobConfig struct {
	Knobs   []KnobDef   `json:"knobs"`
	Presets []PresetDef `json:"presets"`
}

type Profile struct {
	Knobs  map[string]float64 `json:"knobs"`
	Preset *string            `json:"preset"`
}

func (s *Server) knobConfig(w http.ResponseWriter, r *http.Request, _ User) {
	cat, ok := s.catalog(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toConfig(cat))
}

// catalog fetches the knob definitions from WALRUS, or answers for it.
func (s *Server) catalog(w http.ResponseWriter, r *http.Request) (*walrus.KnobCatalog, bool) {
	if s.engine == nil {
		writeError(w, http.StatusServiceUnavailable, "recommendations are not configured (set WALRUS_URL)")
		return nil, false
	}
	cat, err := s.engine.Knobs(r.Context(), r.URL.Query().Get("locale"))
	if err != nil {
		log.Printf("walrus knobs: %v", err)
		writeError(w, http.StatusBadGateway, "recommendations are unavailable right now")
		return nil, false
	}
	return cat, true
}

func toConfig(cat *walrus.KnobCatalog) KnobConfig {
	cfg := KnobConfig{Knobs: make([]KnobDef, 0, len(cat.Knobs)), Presets: make([]PresetDef, 0, len(cat.Presets))}
	offered := map[string][]string{}
	defaults := map[string]float64{}
	for _, k := range cat.Knobs {
		def := KnobDef{
			ID: k.ID, Label: k.Label, Low: k.Low, High: k.High, Min: k.Min, Max: k.Max, Default: k.Default,
			Group: k.Group, Help: k.Help, DependsOn: k.DependsOn, Scope: k.Recommenders,
		}
		if k.Kind != "" && k.Kind != "slider" {
			def.Kind = k.Kind
		}
		for _, o := range k.Options {
			def.Options = append(def.Options, OptionDef{Value: o.Value, Label: o.Label})
		}
		if def.Low == "" {
			def.Low = "Less"
		}
		if def.High == "" {
			def.High = "More"
		}
		cfg.Knobs = append(cfg.Knobs, def)
		offered[k.ID] = k.Recommenders
		defaults[k.ID] = k.Default
	}
	for _, p := range cat.Presets {
		// a preset names only the knobs it moves; the panel applies it as a whole, so fill in the rest
		values := map[string]float64{}
		for id, v := range defaults {
			values[id] = v
		}
		var scope []string
		for id, v := range p.Knobs {
			values[id] = v
			for _, rec := range offered[id] {
				if !slices.Contains(scope, rec) {
					scope = append(scope, rec)
				}
			}
		}
		slices.Sort(scope)
		cfg.Presets = append(cfg.Presets, PresetDef{ID: p.ID, Label: title(p.ID), Knobs: values, Scope: scope})
	}
	return cfg
}

// title turns discover_weekly into Discover weekly.
func title(id string) string {
	s := strings.ReplaceAll(id, "_", " ")
	for i, r := range s {
		return string(unicode.ToUpper(r)) + s[i+len(string(r)):]
	}
	return s
}

// tuneFor decides the knob values to send for one recommender: what the user saved, then what this
// request carries on top. The panel keeps a value for every knob the schema has, but a recommender
// accepts only the ones it offers, so the rest are left out. A knob the schema does not have is a
// mistake in the request.
func tuneFor(cat *walrus.KnobCatalog, recommender string, saved, asked map[string]float64) (map[string]float64, error) {
	offered := map[string]bool{}
	known := map[string]bool{}
	for _, k := range cat.Knobs {
		known[k.ID] = true
		if slices.Contains(k.Recommenders, recommender) {
			offered[k.ID] = true
		}
	}
	for id := range asked {
		if !known[id] {
			return nil, fmt.Errorf("unknown knob %q", id)
		}
	}
	out := map[string]float64{}
	for _, from := range []map[string]float64{saved, asked} {
		for id, v := range from {
			if offered[id] {
				out[id] = v
			}
		}
	}
	return out, nil
}

var knobIDRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

const maxProfileKnobs = 100

func (s *Server) getProfile(w http.ResponseWriter, r *http.Request, u User) {
	if r.PathValue("id") != u.ID {
		writeError(w, http.StatusForbidden, "that is not your profile")
		return
	}
	cat, ok := s.catalog(w, r)
	if !ok {
		return
	}
	p := s.store.Profile(u.ID)
	// start from the defaults, so the panel has a value for every knob
	knobs := map[string]float64{}
	for _, k := range cat.Knobs {
		knobs[k.ID] = k.Default
	}
	for id, v := range p.Knobs {
		if _, known := knobs[id]; known { // a knob dropped by a schema change is forgotten
			knobs[id] = v
		}
	}
	writeJSON(w, http.StatusOK, Profile{Knobs: knobs, Preset: p.Preset})
}

func (s *Server) putProfile(w http.ResponseWriter, r *http.Request, u User) {
	if r.PathValue("id") != u.ID {
		writeError(w, http.StatusForbidden, "that is not your profile")
		return
	}
	var in struct {
		Knobs  map[string]float64 `json:"knobs"`
		Preset *string            `json:"preset"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Knobs) > maxProfileKnobs {
		writeError(w, http.StatusBadRequest, "too many knobs")
		return
	}
	for id, v := range in.Knobs {
		if !knobIDRe.MatchString(id) || math.IsNaN(v) || math.IsInf(v, 0) {
			writeError(w, http.StatusBadRequest, "knob "+id+" must be a valid id with a finite number")
			return
		}
	}
	s.store.SaveProfile(u.ID, in.Knobs, in.Preset)
	w.WriteHeader(http.StatusNoContent)
}
