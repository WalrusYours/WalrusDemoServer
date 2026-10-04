package main

import (
	_ "embed"
	"os"
)

// The demo platform's own schema. PLATFORM_SCHEMA points at another file to use instead.
//
//go:embed schema/music.yml
var defaultSchema []byte

func loadSchema() ([]byte, error) {
	if path := os.Getenv("PLATFORM_SCHEMA"); path != "" {
		return os.ReadFile(path)
	}
	return defaultSchema, nil
}
