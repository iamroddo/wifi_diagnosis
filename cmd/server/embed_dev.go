//go:build !prod

package main

import "io/fs"

// embeddedFrontend is nil in dev builds; the server falls back to
// serving from frontend/dist on disk if present.
var embeddedFrontend fs.FS
