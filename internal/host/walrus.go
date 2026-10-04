package host

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/timurcravtov/demo-host-server/internal/walrus"
)

// Engine is the part of WALRUS this server uses: push the schema, ask for recommendations.
// *client.Client implements it; tests use a fake.
type Engine interface {
	PushSchema(ctx context.Context, yaml []byte, confirmBreaking bool) (*walrus.SchemaResult, error)
	Recommend(ctx context.Context, recommender string, req walrus.RecommendRequest) (*walrus.Response, error)
	Knobs(ctx context.Context, locale string) (*walrus.KnobCatalog, error)
	SendInteractions(ctx context.Context, events []walrus.Interaction) (*walrus.IngestResult, error)
}

// SyncSchema pushes the platform's schema to the engine, retrying while the engine is still
// starting. It returns when the push succeeded, the engine rejected the schema, or ctx ended.
// A schema is pushed, never pulled: the platform owns it.
func SyncSchema(ctx context.Context, e Engine, yaml []byte) error {
	delay := time.Second
	for {
		res, err := e.PushSchema(ctx, yaml, true)
		switch {
		case err == nil && res.OK:
			log.Printf("walrus: schema v%d applied", res.Version)
			return nil
		case err == nil:
			log.Printf("walrus: schema rejected: %s (%d problems)", res.Message, len(res.Errors))
			for _, is := range res.Errors {
				log.Printf("walrus:   %s: %s", is.Path, is.Message)
			}
			return errSchemaRejected
		case isFinal(err):
			log.Printf("walrus: schema push refused: %v", err)
			return err
		default:
			log.Printf("walrus: schema push failed (%v); retrying in %s", err, delay)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, 15*time.Second)
	}
}

var errSchemaRejected = errors.New("walrus rejected the schema")

// isFinal reports an answer that retrying will not change, such as a wrong key.
func isFinal(err error) bool {
	var e *walrus.Error
	return errors.As(err, &e) && e.Status >= 400 && e.Status < 500
}
