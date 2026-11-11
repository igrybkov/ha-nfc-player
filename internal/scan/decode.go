package scan

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/igrybkov/ha-nfc-player/internal/model"
)

// Event data comes from HA templates, so every value may arrive as a string
// ("30", "true"), a number or a bool, and "" or null means unset. The
// decoders below accept all of these and return an ErrInvalid error for
// anything else, never a panic.

// eventData is the decoded data of an nfc_player_play or nfc_player_stop
// event.
type eventData map[string]any

func decodeEventData(raw json.RawMessage) (eventData, error) {
	d := eventData{}
	if len(raw) == 0 || string(raw) == "null" {
		return d, nil
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("%w: event data: %v", ErrInvalid, err)
	}
	return d, nil
}

// playCommand reads the keys of an nfc_player_play event.
func (d eventData) playCommand() (model.PlayCommand, error) {
	cmd := model.PlayCommand{Source: model.SourceEvent}
	var err error
	strs := []struct {
		key string
		dst *string
	}{
		{"area", &cmd.AreaID}, {"player", &cmd.Entity},
		{"media", &cmd.Media}, {"tag", &cmd.Tag}, {"pool", &cmd.Pool},
	}
	for _, s := range strs {
		if *s.dst, err = d.str(s.key); err != nil {
			return cmd, err
		}
	}
	if cmd.Volume, err = d.int("volume"); err != nil {
		return cmd, err
	}
	if cmd.Shuffle, err = d.bool("shuffle"); err != nil {
		return cmd, err
	}
	repeat, err := d.str("repeat")
	if err != nil {
		return cmd, err
	}
	if repeat != "" {
		r := model.Repeat(strings.ToLower(repeat))
		cmd.Repeat = &r
	}
	for key, dst := range map[string]*bool{"radio": &cmd.Radio, "toggle": &cmd.Toggle} {
		v, err := d.bool(key)
		if err != nil {
			return cmd, err
		}
		*dst = v != nil && *v
	}
	return cmd, nil
}

// target reads the keys of an nfc_player_stop event.
func (d eventData) target() (model.Target, error) {
	var t model.Target
	var err error
	if t.AreaID, err = d.str("area"); err != nil {
		return t, err
	}
	t.Entity, err = d.str("player")
	return t, err
}

// str reads a string. A number is formatted (a tag id may render as one);
// "" and null are unset.
func (d eventData) str(key string) (string, error) {
	switch v := d[key].(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(v), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	}
	return "", fmt.Errorf("%w: %s: want text, got %v", ErrInvalid, key, d[key])
}

// int reads a whole number: 30, 30.0 and "30" are 30.
func (d eventData) int(key string) (*int, error) {
	var f float64
	switch v := d[key].(type) {
	case nil:
		return nil, nil
	case float64:
		f = v
	case string:
		v = strings.TrimSpace(v)
		if v == "" {
			return nil, nil
		}
		var err error
		if f, err = strconv.ParseFloat(v, 64); err != nil {
			return nil, fmt.Errorf("%w: %s: %q is not a number", ErrInvalid, key, v)
		}
	default:
		return nil, fmt.Errorf("%w: %s: want a number, got %v", ErrInvalid, key, v)
	}
	if f != math.Trunc(f) || math.IsInf(f, 0) || math.Abs(f) > math.MaxInt32 {
		return nil, fmt.Errorf("%w: %s: %v is not a whole number", ErrInvalid, key, d[key])
	}
	n := int(f)
	return &n, nil
}

// bool reads true/false, 1/0, or the strings true/false, on/off, yes/no,
// 1/0 (any case).
func (d eventData) bool(key string) (*bool, error) {
	var b bool
	switch v := d[key].(type) {
	case nil:
		return nil, nil
	case bool:
		b = v
	case float64:
		if v != 0 && v != 1 {
			return nil, fmt.Errorf("%w: %s: %v is not a bool", ErrInvalid, key, v)
		}
		b = v == 1
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "":
			return nil, nil
		case "true", "on", "yes", "1":
			b = true
		case "false", "off", "no", "0":
			b = false
		default:
			return nil, fmt.Errorf("%w: %s: %q is not a bool", ErrInvalid, key, v)
		}
	default:
		return nil, fmt.Errorf("%w: %s: want a bool, got %v", ErrInvalid, key, v)
	}
	return &b, nil
}
