package templates

import "strings"

// sanitizeHTMLID converts an account or zone ID into a string safe for both
// HTML id attributes AND CSS id selectors used by htmx's hx-target.
//
// WHY needed:
//   Account IDs may contain dots (e.g. "eyodwa.org") and spaces (e.g. "primary - HE_username").
//   HTML id attributes forbid spaces entirely. CSS id selectors additionally parse dots
//   as class separators, so querySelector("#account-zones-eyodwa.org") silently fails.
//   Sanitizing the ID before embedding it in CSS selectors avoids this without
//   changing the displayed text (the original ID is always shown to the user).
//
//   A space in the hx-target selector is parsed as the CSS descendant combinator, so
//   hx-target="#account-zones-primary - HE_username" is a valid but wrong selector —
//   it looks for an element inside "#account-zones-primary" with class "HE_username",
//   finds nothing, and htmx silently does nothing. The button appears to not react at all.
//
// Replacements: . → -, : → -, @ → -, / → -, space → -
func sanitizeHTMLID(s string) string {
	return strings.NewReplacer(".", "-", ":", "-", "@", "-", "/", "-", " ", "-").Replace(s)
}
