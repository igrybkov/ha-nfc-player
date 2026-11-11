package migrate

import (
	"log/slog"

	"go.yaml.in/yaml/v3"

	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// v4to5 turns label backgrounds into themes:
//
//   - backgrounds.yaml becomes themes.yaml, its "backgrounds" list
//     "themes". Each theme may now set, per label format (small, large,
//     dot), where the art goes and whether it is gray; the old ones set
//     nothing and get each format's default. When themes.yaml already
//     exists, it wins and backgrounds.yaml is dropped.
//   - A template's "layout" becomes its "format": "full" printed art
//     behind the text, the default of small labels; "side" printed art on
//     the left, the default of large labels. So every label prints as
//     before.
//   - settings print.default_background becomes print.default_theme.
func v4to5(t *store.Tree) error {
	if n, v, ok := t.Get("backgrounds.yaml"); ok && v == 4 {
		t.Delete("backgrounds.yaml")
		if _, _, exists := t.Get("themes.yaml"); exists {
			slog.Warn("migrate: themes.yaml exists; backgrounds.yaml is ignored and goes on the next save")
		} else {
			renameKey(n, "backgrounds", "themes")
			t.Put("themes.yaml", n)
		}
	}

	if n, v, ok := t.Get("templates.yaml"); ok && v == 4 {
		if list := store.MapGet(n, "templates"); list != nil && list.Kind == yaml.SequenceNode {
			for _, tpl := range list.Content {
				if tpl.Kind != yaml.MappingNode {
					continue
				}
				format := "large"
				switch layout := (v1Map{tpl}).str("layout"); layout {
				case "full":
					format = "small"
				case "side", "":
				default:
					slog.Warn("migrate: unknown template layout; the template is now a large label", "template", (v1Map{tpl}).str("id"), "layout", layout)
				}
				for i := 0; i+1 < len(tpl.Content); i += 2 {
					if tpl.Content[i].Value == "layout" {
						tpl.Content[i].Value = "format"
						tpl.Content[i+1] = str(format)
					}
				}
				if store.MapGet(tpl, "format") == nil {
					store.MapSet(tpl, "format", str(format))
				}
			}
		}
	}

	if n, v, ok := t.Get("settings.yaml"); ok && v == 4 {
		if p := store.MapGet(n, "print"); p != nil && p.Kind == yaml.MappingNode {
			renameKey(p, "default_background", "default_theme")
		}
	}
	return nil
}

// renameKey renames a key of a mapping node in place, keeping its value,
// position and comments.
func renameKey(m *yaml.Node, from, to string) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == from {
			m.Content[i].Value = to
		}
	}
}
