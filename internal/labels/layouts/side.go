// Package layouts holds the built-in label layouts. Each registers itself
// with labels.RegisterLayout in init; blank-import this package to use them.
//
// Owner: track B (docs/PLAN.md section 10).
package layouts

import "github.com/igrybkov/ha-nfc-player/internal/labels"

func init() { labels.RegisterLayout(side{}) }

// side: square art flush left, text block vertically centered to the right.
type side struct{}

func (side) ID() string   { return "side" }
func (side) Name() string { return "Art left, text right" }

func (side) Draw(c labels.Canvas, cell labels.Rect, item labels.Item, bg labels.Background, t labels.Template) error {
	panic("TODO: track B")
}
