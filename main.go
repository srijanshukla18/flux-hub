package main

import (
	"log"
	"net/http"
)

func main() {
	app, err := NewApp()
	if err != nil {
		log.Fatal(err)
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
	mux.HandleFunc("/ui/summary", app.handleUISummary)
	mux.HandleFunc("/ui/sessions", app.handleUISessions)
	mux.HandleFunc("/ui/events", app.handleUIEvents)
	mux.HandleFunc("/ui/session-body", app.handleUISessionBody)
	mux.HandleFunc("/session", app.handleSessionPage)

	log.Printf("flux-hub listening on %s", app.listenAddr)
	if err := http.ListenAndServe(app.listenAddr, requestLogger(mux)); err != nil {
		log.Fatal(err)
	}
}
