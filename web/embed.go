// Package web embeds the built frontend (run `npm run build` in this
// directory before compiling the server).
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
