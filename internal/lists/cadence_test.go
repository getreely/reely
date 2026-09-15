package lists

import (
	"testing"
	"time"

	"github.com/getreely/reely/internal/catalog"
)

// Each person owns one sync timer covering all their lists; people who
// never set one ride the install default.
func TestPerUserCadence(t *testing.T) {
	s := &Syncer{Settings: mapSettings{
		"lists_sync_minutes": "30",
		UserCadenceKey(7):    "15",
	}}
	now := time.Now().UTC()
	stamp := func(d time.Duration) string { return now.Add(-d).Format("2006-01-02 15:04:05") }

	// user 7 runs a 15-minute clock — due at 16 minutes
	impatient := &catalog.List{CreatedBy: 7, LastSynced: stamp(16 * time.Minute)}
	if !s.due(impatient, now) {
		t.Fatal("15-minute user's list not due after 16 minutes")
	}
	// a user with no personal timer rides the 30-minute default
	patient := &catalog.List{CreatedBy: 9, LastSynced: stamp(16 * time.Minute)}
	if s.due(patient, now) {
		t.Fatal("default-cadence list due after only 16 minutes")
	}
	patient.LastSynced = stamp(31 * time.Minute)
	if !s.due(patient, now) {
		t.Fatal("default-cadence list not due after 31 minutes")
	}
	// never synced → always due; broken stamps re-sync rather than stall
	if !s.due(&catalog.List{}, now) {
		t.Fatal("never-synced list not due")
	}
	if !s.due(&catalog.List{LastSynced: "not a time"}, now) {
		t.Fatal("unparseable stamp did not re-sync")
	}
	// the floor holds no matter how eager the setting
	eager := &Syncer{Settings: mapSettings{UserCadenceKey(7): "1"}}
	if got := eager.listInterval(&catalog.List{CreatedBy: 7}); got != 15*time.Minute {
		t.Fatalf("floored interval = %v, want 15m", got)
	}
}
