// Command demo-host-server is the demo platform's backend. Env:
//
//	PLATFORM_ADDR         listen address (default :8081, the port the client's nginx proxies /api to)
//	PLATFORM_CORS_ORIGIN  optional; only when a browser calls it directly instead of through the proxy
//	WALRUS_URL            base URL of the WALRUS engine; without it recommendations answer 503
//	WALRUS_KEY            the key sent to the engine
//	PLATFORM_SCHEMA       optional path to a schema to push instead of the built-in one
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/timurcravtov/demo-host-server/internal/host"
	"github.com/timurcravtov/walrus/pkg/client"
)

func main() {
	addr := env("PLATFORM_ADDR", ":8081")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var opts []host.Option
	if url := os.Getenv("WALRUS_URL"); url != "" {
		engine := client.New(url, os.Getenv("WALRUS_KEY"))
		opts = append(opts, host.WithEngine(engine))
		yaml, err := loadSchema()
		if err != nil {
			log.Fatalf("read schema: %v", err)
		}
		// in the background, so the server is up even while the engine is still starting
		go func() {
			if err := host.SyncSchema(ctx, engine, yaml); err != nil {
				log.Printf("schema not synced: %v", err)
			}
		}()
	} else {
		log.Printf("WALRUS_URL is not set: recommendations are off")
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           host.NewServer(host.NewStore(), os.Getenv("PLATFORM_CORS_ORIGIN"), opts...),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	log.Printf("demo-host-server listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
