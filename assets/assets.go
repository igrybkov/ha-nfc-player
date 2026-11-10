// Package assets embeds static assets: Noto Sans Regular and Bold (OFL, see
// fonts/OFL.txt), with Cyrillic coverage.
//
// Owner: track B (docs/PLAN.md section 10).
package assets

import "embed"

// Fonts holds fonts/NotoSans-Regular.ttf and fonts/NotoSans-Bold.ttf.
//
//go:embed fonts/*.ttf
var Fonts embed.FS
