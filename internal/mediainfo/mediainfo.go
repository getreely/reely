// Package mediainfo reads a video file's pixel dimensions straight out of
// its container header, so a file whose NAME says nothing about its
// quality can still be classified.
//
// No ffmpeg. Reely's image is alpine plus four packages, and pulling in
// ffprobe to learn two integers would add tens of megabytes, a subprocess
// per file, and a large C surface parsing untrusted media — for a number
// that sits in plain sight a few hundred bytes into the file. Nothing here
// decodes a frame; it walks headers and stops at the first video track.
package mediainfo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnsupported means the container isn't one this package reads. It is
// not a failure worth logging loudly — an .avi in a library is fine, it
// just can't be probed this way.
var ErrUnsupported = errors.New("unsupported container")

// scanLimit bounds how far in we look for the header. Track metadata lives
// near the front in every sane file; an MP4 with its moov box at the end
// (un-faststarted) is handled by seeking to it directly rather than by
// reading the whole file.
const scanLimit = 64 << 20

// maxDimension is the largest pixel count worth believing. 65536 is far
// past 8K and well inside what an int holds on any platform reely builds
// for; anything larger is a corrupt or hostile header, not a video.
const maxDimension = 1 << 16

// Dimensions are a video track's pixel size.
type Dimensions struct {
	Width  int
	Height int
}

// Probe reads the first video track's dimensions from a file.
func Probe(path string) (Dimensions, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the path comes from the catalog, not a request
	if err != nil {
		return Dimensions{}, err
	}
	defer f.Close()

	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv", ".webm":
		return probeMatroska(f)
	case ".mp4", ".m4v", ".mov":
		return probeMP4(f)
	case ".avi":
		return probeAVI(f)
	}
	return Dimensions{}, ErrUnsupported
}

// ── Matroska / WebM ────────────────────────────────────────────────────
//
// EBML: every element is <id><size><payload>, both id and size encoded
// with a leading-bit length marker. We descend Segment → Tracks →
// TrackEntry → Video and read PixelWidth/PixelHeight, skipping the payload
// of anything else — which is what keeps this from reading the clusters.

const (
	idSegment     = 0x18538067
	idTracks      = 0x1654AE6B
	idTrackEntry  = 0xAE
	idVideo       = 0xE0
	idPixelWidth  = 0xB0
	idPixelHeight = 0xBA
)

// descend lists the container elements worth walking into rather than
// skipping over.
var descend = map[uint32]bool{
	idSegment: true, idTracks: true, idTrackEntry: true, idVideo: true,
}

func probeMatroska(r io.ReadSeeker) (Dimensions, error) {
	var d Dimensions
	if err := walkEBML(r, scanLimit, &d); err != nil && !errors.Is(err, io.EOF) {
		return Dimensions{}, err
	}
	if d.Width == 0 || d.Height == 0 {
		return Dimensions{}, fmt.Errorf("no video track found")
	}
	return d, nil
}

// walkEBML reads elements until the budget runs out or a complete video
// size has been found. It descends only into the handful of containers on
// the path to Video, and always seeks to a container's exact end before
// moving on — so a TrackEntry holding audio doesn't abandon the search for
// the video track that follows it.
func walkEBML(r io.ReadSeeker, budget int64, d *Dimensions) error {
	for budget > 0 {
		if d.Width > 0 && d.Height > 0 {
			return nil
		}
		id, idLen, err := readEBMLID(r)
		if err != nil {
			return err
		}
		size, sizeLen, unknown, err := readEBMLSize(r)
		if err != nil {
			return err
		}
		budget -= idLen + sizeLen
		if unknown {
			// a live-muxed container of unknown length: descend if it is on
			// our path, otherwise there is no way to skip it
			if !descend[id] {
				return io.EOF
			}
			return walkEBML(r, budget, d)
		}

		switch {
		case descend[id]:
			here, err := r.Seek(0, io.SeekCurrent)
			if err != nil {
				return err
			}
			if err := walkEBML(r, min(size, budget), d); err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			if d.Width > 0 && d.Height > 0 {
				return nil
			}
			if _, err := r.Seek(here+size, io.SeekStart); err != nil {
				return err
			}
		case id == idPixelWidth, id == idPixelHeight:
			v, err := readUint(r, size)
			if err != nil {
				return err
			}
			// the file is untrusted input: a corrupt or hostile header can
			// claim any size at all, and a value past this is not a frame
			if v > maxDimension {
				return fmt.Errorf("implausible dimension %d", v)
			}
			if id == idPixelWidth {
				d.Width = int(v)
			} else {
				d.Height = int(v)
			}
		default:
			if _, err := r.Seek(size, io.SeekCurrent); err != nil {
				return err
			}
		}
		budget -= size
	}
	return nil
}

// readEBMLID reads a variable-length element id, marker bits kept — ids
// are conventionally written with them (0xB0, 0x1654AE6B).
func readEBMLID(r io.Reader) (uint32, int64, error) {
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return 0, 0, err
	}
	n := leadingZeros(first[0])
	if n > 3 {
		return 0, 0, fmt.Errorf("bad EBML id")
	}
	id := uint32(first[0])
	for i := 0; i < n; i++ {
		var b [1]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, 0, err
		}
		id = id<<8 | uint32(b[0])
	}
	return id, int64(n) + 1, nil
}

// readEBMLSize reads a variable-length size, marker bit stripped. An
// all-ones value means "unknown length".
func readEBMLSize(r io.Reader) (size int64, read int64, unknown bool, err error) {
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return 0, 0, false, err
	}
	n := leadingZeros(first[0])
	if n > 7 {
		return 0, 0, false, fmt.Errorf("bad EBML size")
	}
	value := int64(first[0]) & (int64(1)<<(7-n) - 1)
	allOnes := value == int64(1)<<(7-n)-1
	for i := 0; i < n; i++ {
		var b [1]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, 0, false, err
		}
		value = value<<8 | int64(b[0])
		allOnes = allOnes && b[0] == 0xFF
	}
	return value, int64(n) + 1, allOnes, nil
}

// leadingZeros counts the zero bits before the marker bit.
func leadingZeros(b byte) int {
	for i := 0; i < 8; i++ {
		if b&(0x80>>i) != 0 {
			return i
		}
	}
	return 8
}

// readUint reads a big-endian unsigned integer of n bytes (EBML stores
// them minimally, so PixelWidth may be one byte or four).
func readUint(r io.Reader, n int64) (uint64, error) {
	if n <= 0 || n > 8 {
		return 0, fmt.Errorf("bad integer width %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, err
	}
	var v uint64
	for _, b := range buf {
		v = v<<8 | uint64(b)
	}
	return v, nil
}

// ── MP4 / MOV (ISO base media) ─────────────────────────────────────────
//
// Boxes are <size uint32><type [4]byte><payload>, nested. We want
// moov → trak → tkhd, whose width and height are 16.16 fixed-point at a
// fixed offset. An audio track's tkhd carries 0x0, so taking the largest
// non-zero pair picks the video track without parsing hdlr.

func probeMP4(r io.ReadSeeker) (Dimensions, error) {
	var d Dimensions
	if err := walkBoxes(r, scanLimit, &d); err != nil && !errors.Is(err, io.EOF) {
		return Dimensions{}, err
	}
	if d.Width == 0 || d.Height == 0 {
		return Dimensions{}, fmt.Errorf("no video track found")
	}
	return d, nil
}

var mp4Containers = map[string]bool{"moov": true, "trak": true}

func walkBoxes(r io.ReadSeeker, budget int64, d *Dimensions) error {
	for budget > 0 {
		var header [8]byte
		if _, err := io.ReadFull(r, header[:]); err != nil {
			return err
		}
		size := int64(binary.BigEndian.Uint32(header[0:4]))
		name := string(header[4:8])
		payload := size - 8
		switch size {
		case 1: // 64-bit extended size
			var ext [8]byte
			if _, err := io.ReadFull(r, ext[:]); err != nil {
				return err
			}
			size64 := binary.BigEndian.Uint64(ext[:])
			if size64 > math.MaxInt64 || size64 < 16 {
				return fmt.Errorf("implausible 64-bit box size in %q", name)
			}
			payload = int64(size64) - 16
		case 0: // runs to the end of the file
			payload = budget - 8
		}
		if payload < 0 {
			return fmt.Errorf("bad box size in %q", name)
		}
		budget -= 8

		here, err := r.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
		switch {
		case mp4Containers[name]:
			if err := walkBoxes(r, min(payload, budget), d); err != nil && !errors.Is(err, io.EOF) {
				return err
			}
		case name == "tkhd":
			if err := readTkhd(r, payload, d); err != nil {
				return err
			}
		}
		// every branch lands on the box's exact end, so a short read inside
		// one track never derails the next
		if _, err := r.Seek(here+payload, io.SeekStart); err != nil {
			return err
		}
		budget -= payload
	}
	return nil
}

// readTkhd pulls a track's display size. Width and height are the last two
// fields of the box, 16.16 fixed-point — the integer half is the pixel
// count. Version 1 uses 64-bit times, making the box 20 bytes longer.
func readTkhd(r io.Reader, payload int64, d *Dimensions) error {
	buf := make([]byte, payload)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	if len(buf) < 84 {
		return nil // too short to hold a size; not fatal
	}
	w := int(binary.BigEndian.Uint16(buf[len(buf)-8 : len(buf)-6]))
	h := int(binary.BigEndian.Uint16(buf[len(buf)-4 : len(buf)-2]))
	// the largest track wins: an audio tkhd reports 0x0, and a cover-art
	// track is smaller than the video
	if w*h > d.Width*d.Height {
		d.Width, d.Height = w, h
	}
	return nil
}

// ── AVI (RIFF) ─────────────────────────────────────────────────────────
//
// RIFF is chunks of <fourcc><u32 little-endian size><payload>, with LIST
// chunks naming their type in the first four payload bytes. The main AVI
// header carries the frame size; a file that leaves it zero — some
// encoders do — is answered by the stream format's bitmap header instead.

func probeAVI(r io.ReadSeeker) (Dimensions, error) {
	var header [12]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Dimensions{}, err
	}
	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "AVI " {
		return Dimensions{}, fmt.Errorf("not a RIFF/AVI file")
	}
	var d Dimensions
	if err := walkRIFF(r, scanLimit, &d); err != nil && !errors.Is(err, io.EOF) {
		return Dimensions{}, err
	}
	if d.Width == 0 || d.Height == 0 {
		return Dimensions{}, fmt.Errorf("no frame size found")
	}
	return d, nil
}

func walkRIFF(r io.ReadSeeker, budget int64, d *Dimensions) error {
	for budget > 0 {
		var head [8]byte
		if _, err := io.ReadFull(r, head[:]); err != nil {
			return err
		}
		name := string(head[0:4])
		size := int64(binary.LittleEndian.Uint32(head[4:8]))
		budget -= 8
		here, err := r.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}

		switch name {
		case "LIST":
			// skip the four-byte list type, then walk the children
			if _, err := r.Seek(4, io.SeekCurrent); err != nil {
				return err
			}
			if err := walkRIFF(r, min(size-4, budget), d); err != nil && !errors.Is(err, io.EOF) {
				return err
			}
		case "avih":
			if err := readAVIH(r, size, d); err != nil {
				return err
			}
		case "strf":
			// only consulted when avih left the size at zero
			if d.Width == 0 || d.Height == 0 {
				if err := readBitmapHeader(r, size, d); err != nil {
					return err
				}
			}
		}
		// chunks are word-aligned: an odd size carries a pad byte
		if _, err := r.Seek(here+size+size%2, io.SeekStart); err != nil {
			return err
		}
		budget -= size
		if d.Width > 0 && d.Height > 0 {
			return nil
		}
	}
	return nil
}

// readAVIH takes dwWidth and dwHeight, at a fixed offset into the main
// AVI header.
func readAVIH(r io.Reader, size int64, d *Dimensions) error {
	if size < 40 {
		return nil
	}
	buf := make([]byte, 40)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	w := int64(binary.LittleEndian.Uint32(buf[32:36]))
	h := int64(binary.LittleEndian.Uint32(buf[36:40]))
	if w > 0 && w <= maxDimension && h > 0 && h <= maxDimension {
		d.Width, d.Height = int(w), int(h)
	}
	return nil
}

// readBitmapHeader takes biWidth and biHeight from a video stream's
// format chunk. biHeight is signed — negative means a top-down bitmap,
// and only its magnitude is the frame height.
func readBitmapHeader(r io.Reader, size int64, d *Dimensions) error {
	if size < 12 {
		return nil
	}
	buf := make([]byte, 12)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	// biWidth and biHeight are signed by definition, so the reinterpretation
	// is the format's, not a lossy narrowing
	w := int64(int32(binary.LittleEndian.Uint32(buf[4:8])))  //nolint:gosec // G115: biWidth is int32 in BITMAPINFOHEADER
	h := int64(int32(binary.LittleEndian.Uint32(buf[8:12]))) //nolint:gosec // G115: biHeight is int32, negative = top-down
	if h < 0 {
		h = -h
	}
	if w > 0 && w <= maxDimension && h > 0 && h <= maxDimension {
		d.Width, d.Height = int(w), int(h)
	}
	return nil
}

// ── Matroska segment title ─────────────────────────────────────────────
//
// Matroska carries a Title element in its segment Info, and release
// groups often set it to the full release name — which is exactly the
// string reely's parser reads quality and source from. Just as often it
// is a plain human title ("Big Buck Bunny, Sunflower version"), so this
// is an opportunistic read: the caller parses what comes back and takes
// only what falls out.

const (
	idInfo  = 0x1549A966
	idTitle = 0x7BA9
)

// titleScanLimit bounds the search. Info sits at the front of any sane
// file, right beside Tracks.
const titleScanLimit = 4 << 20

// maxTitleLen caps what we will read as a title: this is untrusted file
// metadata, and a "title" the size of a movie is a corrupt or hostile
// length, not a name.
const maxTitleLen = 1024

// Title reads the Matroska segment title. Empty with nil error means the
// file simply has none — the common case, not a failure. Non-Matroska
// containers return ErrUnsupported: MP4's ©nam sits under a chain release
// tooling rarely fills, so pretending to support it would only report
// "no title" with extra steps.
func Title(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the path comes from the catalog, not a request
	if err != nil {
		return "", err
	}
	defer f.Close()
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv", ".webm":
		return matroskaTitle(f)
	}
	return "", ErrUnsupported
}

func matroskaTitle(r io.ReadSeeker) (string, error) {
	title, err := walkForTitle(r, titleScanLimit)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return title, nil
}

// walkForTitle descends Segment → Info and returns the Title element's
// text. It stops at the first Info: a segment has one, and anything after
// it is cluster data not worth walking.
func walkForTitle(r io.ReadSeeker, budget int64) (string, error) {
	for budget > 0 {
		id, idLen, err := readEBMLID(r)
		if err != nil {
			return "", err
		}
		size, sizeLen, unknown, err := readEBMLSize(r)
		if err != nil {
			return "", err
		}
		budget -= idLen + sizeLen
		if unknown {
			if id != idSegment && id != idInfo {
				return "", io.EOF
			}
			continue // descend into an unknown-length container in place
		}

		switch id {
		case idSegment, idInfo:
			here, err := r.Seek(0, io.SeekCurrent)
			if err != nil {
				return "", err
			}
			title, err := walkForTitle(r, min(size, budget))
			if err != nil && !errors.Is(err, io.EOF) {
				return "", err
			}
			if title != "" {
				return title, nil
			}
			if id == idInfo {
				return "", nil // one Info per segment; no title means no title
			}
			if _, err := r.Seek(here+size, io.SeekStart); err != nil {
				return "", err
			}
		case idTitle:
			if size > maxTitleLen {
				return "", fmt.Errorf("implausible title length %d", size)
			}
			buf := make([]byte, size)
			if _, err := io.ReadFull(r, buf); err != nil {
				return "", err
			}
			return strings.ToValidUTF8(strings.TrimSpace(string(buf)), ""), nil
		default:
			if _, err := r.Seek(size, io.SeekCurrent); err != nil {
				return "", err
			}
		}
		budget -= size
	}
	return "", nil
}
