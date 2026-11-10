package layouts

import "github.com/igrybkov/ha-nfc-player/internal/labels"

func init() { labels.RegisterLayout(full{}) }

// full: art fills the label, gradient fade under the text lines at the bottom.
type full struct{}

func (full) ID() string   { return "full" }
func (full) Name() string { return "Full art" }

func (full) Draw(c labels.Canvas, cell labels.Rect, item labels.Item, bg labels.Background, t labels.Template) error {
	panic("TODO: track B")
}
