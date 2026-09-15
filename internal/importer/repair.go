package importer

import (
	"context"
	"errors"
	"log"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/mediainfo"
	"github.com/getreely/reely/internal/parser"
	"github.com/getreely/reely/internal/quality"
)

// Repairing a library whose files never had their quality or source
// recorded.
//
// A blank quality used to read as "no file" to the RSS sync — which
// downloaded a copy of what was already there — and a blank source counts
// as below any source cutoff. Neither needs a re-download: the container
// itself knows its pixel size, and an MKV's segment title often carries
// the original release name, which is exactly the string the parser reads
// a source from.

// RepairResult says what one pass managed. Unknown counts files the pass
// could not finish completely: an unreadable container for quality, or a
// title tag carrying no source — a plain human name, or no tag at all.
type RepairResult struct {
	QualityFixed int `json:"qualityFixed"`
	SourceFixed  int `json:"sourceFixed"`
	Unknown      int `json:"unknown"`
}

// RepairUnknownQuality probes every attached file missing its quality or
// source and records what it finds.
//
// Quality comes from the container's pixel size, so it is authoritative.
// Source comes only from an MKV title tag that parses to one — an
// opportunistic read of untrusted metadata, which is why it only ever
// fills a blank and never overwrites: a recorded source outranks a tag.
func (imp *Importer) RepairUnknownQuality(ctx context.Context) RepairResult {
	var res RepairResult
	files, err := imp.cat.FilesNeedingRepair(0)
	if err != nil {
		log.Printf("reely: repair: %v", err)
		return res
	}
	if len(files) == 0 {
		return res
	}
	log.Printf("reely: %d file(s) on disk missing recorded quality or source — probing", len(files))
	for _, f := range files {
		if ctx.Err() != nil {
			break
		}
		incomplete := false
		if f.NeedQuality {
			if imp.repairQuality(f) {
				res.QualityFixed++
			} else {
				incomplete = true
			}
		}
		if f.NeedSource {
			if imp.repairSource(f) {
				res.SourceFixed++
			} else {
				incomplete = true
			}
		}
		if incomplete {
			res.Unknown++
		}
	}
	log.Printf("reely: repair done — %d quality recorded, %d sources recovered, %d file(s) still short",
		res.QualityFixed, res.SourceFixed, res.Unknown)
	return res
}

// repairQuality reads the pixel size out of the container and records the
// matching rung. Unsupported containers are the common miss and say
// nothing interesting; a real read error is worth one line.
func (imp *Importer) repairQuality(f catalog.RepairableFile) bool {
	d, err := mediainfo.Probe(f.Path)
	if err != nil {
		if !errors.Is(err, mediainfo.ErrUnsupported) {
			log.Printf("reely: probing %s: %v", f.Path, err)
		}
		return false
	}
	q := quality.FromDimensions(d.Width, d.Height)
	if q == "" {
		return false
	}
	if err := imp.cat.SetQuality(f.Kind, f.ID, q); err != nil {
		log.Printf("reely: recording %s for %s: %v", q, f.Path, err)
		return false
	}
	return true
}

// repairSource reads the MKV segment title and takes a source only if the
// parser finds one in it. The tag is untrusted metadata a muxer may have
// set to anything, so nothing else is taken from it — not the quality
// (pixels outrank a label) and certainly not the identity of the file.
func (imp *Importer) repairSource(f catalog.RepairableFile) bool {
	title, err := mediainfo.Title(f.Path)
	if err != nil || title == "" {
		return false
	}
	src := parser.Parse(title).Source
	if src == "" {
		return false // a human title, not a release name — the common case
	}
	if err := imp.cat.SetSource(f.Kind, f.ID, src); err != nil {
		log.Printf("reely: recording source %s for %s: %v", src, f.Path, err)
		return false
	}
	return true
}
