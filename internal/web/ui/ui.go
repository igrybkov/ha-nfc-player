// Package ui embeds the static UI (HTML, CSS, vanilla JS modules).
//
// Owner: track C (docs/PLAN.md section 10).
package ui

import "embed"

// FS holds the UI files. Use relative URLs only (Ingress).
//
//go:embed index.html *.css *.js
var FS embed.FS
