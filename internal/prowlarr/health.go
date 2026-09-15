package prowlarr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Health surface: Prowlarr already tracks which of its indexers are
// failing and benches them after repeated errors. These calls expose that
// bookkeeping so reely's health panel can name the broken indexer instead
// of shrugging.

// Indexer is one indexer Prowlarr manages.
type Indexer struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Enable bool   `json:"enable"`
}

// Indexers lists every indexer Prowlarr knows.
func (c *Client) Indexers(ctx context.Context) ([]Indexer, error) {
	var out []Indexer
	return out, c.get(ctx, "/api/v1/indexer", &out)
}

// IndexerStatus is Prowlarr's failure bench for one indexer: while
// DisabledTill is in the future, Prowlarr won't use it.
type IndexerStatus struct {
	IndexerID         int    `json:"indexerId"`
	DisabledTill      string `json:"disabledTill"`
	MostRecentFailure string `json:"mostRecentFailure"`
}

// IndexerStatuses lists the current failure bench.
func (c *Client) IndexerStatuses(ctx context.Context) ([]IndexerStatus, error) {
	var out []IndexerStatus
	return out, c.get(ctx, "/api/v1/indexerstatus", &out)
}

// HealthWarning is one row from Prowlarr's own health check — the "indexer
// X is unavailable due to failures" messages live here.
type HealthWarning struct {
	Source  string `json:"source"`
	Type    string `json:"type"` // warning | error
	Message string `json:"message"`
}

// Health returns Prowlarr's own health warnings.
func (c *Client) Health(ctx context.Context) ([]HealthWarning, error) {
	var out []HealthWarning
	return out, c.get(ctx, "/api/v1/health", &out)
}

func (c *Client) get(ctx context.Context, path string, into any) error {
	base := strings.TrimRight(c.url(), "/")
	key := c.key()
	if base == "" || key == "" {
		return errors.New("no Prowlarr URL or API key configured — add them in Settings")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", key)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("reaching Prowlarr: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New("the Prowlarr API key was rejected")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("calling Prowlarr %s: %s", path, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("reading the Prowlarr response: %w", err)
	}
	return nil
}
