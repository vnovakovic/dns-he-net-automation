package templates

import "strings"

// sanitizeHTMLID converts an account or zone ID into a string safe for both
// HTML id attributes AND CSS id selectors used by htmx's hx-target.
//
// WHY needed:
//   Account IDs may contain dots (e.g. "eyodwa.org"). HTML id attributes allow dots,
//   but CSS id selectors do not — a dot is parsed as a class separator, so
//   querySelector("#account-zones-eyodwa.org") silently fails to find the element.
//   Sanitizing the ID before embedding it in CSS selectors avoids this without
//   changing the displayed text (the original ID is always shown to the user).
//
// Replacements: . → -, : → -, @ → -, / → -
func sanitizeHTMLID(s string) string {
	return strings.NewReplacer(".", "-", ":", "-", "@", "-", "/", "-").Replace(s)
}
