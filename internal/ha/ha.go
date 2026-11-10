// Package ha is the Home Assistant websocket client: auth, events, service
// calls and registry lookups over one connection.
//
// Owner: track A (docs/PLAN.md section 10).
package ha

import (
	"context"
	"encoding/json"
	"time"
)

// Client is the part of the HA websocket API the app uses.
type Client interface {
	SubscribeEvents(ctx context.Context, eventType string, fn func(Event)) error
	CallService(ctx context.Context, domain, service string, data map[string]any, target *Target, returnResponse bool) (json.RawMessage, error)
	Areas(ctx context.Context) ([]Area, error)
	Devices(ctx context.Context) ([]Device, error)
	Entities(ctx context.Context) ([]Entity, error)
	States(ctx context.Context) ([]State, error)
	Tags(ctx context.Context) ([]Tag, error) // websocket "tag/list"
}

// Event is an HA bus event.
type Event struct {
	EventType string          `json:"event_type"`
	Data      json.RawMessage `json:"data"`
	Origin    string          `json:"origin"`
	TimeFired time.Time       `json:"time_fired"`
}

// Target is the service call target.
type Target struct {
	EntityID []string `json:"entity_id,omitempty"`
	DeviceID []string `json:"device_id,omitempty"`
	AreaID   []string `json:"area_id,omitempty"`
}

// Area is an area registry entry ("config/area_registry/list").
type Area struct {
	AreaID string `json:"area_id"`
	Name   string `json:"name"`
}

// Device is a device registry entry ("config/device_registry/list").
type Device struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	NameByUser    string     `json:"name_by_user"`
	Manufacturer  string     `json:"manufacturer"`
	Model         string     `json:"model"`
	AreaID        string     `json:"area_id"`
	ConfigEntries []string   `json:"config_entries"`
	Identifiers   [][]string `json:"identifiers"` // [domain, id] pairs
}

// Entity is an entity registry entry ("config/entity_registry/list").
// Platform is the integration domain (esphome, sonos, music_assistant).
type Entity struct {
	EntityID      string `json:"entity_id"`
	DeviceID      string `json:"device_id"`
	AreaID        string `json:"area_id"` // entity override; empty = device area
	Platform      string `json:"platform"`
	ConfigEntryID string `json:"config_entry_id"`
	Name          string `json:"name"`
	OriginalName  string `json:"original_name"`
	DisabledBy    string `json:"disabled_by"`
}

// State is an entity state ("get_states").
type State struct {
	EntityID    string         `json:"entity_id"`
	State       string         `json:"state"`
	Attributes  map[string]any `json:"attributes"`
	LastChanged time.Time      `json:"last_changed"`
}

// Tag is an HA tag registry entry ("tag/list").
type Tag struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	LastScanned *time.Time `json:"last_scanned"`
}

// Options configures a websocket client.
type Options struct {
	URL   string // ws:// or wss:// URL of /api/websocket
	Token string
}

// WSClient is the websocket implementation of Client.
type WSClient struct {
	opts Options
}

var _ Client = (*WSClient)(nil)

// New returns a client. It does not connect; call Run.
func New(opts Options) *WSClient { return &WSClient{opts: opts} }

// Run connects, authenticates and keeps the connection alive with reconnects
// until ctx is done.
func (c *WSClient) Run(ctx context.Context) error {
	_ = c.opts
	panic("TODO: track A")
}

func (c *WSClient) SubscribeEvents(ctx context.Context, eventType string, fn func(Event)) error {
	panic("TODO: track A")
}

func (c *WSClient) CallService(ctx context.Context, domain, service string, data map[string]any, target *Target, returnResponse bool) (json.RawMessage, error) {
	panic("TODO: track A")
}

func (c *WSClient) Areas(ctx context.Context) ([]Area, error)      { panic("TODO: track A") }
func (c *WSClient) Devices(ctx context.Context) ([]Device, error)  { panic("TODO: track A") }
func (c *WSClient) Entities(ctx context.Context) ([]Entity, error) { panic("TODO: track A") }
func (c *WSClient) States(ctx context.Context) ([]State, error)    { panic("TODO: track A") }
func (c *WSClient) Tags(ctx context.Context) ([]Tag, error)        { panic("TODO: track A") }
