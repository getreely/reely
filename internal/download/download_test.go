package download

import (
	"encoding/json"
	"testing"
)

// The queue item is handed straight to the browser, so its JSON is a
// contract with the UI rather than an internal detail.
//
// Numbers are the point of this test. SAB quotes its own, and for a
// while that leaked all the way out: the API emitted "mb":"100" while
// the TypeScript declared a number, and it only worked because
// JavaScript coerces a string in arithmetic. A client's wire quirks are
// now undone in that client's package, and what leaves here is honest.
func TestTheQueueItemIsTheWireTheUIReads(t *testing.T) {
	b, err := json.Marshal(QueueItem{
		ID: "a", Name: "One.1080p", Status: "Downloading", Category: "movies",
		SizeMB: 100, LeftMB: 40, Percentage: 60, TimeLeft: "0:01:00", Priority: "Normal",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"mb", "mbleft", "percentage"} {
		if _, ok := got[field].(float64); !ok {
			t.Errorf("%s came out as %T (%v), want a JSON number", field, got[field], got[field])
		}
	}
	// the names are SAB's, kept deliberately: they are the wire the UI
	// already reads, and renaming them is a change to make with the UI
	for _, field := range []string{"nzo_id", "filename", "status", "cat", "timeleft", "priority"} {
		if _, ok := got[field]; !ok {
			t.Errorf("%s is missing — the UI reads it by that name", field)
		}
	}
}
