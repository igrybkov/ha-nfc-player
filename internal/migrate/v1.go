package migrate

import (
	"fmt"
	"log/slog"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/igrybkov/ha-nfc-player/internal/store"
)

// v1to2 merges tag entries (tags.yaml) and pools (pools.yaml) into records
// (records.yaml), and wraps templates.yaml and backgrounds.yaml, bare lists
// in v1, into mappings that can hold a version.
//
// A record is an HA tag name and the items it plays: media, another record,
// or all other records. The rules:
//
//   - An entry with a URL becomes a record with that one media item. Its
//     shuffle applies to every kind, so it plays as before.
//   - A pool becomes a record. Its tag items point to their entries'
//     records. Pool shuffle true becomes the per-kind defaults (albums in
//     order, the rest shuffled); false turns shuffle off for every kind.
//   - An entry that plays a pool of the same name merges with it: one
//     record with the pool's items, and the entry's volume, player and
//     label where set, as they won before.
//   - Any other entry that plays a pool becomes a record with one item:
//     that pool's record, or all other records for the built-in "all".
//   - Old ids stay as aliases ("tag:<id>", "pool:<id>"), so play events with
//     tag: or pool: keep working. A pool id taken by an entry gets "-pool".
//     A pool whose name an entry already matched gets " (pool)", since the
//     entry won that name match before. Linked HA tag ids are dropped:
//     records match by name.
//
// When records.yaml already exists (a crash between writes, a git pull),
// it wins and tags.yaml and pools.yaml are dropped unread.
func v1to2(t *store.Tree) error {
	for _, f := range [][2]string{{"templates.yaml", "templates"}, {"backgrounds.yaml", "backgrounds"}} {
		if n, v, ok := t.Get(f[0]); ok && v == 1 && n.Kind == yaml.SequenceNode {
			m := mapping()
			store.MapSet(m, f[1], n)
			t.Put(f[0], m)
		}
	}

	tags, tv, hasTags := t.Get("tags.yaml")
	pools, pv, hasPools := t.Get("pools.yaml")
	if (!hasTags || tv != 1) && (!hasPools || pv != 1) {
		return nil
	}
	if _, _, ok := t.Get("records.yaml"); ok {
		slog.Warn("records.yaml exists; tags.yaml and pools.yaml are ignored and go on the next save")
		t.Delete("tags.yaml")
		t.Delete("pools.yaml")
		return nil
	}

	var entries, poolList []v1Map
	if hasTags {
		entries = list(store.MapGet(tags, "tags"))
	}
	if hasPools {
		poolList = list(store.MapGet(pools, "pools"))
	}

	// Ids first, so items can point to records in any order.
	used := map[string]bool{}
	entryRec := map[string]string{} // entry id → record id
	poolRec := map[string]string{}  // pool id → record id
	poolByID := map[string]v1Map{}
	for _, p := range poolList {
		poolByID[p.str("id")] = p
	}
	merged := map[string]bool{} // pool ids merged into an entry
	for _, e := range entries {
		id := e.str("id")
		if pid := e.str("pool"); pid != "" {
			if p, ok := poolByID[pid]; ok && normalize(p.str("name")) == normalize(e.str("name")) {
				merged[pid] = true
			}
		}
		entryRec[id] = unique(id, used)
	}
	for _, p := range poolList {
		id := p.str("id")
		if merged[id] {
			for _, e := range entries {
				if e.str("pool") == id && normalize(e.str("name")) == normalize(p.str("name")) {
					poolRec[id] = entryRec[e.str("id")]
					break
				}
			}
			continue
		}
		rid := id
		if used[rid] {
			rid = unique(id+"-pool", used)
		} else {
			used[rid] = true
		}
		poolRec[id] = rid
	}

	entryNames := map[string]bool{}
	for _, e := range entries {
		entryNames[normalize(e.str("name"))] = true
	}

	records := &yaml.Node{Kind: yaml.SequenceNode}
	for _, e := range entries {
		id, pid := e.str("id"), e.str("pool")
		r := v1Record{id: entryRec[id], name: e.str("name"), aliases: []string{"tag:" + id}}
		switch p, isPool := poolByID[pid]; {
		case pid != "" && merged[pid]:
			r.aliases = append(r.aliases, "pool:"+pid)
			r.items = poolItems(p, entryRec)
			r.shuffle = poolShuffle(p)
			r.repeat = p.node("repeat")
			r.volume = firstSet(e.node("volume"), p.node("volume"))
			r.player = firstSet(e.node("player"), p.node("player"))
			r.label = mergeLabels(e.node("label"), p.node("label"))
		case pid == "all":
			r.items = []*yaml.Node{item("others", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})}
			r.volume, r.player, r.label = e.node("volume"), e.node("player"), e.node("label")
		case pid != "":
			if !isPool {
				slog.Warn("migrate: entry plays a pool that does not exist; it gets no items", "entry", id, "pool", pid)
			} else {
				r.items = []*yaml.Node{item("record", str(poolRec[pid]))}
			}
			r.volume, r.player, r.label = e.node("volume"), e.node("player"), e.node("label")
		default:
			if url := e.str("url"); url != "" {
				r.items = []*yaml.Node{item("media", str(url))}
			}
			r.shuffle = allKinds(e.node("shuffle"))
			r.repeat = e.node("repeat")
			r.volume, r.player, r.label = e.node("volume"), e.node("player"), e.node("label")
		}
		records.Content = append(records.Content, r.node())
	}
	for _, p := range poolList {
		id := p.str("id")
		if merged[id] {
			continue
		}
		name := p.str("name")
		if entryNames[normalize(name)] {
			slog.Warn("migrate: a tag entry had this pool's name; the pool is renamed", "pool", id, "name", name+" (pool)")
			name += " (pool)"
		}
		records.Content = append(records.Content, v1Record{
			id: poolRec[id], name: name, aliases: []string{"pool:" + id},
			items: poolItems(p, entryRec), shuffle: poolShuffle(p),
			repeat: p.node("repeat"), volume: p.node("volume"), player: p.node("player"), label: p.node("label"),
		}.node())
	}

	out := mapping()
	store.MapSet(out, "records", records)
	t.Put("records.yaml", out)
	t.Delete("tags.yaml")
	t.Delete("pools.yaml")
	return nil
}

// v1Map is a v1 mapping node, read by key.
type v1Map struct{ n *yaml.Node }

func (m v1Map) node(key string) *yaml.Node { return store.MapGet(m.n, key) }

func (m v1Map) str(key string) string {
	if n := m.node(key); n != nil && n.Kind == yaml.ScalarNode && n.Tag != "!!null" {
		return n.Value
	}
	return ""
}

func list(n *yaml.Node) []v1Map {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]v1Map, 0, len(n.Content))
	for _, c := range n.Content {
		if c.Kind == yaml.MappingNode {
			out = append(out, v1Map{c})
		}
	}
	return out
}

// v1Record is a v2 record as the step builds it. Nil nodes are written as null.
type v1Record struct {
	id, name                      string
	aliases                       []string
	items                         []*yaml.Node
	shuffle                       *yaml.Node
	repeat, volume, player, label *yaml.Node
}

func (r v1Record) node() *yaml.Node {
	m := mapping()
	store.MapSet(m, "id", str(r.id))
	store.MapSet(m, "name", str(r.name))
	aliases := &yaml.Node{Kind: yaml.SequenceNode}
	for _, a := range r.aliases {
		aliases.Content = append(aliases.Content, str(a))
	}
	store.MapSet(m, "aliases", aliases)
	store.MapSet(m, "items", &yaml.Node{Kind: yaml.SequenceNode, Content: r.items})
	shuffle := r.shuffle
	if shuffle == nil {
		shuffle = allKinds(nil)
	}
	store.MapSet(m, "shuffle", shuffle)
	store.MapSet(m, "repeat", orNull(r.repeat))
	store.MapSet(m, "volume", orNull(r.volume))
	store.MapSet(m, "player", orNull(r.player))
	store.MapSet(m, "label", label(r.label))
	return m
}

// shuffleKinds are the keys of a v2 record's shuffle mapping.
var shuffleKinds = []string{"albums", "artists", "playlists", "other"}

// allKinds sets every kind to v; nil (or null) leaves every kind default.
func allKinds(v *yaml.Node) *yaml.Node {
	m := mapping()
	for _, k := range shuffleKinds {
		store.MapSet(m, k, orNull(v))
	}
	return m
}

func poolShuffle(p v1Map) *yaml.Node {
	if v := p.node("shuffle"); v != nil && v.Tag == "!!bool" && v.Value == "false" {
		return allKinds(v)
	}
	return allKinds(nil)
}

func poolItems(p v1Map, entryRec map[string]string) []*yaml.Node {
	var out []*yaml.Node
	for _, it := range list(p.node("items")) {
		switch {
		case it.str("tag") != "":
			rid, ok := entryRec[it.str("tag")]
			if !ok {
				slog.Warn("migrate: pool item names a missing tag entry; dropped", "pool", p.str("id"), "tag", it.str("tag"))
				continue
			}
			out = append(out, item("record", str(rid)))
		case it.str("media") != "":
			out = append(out, item("media", str(it.str("media"))))
		}
	}
	return out
}

// mergeLabels takes each label field from a where set, else from b.
func mergeLabels(a, b *yaml.Node) *yaml.Node {
	out := mapping()
	for _, k := range []string{"title", "subtitle", "artwork"} {
		store.MapSet(out, k, orNull(firstSet(store.MapGet(a, k), store.MapGet(b, k))))
	}
	return out
}

func label(n *yaml.Node) *yaml.Node {
	return mergeLabels(n, nil)
}

func firstSet(ns ...*yaml.Node) *yaml.Node {
	for _, n := range ns {
		if n != nil && n.Tag != "!!null" && !(n.Kind == yaml.ScalarNode && n.Value == "" && n.Tag == "!!str") {
			return n
		}
	}
	return nil
}

func item(key string, v *yaml.Node) *yaml.Node {
	m := mapping()
	store.MapSet(m, key, v)
	return m
}

func mapping() *yaml.Node { return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"} }

func str(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }

func orNull(n *yaml.Node) *yaml.Node {
	if n == nil {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	}
	return n
}

// normalize is v1's tag name key: cut at "|", trim, lower case. Frozen
// here, as the step must not follow later changes to the app's rule.
func normalize(name string) string {
	name, _, _ = strings.Cut(name, "|")
	return strings.ToLower(strings.TrimSpace(name))
}

// unique returns id, or id-2, id-3 … when taken, and marks it used.
func unique(id string, used map[string]bool) string {
	if id == "" {
		id = "record"
	}
	out := id
	for n := 2; used[out]; n++ {
		out = fmt.Sprintf("%s-%d", id, n)
	}
	used[out] = true
	return out
}
