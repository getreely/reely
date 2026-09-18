// Package sabnzbd talks to SABnzbd — the download client (usenet-only for
// now, by design). Everything goes through SAB's single /api endpoint.
package sabnzbd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/getreely/reely/internal/download"
)

type Client struct {
	// url and key read live from settings — pointing reely at a SAB takes
	// effect without a restart.
	url    func() string
	key    func() string
	client *http.Client
}

func New(url, key func() string) *Client {
	return &Client{url: url, key: key, client: &http.Client{Timeout: 30 * time.Second}}
}

// Configured reports whether both a URL and an API key are present.
func (c *Client) Configured() bool { return c.url() != "" && c.key() != "" }

// AddURL queues an NZB by its download link, filed under category with
// nzbname as SAB's display/folder name (the release name — the importer
// parses it back out of the completed folder later). Returns SAB's nzo id.
func (c *Client) AddURL(ctx context.Context, nzbURL, nzbName, category string) (string, error) {
	q := url.Values{
		"mode":    {"addurl"},
		"name":    {nzbURL},
		"nzbname": {nzbName},
		"cat":     {category},
	}
	var body struct {
		Status bool     `json:"status"`
		NzoIDs []string `json:"nzo_ids"`
		Error  string   `json:"error"`
	}
	if err := c.call(ctx, q, &body); err != nil {
		return "", err
	}
	if !body.Status {
		if body.Error != "" {
			return "", fmt.Errorf("adding the NZB to SABnzbd: %s", body.Error)
		}
		return "", errors.New("the NZB was refused and SABnzbd gave no reason")
	}
	if len(body.NzoIDs) == 0 {
		return "", nil // accepted, but SAB didn't name the job (older versions)
	}
	return body.NzoIDs[0], nil
}

// queueSlot is one job still downloading, as SAB words it. SAB quotes
// its numbers, hence the ,string tags — a SABnzbd quirk that stops here
// rather than reaching the rest of reely.
type queueSlot struct {
	NzoID      string  `json:"nzo_id"`
	Name       string  `json:"filename"`
	Status     string  `json:"status"` // Downloading / Queued / Paused / …
	Category   string  `json:"cat"`
	SizeMB     float64 `json:"mb,string"`
	LeftMB     float64 `json:"mbleft,string"`
	Percentage int     `json:"percentage,string"`
	TimeLeft   string  `json:"timeleft"`
	Priority   string  `json:"priority"` // Force / High / Normal / Low
}

func (q queueSlot) item() download.QueueItem {
	return download.QueueItem{
		ID: q.NzoID, Protocol: download.Usenet,
		Name: q.Name, Status: q.Status, Category: q.Category,
		SizeMB: q.SizeMB, LeftMB: q.LeftMB, Percentage: q.Percentage,
		TimeLeft: q.TimeLeft, Priority: q.Priority,
	}
}

// flexInt reads a count that SAB quotes in some builds and leaves bare in
// others. A total we can't parse must not fail the whole queue read, so a
// malformed value degrades to zero rather than erroring.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	n, err := strconv.Atoi(strings.Trim(string(b), `"`))
	if err != nil {
		*f = 0
		return nil
	}
	*f = flexInt(n)
	return nil
}

// Queue lists what SAB is currently downloading in one category, starting
// at start. The second return is SAB's own count for that category, which
// is what lets a caller page instead of guessing whether one request saw
// everything.
func (c *Client) Queue(ctx context.Context, category string, start, limit int) ([]download.QueueItem, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if start < 0 {
		start = 0
	}
	q := url.Values{
		"mode":     {"queue"},
		"category": {category},
		"start":    {strconv.Itoa(start)},
		"limit":    {strconv.Itoa(limit)},
	}
	var body struct {
		Queue struct {
			Slots     []queueSlot `json:"slots"`
			NoOfSlots flexInt     `json:"noofslots"`
		} `json:"queue"`
	}
	if err := c.call(ctx, q, &body); err != nil {
		return nil, 0, err
	}
	total := int(body.Queue.NoOfSlots)
	// an older SAB that omits the count still reports at least what it sent
	if total < len(body.Queue.Slots) {
		total = len(body.Queue.Slots)
	}
	out := make([]download.QueueItem, 0, len(body.Queue.Slots))
	for _, slot := range body.Queue.Slots {
		out = append(out, slot.item())
	}
	return out, total, nil
}

// SetPriority changes one queued job's priority: 2 Force, 1 High,
// 0 Normal, -1 Low — the same scale SAB's own UI uses. Force starts the
// job immediately, ahead of everything else.
func (c *Client) SetPriority(ctx context.Context, nzoID string, priority int) error {
	q := url.Values{"mode": {"queue"}, "name": {"priority"},
		"value": {nzoID}, "value2": {strconv.Itoa(priority)}}
	// modern SAB answers {"status": true, "position": n}; older builds
	// answer the bare new position. Only an explicit refusal is an error.
	var raw json.RawMessage
	if err := c.call(ctx, q, &raw); err != nil {
		return err
	}
	var body struct {
		Status *bool  `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err == nil && body.Status != nil && !*body.Status {
		if body.Error != "" {
			return fmt.Errorf("SABnzbd refused the priority change: %s", body.Error)
		}
		return fmt.Errorf("changing priority of %s was refused", nzoID)
	}
	return nil
}

// DeleteQueue cancels a job SAB is still working on. delFiles makes SAB bin
// the partially downloaded data along with the entry — a cancel that left
// half a release sitting in the incomplete folder would just leak disk.
// Pause holds one job where it is, leaving what has downloaded alone.
//
// SAB pauses per job as well as globally; this is the per-job one, so
// pausing something in reely does not stop the rest of the queue.
func (c *Client) Pause(ctx context.Context, nzoID string) error {
	return c.queueSwitch(ctx, "pause", nzoID)
}

// Resume sets a paused job going again.
func (c *Client) Resume(ctx context.Context, nzoID string) error {
	return c.queueSwitch(ctx, "resume", nzoID)
}

// queueSwitch runs one of SAB's per-job queue verbs.
//
// SAB answers {"status": false} rather than an HTTP error when it will
// not do something — an id it no longer holds, most often — so the body
// is what decides, the same way cancelling reads it.
func (c *Client) queueSwitch(ctx context.Context, name, nzoID string) error {
	var body struct {
		Status bool `json:"status"`
	}
	if err := c.call(ctx, url.Values{
		"mode": {"queue"}, "name": {name}, "value": {nzoID},
	}, &body); err != nil {
		return err
	}
	if !body.Status {
		return fmt.Errorf("SABnzbd refused to %s download %s", name, nzoID)
	}
	return nil
}

func (c *Client) DeleteQueue(ctx context.Context, nzoID string, delFiles bool) error {
	q := url.Values{"mode": {"queue"}, "name": {"delete"}, "value": {nzoID}}
	if delFiles {
		q.Set("del_files", "1")
	}
	var body struct {
		Status bool `json:"status"`
	}
	if err := c.call(ctx, q, &body); err != nil {
		return err
	}
	if !body.Status {
		return fmt.Errorf("cancelling download %s was refused", nzoID)
	}
	return nil
}

// HistoryItem is one finished (or failed) job from SAB's history.
type historySlot struct {
	NzoID       string `json:"nzo_id"`
	Name        string `json:"name"`   // the job name — our release name
	Status      string `json:"status"` // Completed / Failed / …
	Category    string `json:"category"`
	Storage     string `json:"storage"` // final folder of a completed job
	FailMessage string `json:"fail_message"`
}

func (h historySlot) item() download.HistoryItem {
	return download.HistoryItem{
		ID: h.NzoID, Protocol: download.Usenet,
		Name: h.Name, Status: h.Status, Category: h.Category,
		Storage: h.Storage, FailMessage: h.FailMessage,
	}
}

// History lists recent jobs in one category, newest first.
func (c *Client) History(ctx context.Context, category string, limit int) ([]download.HistoryItem, error) {
	if limit <= 0 {
		limit = 50
	}
	q := url.Values{
		"mode":     {"history"},
		"category": {category},
		"start":    {"0"},
		"limit":    {strconv.Itoa(limit)},
	}
	var body struct {
		History struct {
			Slots []historySlot `json:"slots"`
		} `json:"history"`
	}
	if err := c.call(ctx, q, &body); err != nil {
		return nil, err
	}
	out := make([]download.HistoryItem, 0, len(body.History.Slots))
	for _, slot := range body.History.Slots {
		out = append(out, slot.item())
	}
	return out, nil
}

// DeleteHistory removes one job from SAB's history (files stay). Reely
// deletes what it has handled, which makes SAB's history the work queue —
// anything still listed is still pending.
// DeleteHistory removes one finished job from SAB's history. delFiles
// makes SAB delete what the job left on disk too — the emptied job
// folder and its par2/nfo junk after an import moved the video out, or a
// failed job's partial data. Without it every handled job leaves a husk
// in the completed folder forever.
func (c *Client) DeleteHistory(ctx context.Context, nzoID string, delFiles bool) error {
	q := url.Values{"mode": {"history"}, "name": {"delete"}, "value": {nzoID}}
	if delFiles {
		q.Set("del_files", "1")
	}
	var body struct {
		Status bool `json:"status"`
	}
	if err := c.call(ctx, q, &body); err != nil {
		return err
	}
	if !body.Status {
		return fmt.Errorf("deleting history entry %s was refused", nzoID)
	}
	return nil
}

func (c *Client) call(ctx context.Context, q url.Values, into any) error {
	base := strings.TrimRight(c.url(), "/")
	key := c.key()
	if base == "" || key == "" {
		return errors.New("no SABnzbd URL or API key configured — add them in Settings")
	}
	q.Set("apikey", key)
	q.Set("output", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("reaching SABnzbd: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return errors.New("the SABnzbd API key was rejected")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("calling SABnzbd: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("reading the SABnzbd response: %w", err)
	}
	return nil
}
