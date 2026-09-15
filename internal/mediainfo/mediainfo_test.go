package mediainfo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// ── EBML builders ──────────────────────────────────────────────────────

// ebmlSize encodes a length with its marker bit, in as few bytes as the
// value needs — the same minimal form real muxers emit.
func ebmlSize(n int64) []byte {
	switch {
	case n < 0x7F:
		return []byte{byte(n&0x7F) | 0x80}
	case n < 0x3FFF:
		return []byte{byte(n>>8&0x3F) | 0x40, byte(n & 0xFF)}
	default:
		return []byte{byte(n>>16&0x1F) | 0x20, byte(n >> 8 & 0xFF), byte(n & 0xFF)}
	}
}

func idBytes(id uint32) []byte {
	switch {
	case id <= 0xFF:
		return []byte{byte(id & 0xFF)}
	case id <= 0xFFFF:
		return []byte{byte(id >> 8 & 0xFF), byte(id & 0xFF)}
	default:
		return []byte{byte(id >> 24 & 0xFF), byte(id >> 16 & 0xFF), byte(id >> 8 & 0xFF), byte(id & 0xFF)}
	}
}

func elem(id uint32, payload []byte) []byte {
	out := append([]byte{}, idBytes(id)...)
	out = append(out, ebmlSize(int64(len(payload)))...)
	return append(out, payload...)
}

func uintPayload(v uint64) []byte {
	if v <= 0xFF {
		return []byte{byte(v & 0xFF)}
	}
	return []byte{byte(v >> 8 & 0xFF), byte(v & 0xFF)}
}

// mkv builds a Matroska file carrying the given tracks, each an
// (width, height) pair — a width of 0 stands for an audio track, which
// carries no Video element at all.
func mkv(tracks ...[2]uint64) []byte {
	var entries []byte
	for _, tr := range tracks {
		var body []byte
		if tr[0] > 0 {
			video := append(elem(idPixelWidth, uintPayload(tr[0])),
				elem(idPixelHeight, uintPayload(tr[1]))...)
			body = elem(idVideo, video)
		} else {
			// an audio entry: some element that is not Video
			body = elem(0x83, []byte{0x02})
		}
		entries = append(entries, elem(idTrackEntry, body)...)
	}
	segment := elem(idTracks, entries)
	// a real file opens with an EBML header the walker has to skip past
	out := elem(0x1A45DFA3, []byte{0x42, 0x86, 0x81, 0x01})
	return append(out, elem(idSegment, segment)...)
}

// ── MP4 builders ───────────────────────────────────────────────────────

func box(name string, payload []byte) []byte {
	out := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint32(out[0:4], uint32(8+len(payload))) //nolint:gosec // G115: fixtures are a few hundred bytes
	copy(out[4:8], name)
	return append(out, payload...)
}

// tkhd builds a version-0 track header whose last two fields are the
// 16.16 fixed-point display size.
func tkhd(w, h uint16) []byte {
	p := make([]byte, 84)
	binary.BigEndian.PutUint16(p[len(p)-8:len(p)-6], w)
	binary.BigEndian.PutUint16(p[len(p)-4:len(p)-2], h)
	return box("tkhd", p)
}

func mp4(tracks ...[2]uint16) []byte {
	var traks []byte
	for _, tr := range tracks {
		traks = append(traks, box("trak", tkhd(tr[0], tr[1]))...)
	}
	out := box("ftyp", []byte("isom\x00\x00\x02\x00isomiso2"))
	return append(out, box("moov", traks)...)
}

// ── the tests ──────────────────────────────────────────────────────────

func write(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProbeMatroska(t *testing.T) {
	d, err := Probe(write(t, "show.mkv", mkv([2]uint64{1920, 1080})))
	if err != nil {
		t.Fatal(err)
	}
	if d.Width != 1920 || d.Height != 1080 {
		t.Fatalf("got %+v", d)
	}
}

// The ordering that broke the first version of the walker: an audio track
// ahead of the video one must not end the search.
func TestProbeMatroskaSkipsAudioTrackFirst(t *testing.T) {
	d, err := Probe(write(t, "show.mkv", mkv([2]uint64{0, 0}, [2]uint64{3840, 2160})))
	if err != nil {
		t.Fatal(err)
	}
	if d.Width != 3840 || d.Height != 2160 {
		t.Fatalf("got %+v — the video track after an audio one was missed", d)
	}
}

func TestProbeMP4(t *testing.T) {
	d, err := Probe(write(t, "movie.mp4", mp4([2]uint16{1280, 720})))
	if err != nil {
		t.Fatal(err)
	}
	if d.Width != 1280 || d.Height != 720 {
		t.Fatalf("got %+v", d)
	}
}

// An audio tkhd reports 0x0, so the largest track has to win.
func TestProbeMP4PicksTheVideoTrack(t *testing.T) {
	d, err := Probe(write(t, "movie.m4v", mp4([2]uint16{0, 0}, [2]uint16{1920, 1080}, [2]uint16{0, 0})))
	if err != nil {
		t.Fatal(err)
	}
	if d.Width != 1920 || d.Height != 1080 {
		t.Fatalf("got %+v", d)
	}
}

func TestProbeUnsupportedContainer(t *testing.T) {
	_, err := Probe(write(t, "old.wmv", []byte("some wmv bytes")))
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported — a .wmv is fine, just not probeable", err)
	}
}

// Garbage must fail cleanly rather than hang, panic, or read the world.
func TestProbeGarbageFailsCleanly(t *testing.T) {
	for _, name := range []string{"junk.mkv", "junk.mp4"} {
		t.Run(name, func(t *testing.T) {
			if _, err := Probe(write(t, name, bytes.Repeat([]byte{0xFF}, 512))); err == nil {
				t.Fatal("garbage probed clean")
			}
		})
	}
}

func TestProbeTruncatedFile(t *testing.T) {
	full := mkv([2]uint64{1920, 1080})
	if _, err := Probe(write(t, "cut.mkv", full[:len(full)/2])); err == nil {
		t.Fatal("a truncated file probed clean")
	}
}

// ── AVI ────────────────────────────────────────────────────────────────

func riff(name string, payload []byte) []byte {
	out := make([]byte, 8, 8+len(payload)+1)
	copy(out[0:4], name)
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(payload))) //nolint:gosec // G115: fixtures are a few hundred bytes
	out = append(out, payload...)
	if len(payload)%2 == 1 {
		out = append(out, 0) // word alignment
	}
	return out
}

// avih builds a main AVI header whose dwWidth/dwHeight sit at a fixed
// offset; zeroes there stand for an encoder that left them unset.
func avih(w, h uint32) []byte {
	p := make([]byte, 56)
	binary.LittleEndian.PutUint32(p[32:36], w)
	binary.LittleEndian.PutUint32(p[36:40], h)
	return riff("avih", p)
}

// strf builds a video stream format chunk. A negative height is a
// top-down bitmap, which is still that many pixels tall.
func strf(w, h int32) []byte {
	p := make([]byte, 40)
	binary.LittleEndian.PutUint32(p[4:8], uint32(w))  //nolint:gosec // G115: fixture values
	binary.LittleEndian.PutUint32(p[8:12], uint32(h)) //nolint:gosec // G115: fixture values
	return riff("strf", p)
}

func avi(body []byte) []byte {
	payload := append([]byte("AVI "), riff("LIST", append([]byte("hdrl"), body...))...)
	out := make([]byte, 8)
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(payload))) //nolint:gosec // G115: fixtures are small
	return append(out, payload...)
}

func TestProbeAVI(t *testing.T) {
	d, err := Probe(write(t, "old.avi", avi(avih(720, 480))))
	if err != nil {
		t.Fatal(err)
	}
	if d.Width != 720 || d.Height != 480 {
		t.Fatalf("got %+v", d)
	}
}

// Some encoders leave the main header's size at zero; the stream format
// still knows.
func TestProbeAVIFallsBackToStreamFormat(t *testing.T) {
	d, err := Probe(write(t, "old.avi", avi(append(avih(0, 0), strf(1280, -720)...))))
	if err != nil {
		t.Fatal(err)
	}
	if d.Width != 1280 || d.Height != 720 {
		t.Fatalf("got %+v — a top-down bitmap is still 720 tall", d)
	}
}

func TestProbeAVIRejectsNonRIFF(t *testing.T) {
	if _, err := Probe(write(t, "fake.avi", []byte("not a riff file at all!!"))); err == nil {
		t.Fatal("a non-RIFF .avi probed clean")
	}
}

// ── segment title ──────────────────────────────────────────────────────

// mkvWithTitle wraps the tracks fixture with an Info element carrying a
// segment title, the way muxers write one.
func mkvWithTitle(title string, tracks ...[2]uint64) []byte {
	info := elem(idInfo, elem(idTitle, []byte(title)))
	var entries []byte
	for _, tr := range tracks {
		video := append(elem(idPixelWidth, uintPayload(tr[0])),
			elem(idPixelHeight, uintPayload(tr[1]))...)
		entries = append(entries, elem(idTrackEntry, elem(idVideo, video))...)
	}
	segment := append(info, elem(idTracks, entries)...)
	out := elem(0x1A45DFA3, []byte{0x42, 0x86, 0x81, 0x01})
	return append(out, elem(idSegment, segment)...)
}

func TestTitleReadsTheSegmentTitle(t *testing.T) {
	const release = "Show.S01E01.1080p.WEB-DL.x264-GROUP"
	got, err := Title(write(t, "show.mkv", mkvWithTitle(release, [2]uint64{1920, 1080})))
	if err != nil {
		t.Fatal(err)
	}
	if got != release {
		t.Fatalf("title = %q", got)
	}
}

func TestTitleEmptyWhenAbsent(t *testing.T) {
	// the plain fixture writes no Info at all — no title is not an error
	got, err := Title(write(t, "show.mkv", mkv([2]uint64{1920, 1080})))
	if err != nil || got != "" {
		t.Fatalf("got %q, %v — a missing title is the common case, not a failure", got, err)
	}
}

func TestTitleUnsupportedOutsideMatroska(t *testing.T) {
	if _, err := Title(write(t, "movie.mp4", mp4([2]uint16{1280, 720}))); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v — MP4 title tags are deliberately not read", err)
	}
}
