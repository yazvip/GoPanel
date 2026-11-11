package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed index.html assets locales
var content embed.FS

func FS() fs.FS {
	sub, err := fs.Sub(content, ".")
	if err != nil {
		panic(err)
	}
	return sub
}

func Handler() http.Handler {
	return http.FileServerFS(FS())
}
