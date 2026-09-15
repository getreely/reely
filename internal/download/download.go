// Package download is the vocabulary reely uses to talk to a download
// client, independent of which one is installed.
//
// It exists because there is more than one kind. Usenet and BitTorrent
// disagree about almost everything at the wire — SAB quotes its numbers
// and calls a job an "nzo", a torrent client speaks hashes and ratios —
// but the import pipeline cares about none of that. It wants to know
// what is downloading, what finished, and where the files landed.
//
// So each client package owns its own wire structs and converts into
// these. A quirk of one client stops at that package's edge instead of
// reaching the importer, and adding a client means writing a converter
// rather than teaching the rest of reely a second dialect.
package download

// The two protocols reely knows.
//
// Prowlarr words them this way and so does everything downstream, so
// they are named once here rather than spelled as literals at each
// comparison.
const (
	Usenet  = "usenet"
	Torrent = "torrent"
)

// The statuses the import path acts on.
//
// The importer switches on these exact strings, so they are a contract
// between every download client and the sweep that reads them — not a
// SABnzbd detail that a second client happens to have to copy. SAB
// words its history this way natively; a client that does not has to
// say so in these terms.
const (
	StatusCompleted = "Completed"
	StatusFailed    = "Failed"
)

// QueueItem is one job still in flight.
//
// The JSON field names are SAB's, kept because they are the wire the UI
// already reads. The values are not: SAB quotes its numbers, and that is
// a SABnzbd detail that gets undone in the SABnzbd package rather than
// travelling out to the browser.
type QueueItem struct {
	// ID is the client's handle for the job — SAB's nzo id, a torrent's
	// info hash. Opaque to reely: it comes back from the client and goes
	// back to it to cancel or reprioritise.
	ID string `json:"nzo_id"`
	// Protocol says which client this came from, so a caller holding a
	// mixed list knows who to talk to about any given row.
	Protocol   string  `json:"protocol"`
	Name       string  `json:"filename"`
	Status     string  `json:"status"` // Downloading / Queued / Paused / …
	Category   string  `json:"cat"`
	SizeMB     float64 `json:"mb"`
	LeftMB     float64 `json:"mbleft"`
	Percentage int     `json:"percentage"`
	TimeLeft   string  `json:"timeleft"`
	Priority   string  `json:"priority"` // Force / High / Normal / Low
}

// HistoryItem is one job the client has finished with, for good or ill.
type HistoryItem struct {
	// ID is the same handle QueueItem carries — see there.
	ID string `json:"nzo_id"`
	// Protocol decides what finishing a job means. A usenet job is done
	// and its folder is residue; a torrent is seeding and still holding
	// the files it wrote. The import path reads this to choose between
	// moving a file out and hard-linking it.
	Protocol    string `json:"protocol"`
	Name        string `json:"name"`   // the job name — our release name
	Status      string `json:"status"` // Completed / Failed / …
	Category    string `json:"category"`
	Storage     string `json:"storage"` // final folder of a completed job
	FailMessage string `json:"fail_message"`
}
