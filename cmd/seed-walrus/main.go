// Command seed-walrus fills a WALRUS engine with the demo library: it pushes the platform's schema,
// then sends every artist and track. It can be run again at any time; entities are replaced, not
// duplicated.
//
//	WALRUS_URL  base URL of the engine (default http://localhost:8080)
//	WALRUS_KEY  the key to send (required)
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/timurcravtov/demo-host-server/internal/host"
	"github.com/timurcravtov/demo-host-server/internal/walrus"
	"github.com/timurcravtov/demo-host-server/schema"
)

const batchSize = 50

func main() {
	url := os.Getenv("WALRUS_URL")
	if url == "" {
		url = "http://localhost:8080"
	}
	key := os.Getenv("WALRUS_KEY")
	if key == "" {
		log.Fatal("set WALRUS_KEY to the engine's key")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	engine := walrus.New(url, key)

	yaml, err := schema.Load()
	if err != nil {
		log.Fatalf("read schema: %v", err)
	}
	if err := host.SyncSchema(ctx, engine, yaml); err != nil {
		log.Fatalf("push schema: %v", err)
	}

	rejected := 0
	for _, set := range []struct {
		name     string
		entities []walrus.Entity
	}{
		{"artists", host.ArtistEntities()},
		{"tracks", host.TrackEntities()},
	} {
		accepted, bad, err := send(ctx, engine, set.entities)
		if err != nil {
			log.Fatalf("send %s: %v", set.name, err)
		}
		fmt.Printf("%-8s sent %d, stored %d, rejected %d\n", set.name, len(set.entities), accepted, bad)
		rejected += bad
	}

	counts, err := engine.EntityCounts(ctx)
	if err != nil {
		log.Fatalf("read counts: %v", err)
	}
	fmt.Printf("the engine now holds %d artists and %d tracks\n", counts["artist"], counts["track"])
	if rejected > 0 {
		os.Exit(1)
	}
}

// send uploads the entities in batches and prints why any were rejected.
func send(ctx context.Context, engine *walrus.Client, entities []walrus.Entity) (accepted, rejected int, err error) {
	for start := 0; start < len(entities); start += batchSize {
		end := min(start+batchSize, len(entities))
		res, err := engine.UpsertEntities(ctx, entities[start:end])
		if err != nil {
			return accepted, rejected, err
		}
		accepted += res.Accepted
		rejected += len(res.Rejected)
		for _, r := range res.Rejected {
			fmt.Printf("  rejected %s %s: %s\n", r.Entity, r.ID, r.Error)
		}
	}
	return accepted, rejected, nil
}
