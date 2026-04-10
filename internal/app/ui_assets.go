package app

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var embeddedStaticFiles embed.FS

func staticAssetHandler() http.Handler {
	sub, err := fs.Sub(embeddedStaticFiles, "static")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}
