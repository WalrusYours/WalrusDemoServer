// Package schema holds the demo platform's WALRUS schema.
package schema

import (
	_ "embed"
	"os"
)

//go:embed music.yml
var music []byte

// Load returns the schema to push: the file PLATFORM_SCHEMA names if it is set, else the built-in
// music schema.
func Load() ([]byte, error) {
	if path := os.Getenv("PLATFORM_SCHEMA"); path != "" {
		return os.ReadFile(path)
	}
	return music, nil
}
