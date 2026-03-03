package admin

import "embed"

// staticFS embeds the admin CSS and htmx library directly into the Go binary.
//
// WHY go:embed (not http.FileServer on disk):
//   The binary must be self-contained — no dependency on a filesystem path at runtime.
//   Embedding means the admin UI works whether deployed as a Docker container or a bare
//   binary, with no risk of the static files being missing or mismatched at the deploy site.
//
// Path is relative to this source file (internal/api/admin/static.go).
// The static/ directory must be a sibling of this file. (RESEARCH.md Pitfall 6)
//
// DEPENDENCY: If files in static/ are moved or renamed, update this directive AND
// the http.FileServer call in router.go. The embed glob must match actual filenames.
//
// DEPENDENCY: any new file added to static/ must also be listed here.
//go:embed static/admin.css static/htmx.min.js static/admin.js
var staticFS embed.FS
