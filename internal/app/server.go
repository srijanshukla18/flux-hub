package app

import (
	"fmt"
	"log"
	"net/http"
)

func Run() error {
	app, err := NewApp()
	if err != nil {
		return err
	}
	defer func() {
		if err := app.Close(); err != nil {
			log.Printf("app close error: %v", err)
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", staticAssetHandler()))
	mux.HandleFunc("/", app.handleRoot)
	mux.HandleFunc("/healthz", app.handleHealthz)
	mux.HandleFunc("/webhook", app.handleWebhook)
	mux.HandleFunc("/ui/status", app.handleUIStatus)

	log.Printf("flux-hub listening on %s", app.listenAddr)
	if err := http.ListenAndServe(app.listenAddr, requestLogger(mux)); err != nil {
		return fmt.Errorf("listen and serve: %w", err)
	}
	return nil
}
