package importer

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/parser"
)

// minimalMP4 writes a file whose track header reports w x h — enough for
// the probe, without needing a muxer in the test environment.
func minimalMP4(t *testing.T, path string, w, h uint16) {
	t.Helper()
	box := func(name string, payload []byte) []byte {
		out := make([]byte, 8, 8+len(payload))
		binary.BigEndian.PutUint32(out[0:4], uint32(8+len(payload))) //nolint:gosec // G115: fixtures are a few hundred bytes
		copy(out[4:8], name)
		return append(out, payload...)
	}
	tkhd := make([]byte, 84)
	binary.BigEndian.PutUint16(tkhd[len(tkhd)-8:len(tkhd)-6], w)
	binary.BigEndian.PutUint16(tkhd[len(tkhd)-4:len(tkhd)-2], h)
	data := append(box("ftyp", []byte("isom\x00\x00\x02\x00isomiso2")),
		box("moov", box("trak", box("tkhd", tkhd)))...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func repairFixture(t *testing.T) (*Importer, *catalog.Store, string) {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := catalog.New(conn)
	lib, err := cat.CreateLibrary("Shows", dir, "shows")
	if err != nil {
		t.Fatal(err)
	}
	showID, err := cat.UpsertShow(&metadata.ShowDetail{
		TmdbID: 1, Title: "Severance", Year: 2022,
		Seasons: []metadata.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: []metadata.EpisodeDetail{
			{TmdbID: 10, Season: 1, Episode: 1, Title: "Good News About Hell", AirDate: "2022-02-18"},
		}}},
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := cat.GetShow(showID)
	if err != nil {
		t.Fatal(err)
	}
	epID := sh.Seasons[0].Episodes[0].ID

	// the shape this repairs: a file on disk, attached, with no recorded
	// quality — exactly what a scan of names carrying no resolution leaves
	path := filepath.Join(dir, "Severance - S01E01.mp4")
	minimalMP4(t, path, 1920, 1080)
	if err := cat.AttachEpisodeFile(epID, path, 1, "", ""); err != nil {
		t.Fatal(err)
	}
	return New(cat, nil, nil), cat, path
}

func TestRepairRecordsQualityFromTheFile(t *testing.T) {
	imp, cat, path := repairFixture(t)

	pending, err := cat.FilesNeedingRepair(0)
	if err != nil || len(pending) != 1 {
		t.Fatalf("expected one blank-quality file, got %v (%v)", pending, err)
	}

	res := imp.RepairUnknownQuality(context.Background())
	if res.QualityFixed != 1 {
		t.Fatalf("res = %+v, want one quality recorded", res)
	}

	// the quality gap is closed; the source gap legitimately remains — an
	// MP4 carries no title tag worth reading, and pixels can't say where
	// an encode came from
	left, err := cat.FilesNeedingRepair(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].NeedQuality || !left[0].NeedSource {
		t.Fatalf("after a repair, want only the source gap left, got %+v", left)
	}

	files, err := cat.OrganizeFiles(0)
	if err != nil || len(files) != 1 {
		t.Fatalf("organize files = %v (%v)", files, err)
	}
	if files[0].Quality != "1080p" {
		t.Fatalf("quality = %q, want 1080p read from the file", files[0].Quality)
	}
	// the probe learns pixels, never provenance — source must stay blank
	// rather than be guessed at
	if files[0].Source != "" {
		t.Fatalf("source = %q — a container cannot say disc or stream", files[0].Source)
	}
	if files[0].Path != path {
		t.Fatalf("repair moved the file: %q", files[0].Path)
	}
}

// A container the probe can't read must be counted and skipped, not
// retried forever or recorded as a guess.
func TestRepairLeavesUnreadableFilesAlone(t *testing.T) {
	imp, cat, path := repairFixture(t)
	unreadable := filepath.Join(filepath.Dir(path), "Severance - S01E01.avi")
	if err := os.Rename(path, unreadable); err != nil {
		t.Fatal(err)
	}
	files, _ := cat.FilesNeedingRepair(0)
	if err := cat.AttachEpisodeFile(files[0].ID, unreadable, 1, "", ""); err != nil {
		t.Fatal(err)
	}

	res := imp.RepairUnknownQuality(context.Background())
	if res.QualityFixed != 0 || res.Unknown != 1 {
		t.Fatalf("res = %+v, want nothing fixed and one still short", res)
	}
}

// A scan learns what it can from a filename. It must not erase what it
// cannot: a name with no source says nothing about the file's source,
// which is not the same as saying it has none. Before this, a scan of a
// library named without sources wiped every one of them — and a wiped
// source ranks as unknown, which reads as below any source cutoff.
func TestScanKeepsASourceTheFilenameDoesNotCarry(t *testing.T) {
	_, cat, path := repairFixture(t)
	files, err := cat.FilesNeedingRepair(0)
	if err != nil || len(files) != 1 {
		t.Fatalf("fixture = %v (%v)", files, err)
	}
	epID := files[0].ID
	// as though an earlier grab had recorded both
	if err := cat.AttachEpisodeFile(epID, path, 1, "1080p", "webdl"); err != nil {
		t.Fatal(err)
	}

	// a scan of a name carrying neither
	if err := cat.AttachScannedEpisodeFile(epID, path, 1, "", ""); err != nil {
		t.Fatal(err)
	}
	got, err := cat.OrganizeFiles(0)
	if err != nil || len(got) != 1 {
		t.Fatalf("organize files = %v (%v)", got, err)
	}
	if got[0].Quality != "1080p" || got[0].Source != "webdl" {
		t.Fatalf("scan erased what it could not read: quality=%q source=%q",
			got[0].Quality, got[0].Source)
	}

	// but a name that DOES carry them still wins — a replacement file is
	// genuinely different
	if err := cat.AttachScannedEpisodeFile(epID, path, 1, "720p", "hdtv"); err != nil {
		t.Fatal(err)
	}
	got, _ = cat.OrganizeFiles(0)
	if got[0].Quality != "720p" || got[0].Source != "hdtv" {
		t.Fatalf("a name carrying quality and source must win: %+v", got[0])
	}
}

// mkvWithTitle builds a real-enough Matroska file: an Info element
// carrying a segment title, and a video track at the given size.
func mkvWithTitle(t *testing.T, path, title string, w, h uint64) {
	t.Helper()
	size := func(n int) []byte {
		if n < 0x7F {
			return []byte{byte(n&0x7F) | 0x80}
		}
		return []byte{byte(n>>8&0x3F) | 0x40, byte(n & 0xFF)}
	}
	elem := func(id []byte, payload []byte) []byte {
		out := append([]byte{}, id...)
		out = append(out, size(len(payload))...)
		return append(out, payload...)
	}
	num := func(v uint64) []byte {
		if v <= 0xFF {
			return []byte{byte(v & 0xFF)}
		}
		return []byte{byte(v >> 8 & 0xFF), byte(v & 0xFF)}
	}
	info := elem([]byte{0x15, 0x49, 0xA9, 0x66}, elem([]byte{0x7B, 0xA9}, []byte(title)))
	video := append(elem([]byte{0xB0}, num(w)), elem([]byte{0xBA}, num(h))...)
	tracks := elem([]byte{0x16, 0x54, 0xAE, 0x6B}, elem([]byte{0xAE}, elem([]byte{0xE0}, video)))
	segment := elem([]byte{0x18, 0x53, 0x80, 0x67}, append(info, tracks...))
	data := append(elem([]byte{0x1A, 0x45, 0xDF, 0xA3}, []byte{0x42, 0x86, 0x81, 0x01}), segment...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// An MKV whose title tag carries the original release name gives its
// source back — the one signal pixels can never provide.
func TestRepairRecoversSourceFromAnMKVTitle(t *testing.T) {
	imp, cat, path := repairFixture(t)
	mkvPath := filepath.Join(filepath.Dir(path), "Severance - S01E01.mkv")
	mkvWithTitle(t, mkvPath, "Severance.S01E01.1080p.WEB-DL.DDP5.1.x264-NTb", 1920, 1080)
	files, _ := cat.FilesNeedingRepair(0)
	if err := cat.AttachEpisodeFile(files[0].ID, mkvPath, 1, "", ""); err != nil {
		t.Fatal(err)
	}

	res := imp.RepairUnknownQuality(context.Background())
	if res.QualityFixed != 1 || res.SourceFixed != 1 || res.Unknown != 0 {
		t.Fatalf("res = %+v, want quality and source both recovered", res)
	}
	got, err := cat.OrganizeFiles(0)
	if err != nil || len(got) != 1 {
		t.Fatalf("organize files = %v (%v)", got, err)
	}
	if got[0].Quality != "1080p" || got[0].Source != "webdl" {
		t.Fatalf("quality=%q source=%q — want 1080p from pixels, webdl from the tag", got[0].Quality, got[0].Source)
	}
}

// A human title carries no source; the file keeps a blank rather than a
// guess, and only the source side counts as unfinished.
func TestRepairIgnoresAHumanTitle(t *testing.T) {
	imp, cat, path := repairFixture(t)
	mkvPath := filepath.Join(filepath.Dir(path), "Severance - S01E01.mkv")
	mkvWithTitle(t, mkvPath, "Good News About Hell", 1920, 1080)
	files, _ := cat.FilesNeedingRepair(0)
	if err := cat.AttachEpisodeFile(files[0].ID, mkvPath, 1, "", ""); err != nil {
		t.Fatal(err)
	}

	res := imp.RepairUnknownQuality(context.Background())
	if res.QualityFixed != 1 || res.SourceFixed != 0 || res.Unknown != 1 {
		t.Fatalf("res = %+v — quality from pixels, no source guessed, file still short", res)
	}
	got, _ := cat.OrganizeFiles(0)
	if got[0].Source != "" {
		t.Fatalf("source = %q — a human title must never become a source", got[0].Source)
	}
}

// A recorded source outranks the tag: the repair only ever fills blanks.
func TestRepairNeverOverwritesARecordedSource(t *testing.T) {
	imp, cat, path := repairFixture(t)
	mkvPath := filepath.Join(filepath.Dir(path), "Severance - S01E01.mkv")
	// the tag claims bluray; the row already knows it is a webrip
	mkvWithTitle(t, mkvPath, "Severance.S01E01.1080p.BluRay.x264-GRP", 1920, 1080)
	files, _ := cat.FilesNeedingRepair(0)
	if err := cat.AttachEpisodeFile(files[0].ID, mkvPath, 1, "", "webrip"); err != nil {
		t.Fatal(err)
	}

	res := imp.RepairUnknownQuality(context.Background())
	if res.SourceFixed != 0 {
		t.Fatalf("res = %+v — a recorded source was rewritten from a tag", res)
	}
	got, _ := cat.OrganizeFiles(0)
	if got[0].Source != "webrip" {
		t.Fatalf("source = %q, want the recorded webrip kept", got[0].Source)
	}
}

// A file NAMED like an SD-era release still gets measured: convention is
// good enough to judge a release, but a file in hand has real pixels, and
// a mislabeled HD file must not be recorded as 480p on a naming custom.
func TestScannedQualityProbesOverSDInference(t *testing.T) {
	dir := t.TempDir()
	hd := filepath.Join(dir, "Show - S01E01 HDTV.mp4")
	minimalMP4(t, hd, 1920, 1080)

	p := parser.Parse(filepath.Base(hd))
	if p.Quality != "480p" || !p.QualityInferred {
		t.Fatalf("fixture parse = %+v, expected the SD inference", p)
	}
	resolveScannedQuality(hd, &p)
	if p.Quality != "1080p" || p.QualityInferred {
		t.Fatalf("quality = %q inferred=%v — the pixels must outrank the convention", p.Quality, p.QualityInferred)
	}

	// an unreadable container keeps the inference: an honest convention
	// beats a blank
	sd := filepath.Join(dir, "Old Show - S01E01 HDTV.wmv")
	if err := os.WriteFile(sd, []byte("not probeable"), 0o600); err != nil {
		t.Fatal(err)
	}
	p2 := parser.Parse(filepath.Base(sd))
	resolveScannedQuality(sd, &p2)
	if p2.Quality != "480p" || !p2.QualityInferred {
		t.Fatalf("unreadable container: %+v — the inference should stand", p2)
	}

	// a real marker is never second-guessed, no probe involved
	marked := filepath.Join(dir, "Show - S01E02 720p HDTV.mkv")
	p3 := parser.Parse(filepath.Base(marked))
	resolveScannedQuality(marked, &p3)
	if p3.Quality != "720p" {
		t.Fatalf("a named 720p came out %q", p3.Quality)
	}
}
