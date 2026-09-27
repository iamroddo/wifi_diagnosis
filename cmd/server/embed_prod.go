//go:build prod

package main

import (
	"embed"
	"io/fs"
)

//go:embed frontend/dist
var _embeddedFrontend embed.FS

var embeddedFrontend fs.FS

func init() { embeddedFrontend = fs.FS(_embeddedFrontend) }
