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

	"github.com/maclir/peyk/internal/web"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	fb := web.Firebase{
		APIKey:     os.Getenv("FIREBASE_API_KEY"),
		AuthDomain: os.Getenv("FIREBASE_AUTH_DOMAIN"),
		ProjectID:  os.Getenv("GOOGLE_CLOUD_PROJECT"),
		AppID:      os.Getenv("FIREBASE_APP_ID"),
	}
	// firebase emulators:exec sets these for browser tests; Cloud Run never does.
	if auth, store := os.Getenv("FIREBASE_AUTH_EMULATOR_HOST"), os.Getenv("FIRESTORE_EMULATOR_HOST"); auth != "" && store != "" {
		fb.Emulators = &web.Emulators{Auth: auth, Firestore: store}
	}
	// make deploy sets REVISION to the commit it uploads; a local run has none.
	revision := os.Getenv("REVISION")
	if revision == "" {
		revision = "dev"
	}
	app := web.New(fb, revision)
	srv := &http.Server{Addr: ":" + port, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second}
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
