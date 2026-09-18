package qbittorrent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/getreely/reely/internal/download"
)

// torrent is one row of /api/v2/torrents/info, trimmed to what reely
// reads. qBittorrent's wire shape stops here: everything leaves this
// package as a download.QueueItem or download.HistoryItem.
type torrent struct {
	Hash       string  `json:"hash"`
	Name       string  `json:"name"`
	State      string  `json:"state"`
	Category   string  `json:"category"`
	Size       int64   `json:"size"`
	AmountLeft int64   `json:"amount_left"`
	Progress   float64 `json:"progress"` // 0..1
	ETA        int64   `json:"eta"`      // seconds; 8640000 means "never"
	Ratio      float64 `json:"ratio"`
	// ContentPath is the torrent's own file or folder — NOT save_path,
	// which is the shared downloads root every torrent sits in. The
	// import path scans this and, on other paths, deletes it, so naming
	// the root here would hand it the whole downloads directory.
	ContentPath string `json:"content_path"`
	SavePath    string `json:"save_path"`
}

// qBittorrent says "never" with a sentinel rather than an absence.
const etaNever = 8640000

// done reports whether the torrent has all of its data.
//
// By state rather than by progress: a torrent can read 100% while still
// checking or moving, and calling that finished would hand the importer
// a folder qBittorrent has not finished writing.
func (t torrent) done() bool {
	switch t.State {
	// stoppedUP is qBittorrent 5's name for pausedUP: a torrent that has
	// all its data and is no longer seeding. Both are listed because the
	// rename landed in 5.0 and reely talks to either — and reading a
	// finished torrent as unfinished would leave it never imported.
	case "uploading", "stalledUP", "queuedUP", "pausedUP", "stoppedUP", "forcedUP":
		return true
	}
	return false
}

// broken reports a torrent that will not finish on its own.
func (t torrent) broken() bool {
	return t.State == "error" || t.State == "missingFiles"
}

// item is the in-flight view of a torrent.
func (t torrent) item() download.QueueItem {
	return download.QueueItem{
		ID: t.Hash, Protocol: download.Torrent,
		Name: t.Name, Status: queueStatus(t.State), Category: t.Category,
		SizeMB:     float64(t.Size) / (1 << 20),
		LeftMB:     float64(t.AmountLeft) / (1 << 20),
		Percentage: int(math.Round(t.Progress * 100)),
		TimeLeft:   timeLeft(t.ETA),
	}
}

// finished is the completed view, worded the way the importer reads it.
//
// Storage is the torrent's content path: the folder it wrote, or the
// single file for a one-file torrent. The importer walks it for videos,
// which works for either.
func (t torrent) finished() download.HistoryItem {
	status := download.StatusCompleted
	fail := ""
	if t.broken() {
		status = download.StatusFailed
		fail = "qBittorrent reports the torrent as " + t.State
	}
	return download.HistoryItem{
		ID: t.Hash, Protocol: download.Torrent,
		Name: t.Name, Status: status, Category: t.Category,
		Storage: t.ContentPath, FailMessage: fail,
	}
}

// queueStatus words a qBittorrent state the way the activity view reads
// SAB's. Anything unrecognised passes through as itself rather than
// being flattened into a lie.
func queueStatus(state string) string {
	switch state {
	case "downloading", "forcedDL", "metaDL":
		return "Downloading"
	case "stalledDL":
		return "Stalled"
	case "queuedDL", "allocating":
		return "Queued"
	// stoppedDL is 5's name for pausedDL, and both mean the same thing
	// to somebody reading the queue
	case "pausedDL", "stoppedDL":
		return "Paused"
	case "checkingDL", "checkingUP", "checkingResumeData", "moving":
		return "Checking"
	case "error", "missingFiles":
		return "Failed"
	}
	return state
}

// timeLeft renders an ETA the way SAB words it, "H:MM:SS", because the
// activity view prints it as given.
func timeLeft(secs int64) string {
	if secs <= 0 || secs >= etaNever {
		return ""
	}
	return fmt.Sprintf("%d:%02d:%02d", secs/3600, (secs%3600)/60, secs%60)
}

// list reads torrents.
func (c *Client) list(ctx context.Context, form url.Values) ([]torrent, error) {
	body, err := c.call(ctx, "/api/v2/torrents/info", form)
	if err != nil {
		return nil, err
	}
	var out []torrent
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return nil, fmt.Errorf("reading qBittorrent's torrent list: %w", err)
	}
	return out, nil
}

// AddURL hands qBittorrent a link — a .torrent URL or a magnet — and
// returns the info hash it ended up with.
//
// rename pins the torrent's display name to the release name, the same
// job SAB's nzbname does: it is what reely matches the finished
// download back to a title by.
//
// qBittorrent answers "Ok." and nothing else, so the hash has to be
// gone looking for. A magnet carries its own hash and needs no lookup;
// otherwise the newly added torrent is found by the name just pinned.
// Failing to find it is not an error — the torrent was accepted, and an
// unnamed job is something the history path already tolerates.
func (c *Client) AddURL(ctx context.Context, releaseURL, releaseName, category string) (string, error) {
	form := url.Values{"urls": {releaseURL}, "category": {category}}
	if releaseName != "" {
		form.Set("rename", releaseName)
	}
	body, err := c.call(ctx, "/api/v2/torrents/add", form)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(strings.TrimSpace(body), "Ok.") {
		return "", fmt.Errorf("qBittorrent refused the torrent: %s", strings.TrimSpace(firstLine(body)))
	}
	if hash := magnetHash(releaseURL); hash != "" {
		return hash, nil
	}
	return c.findByName(ctx, category, releaseName), nil
}

// magnetHash reads the info hash out of a magnet link, or "".
func magnetHash(link string) string {
	if !strings.HasPrefix(strings.ToLower(link), "magnet:") {
		return ""
	}
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	for _, xt := range u.Query()["xt"] {
		if rest, ok := strings.CutPrefix(xt, "urn:btih:"); ok {
			return strings.ToLower(rest)
		}
	}
	return ""
}

// findByName looks for the torrent AddURL just created. Best-effort:
// "" means reely will track this job by name instead of by hash.
func (c *Client) findByName(ctx context.Context, category, name string) string {
	if name == "" {
		return ""
	}
	items, err := c.list(ctx, url.Values{"category": {category}})
	if err != nil {
		return ""
	}
	for _, t := range items {
		if t.Name == name {
			return t.Hash
		}
	}
	return ""
}

// Queue lists what is still downloading in one category.
//
// The second return is the total before paging, which is what lets the
// activity view page rather than guess. qBittorrent has no count
// endpoint, so the category is read whole and counted here.
func (c *Client) Queue(ctx context.Context, category string, start, limit int) ([]download.QueueItem, int, error) {
	all, err := c.list(ctx, url.Values{"category": {category}})
	if err != nil {
		return nil, 0, err
	}
	pending := make([]download.QueueItem, 0, len(all))
	for _, t := range all {
		if t.done() {
			continue // seeding, not downloading — that is history's business
		}
		pending = append(pending, t.item())
	}
	total := len(pending)
	if start < 0 {
		start = 0
	}
	if start >= total {
		return nil, total, nil
	}
	if limit <= 0 {
		limit = 50
	}
	end := min(start+limit, total)
	return pending[start:end], total, nil
}

// History lists the torrents in one category that have finished
// downloading — the work queue the importer sweeps.
//
// A torrent that finished is still seeding, so unlike SAB's history
// this list does not empty itself when reely acts on a row. What keeps
// a job from importing twice is reely's own memory of having filed it.
func (c *Client) History(ctx context.Context, category string, limit int) ([]download.HistoryItem, error) {
	all, err := c.list(ctx, url.Values{"category": {category}})
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	out := make([]download.HistoryItem, 0, len(all))
	for _, t := range all {
		if !t.done() && !t.broken() {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, t.finished())
	}
	return out, nil
}

// DeleteQueue removes a torrent that is still downloading. delFiles
// bins the partial data, which is what abandoning a download means.
func (c *Client) DeleteQueue(ctx context.Context, id string, delFiles bool) error {
	return c.remove(ctx, id, delFiles)
}

// DeleteHistory removes a torrent reely is finished with.
//
// For SAB this clears a history entry and the job's leftovers. For a
// torrent there is no history to clear: the entry IS the torrent, and
// removing it stops the seeding. Whether that is the right thing to do
// with a completed torrent is a question for the import path, not for
// this client, which does what it is told.
func (c *Client) DeleteHistory(ctx context.Context, id string, delFiles bool) error {
	return c.remove(ctx, id, delFiles)
}

func (c *Client) remove(ctx context.Context, id string, delFiles bool) error {
	_, err := c.call(ctx, "/api/v2/torrents/delete", url.Values{
		"hashes": {id}, "deleteFiles": {strconv.FormatBool(delFiles)},
	})
	return err
}

// SetPriority moves a torrent within the queue.
//
// SAB's scale is Force/High/Normal/Low as 2/1/0/-1; qBittorrent has no
// scale, only "to the top" and "to the bottom". So anything above
// normal goes to the top and anything below goes to the bottom, and
// normal — which is a torrent already where the queue put it — does
// nothing rather than shuffling it somewhere arbitrary.
func (c *Client) SetPriority(ctx context.Context, id string, priority int) error {
	var path string
	switch {
	case priority > 0:
		path = "/api/v2/torrents/topPrio"
	case priority < 0:
		path = "/api/v2/torrents/bottomPrio"
	default:
		return nil
	}
	_, err := c.call(ctx, path, url.Values{"hashes": {id}})
	return err
}

// Pause holds one torrent where it is, keeping what it has already
// pulled down. Resume sets it going again.
//
// qBittorrent 5.0 renamed these to stop and start and kept pause and
// resume as deprecated aliases. reely asks for the new name and falls
// back to the old one only where the build has never heard of it, so a
// 4.x install and a 5.x install both work and the fallback simply stops
// being reached once the aliases are gone.
func (c *Client) Pause(ctx context.Context, id string) error {
	return c.switchState(ctx, id, "/api/v2/torrents/stop", "/api/v2/torrents/pause")
}

// Resume sets a paused torrent going again.
func (c *Client) Resume(ctx context.Context, id string) error {
	return c.switchState(ctx, id, "/api/v2/torrents/start", "/api/v2/torrents/resume")
}

// switchState calls path, falling back to legacy where this build does
// not have it. Only a 404 justifies the second attempt: anything else is
// this qBittorrent answering the endpoint it does have.
func (c *Client) switchState(ctx context.Context, id, path, legacy string) error {
	form := url.Values{"hashes": {id}}
	body, status, err := c.callStatus(ctx, path, form)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		if body, status, err = c.callStatus(ctx, legacy, form); err != nil {
			return err
		}
	}
	if status != http.StatusOK {
		return fmt.Errorf("qBittorrent answered %d to %s: %s",
			status, path, strings.TrimSpace(firstLine(body)))
	}
	return nil
}

// SetShareLimit says when a torrent should stop seeding.
//
// ratio is a share ratio, or one of RatioUnlimited / RatioGlobal. Zero
// is a real value here — stop as soon as the download finishes — which
// is why the two "no number" answers have sentinels of their own.
//
// seedMinutes caps the time instead, on the same sentinels. Passing
// RatioGlobal for both is qBittorrent's own default behaviour.
func (c *Client) SetShareLimit(ctx context.Context, id string, ratio float64, seedMinutes int) error {
	_, err := c.call(ctx, "/api/v2/torrents/setShareLimits", url.Values{
		"hashes":                   {id},
		"ratioLimit":               {strconv.FormatFloat(ratio, 'f', -1, 64)},
		"seedingTimeLimit":         {strconv.Itoa(seedMinutes)},
		"inactiveSeedingTimeLimit": {strconv.Itoa(RatioGlobal)},
	})
	return err
}

// Version is what the health check asks for: it needs a call that
// proves the URL, the credentials and the session all work.
func (c *Client) Version(ctx context.Context) (string, error) {
	body, err := c.call(ctx, "/api/v2/app/version", nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(body), nil
}

// SetCategory moves a torrent to another category.
//
// This is how reely lets go of a torrent without killing it. A usenet
// job leaves history once it is imported; a torrent has no history to
// leave — it is still seeding, still in the category reely sweeps, and
// would be picked up and imported again on the next pass forever.
//
// Moving it to a category reely does not sweep retires it: the torrent
// keeps seeding, keeps its files, and is plainly labelled in
// qBittorrent as something already taken care of.
func (c *Client) SetCategory(ctx context.Context, id, category string) error {
	_, err := c.call(ctx, "/api/v2/torrents/setCategory", url.Values{
		"hashes": {id}, "category": {category},
	})
	return err
}
