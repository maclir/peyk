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

	"github.com/maclir/peyk/internal/analytics"
	"github.com/maclir/peyk/internal/store"
	"github.com/maclir/peyk/internal/web"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	s, err := openStore(ctx, projectID)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	events, reader := openAnalytics(ctx, projectID, os.Getenv("ANALYTICS_DATASET"))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{Addr: ":" + port, Handler: web.New(s, events, reader).Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on http://localhost:%s", port)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func openStore(ctx context.Context, projectID string) (store.Store, error) {
	if projectID == "" {
		log.Print("GOOGLE_CLOUD_PROJECT is empty; using ephemeral in-memory storage")
		return store.NewMemory(), nil
	}
	return store.NewFirestore(ctx, projectID)
}

// openAnalytics pairs the event writer with the reader the dashboard uses.
// Locally both are one in-memory warehouse, so the dashboard works without
// credentials. In production events go to stdout for the sink, and a missing
// dataset leaves the dashboard saying it cannot read rather than showing zeros.
func openAnalytics(ctx context.Context, projectID, dataset string) (*analytics.Logger, analytics.Reader) {
	if projectID == "" {
		m := analytics.NewMemory(time.Now)
		return analytics.NewLogger(m), m
	}
	events := analytics.NewLogger(os.Stdout)
	bq, err := analytics.NewBigQuery(ctx, projectID, dataset)
	if err != nil {
		log.Printf("analytics: %v; the dashboard will report the warehouse as unavailable", err)
		return events, unavailable{err}
	}
	return events, bq
}

type unavailable struct{ err error }

func (u unavailable) Read(context.Context, time.Time) (analytics.Snapshot, error) {
	return analytics.Snapshot{}, u.err
}
