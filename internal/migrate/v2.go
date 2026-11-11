package migrate

import (
	"log/slog"

	"go.yaml.in/yaml/v3"

	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// v2to3 changes records.yaml:
//
//   - A record's absolute "volume" becomes "volume_offset", added to the
//     area's volume. An absolute volume can't become an offset without
//     knowing the room, so it becomes null (the area volume) and is
//     logged.
//   - Items "others: true" (every other record) are dropped: nobody wants
//     a tag that plays everything.
func v2to3(t *store.Tree) error {
	recs, v, ok := t.Get("records.yaml")
	if !ok || v != 2 {
		return nil
	}
	list := store.MapGet(recs, "records")
	if list == nil || list.Kind != yaml.SequenceNode {
		return nil
	}
	for _, r := range list.Content {
		if r.Kind != yaml.MappingNode {
			continue
		}
		id := v1Map{r}.str("id")
		for i := 0; i+1 < len(r.Content); i += 2 {
			if r.Content[i].Value != "volume" {
				continue
			}
			if old := r.Content[i+1]; old.Tag != "!!null" {
				slog.Warn("migrate: a record's volume is now an offset from the area volume; set it again", "record", id, "old_volume", old.Value)
			}
			r.Content[i].Value = "volume_offset"
			r.Content[i+1] = orNull(nil)
		}
		if items := store.MapGet(r, "items"); items != nil && items.Kind == yaml.SequenceNode {
			kept := items.Content[:0]
			for _, it := range items.Content {
				if (v1Map{it}).str("others") == "true" {
					slog.Warn("migrate: \"all other records\" items are gone; dropped", "record", id)
					continue
				}
				kept = append(kept, it)
			}
			items.Content = kept
		}
	}
	return nil
}
