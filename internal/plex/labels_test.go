package plex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakePMS behaves the way the real server was observed to behave, which
// is the only reason these tests are worth anything: adding a label
// merges rather than replaces, tags come back title-cased, and a write
// it does not understand still answers 200.
type fakePMS struct {
	mu     sync.Mutex
	labels map[int64][]string
	// deaf makes every write a no-op that still answers 200, which is
	// what a payload Plex cannot parse actually does.
	deaf  bool
	scans []string
}

func newFakePMS() *fakePMS { return &fakePMS{labels: map[int64][]string{}} }

func title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (f *fakePMS) server(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/xml")

		switch {
		case strings.HasSuffix(r.URL.Path, "/refresh"):
			f.scans = append(f.scans, q.Get("path"))
			fmt.Fprint(w, `<MediaContainer/>`)

		case strings.HasPrefix(r.URL.Path, "/library/metadata/"):
			key, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/library/metadata/"), 10, 64)
			var b strings.Builder
			b.WriteString(`<MediaContainer><Video ratingKey="` + strconv.FormatInt(key, 10) + `" title="A Film">`)
			for _, l := range f.labels[key] {
				b.WriteString(`<Label tag="` + l + `"/>`)
			}
			b.WriteString(`</Video></MediaContainer>`)
			fmt.Fprint(w, b.String())

		case r.Method == http.MethodPut:
			if f.deaf {
				fmt.Fprint(w, `<MediaContainer/>`) // 200, and nothing happens
				return
			}
			key, _ := strconv.ParseInt(q.Get("id"), 10, 64)
			cur := f.labels[key]
			if rm := q.Get("label[].tag.tag-"); rm != "" {
				out := cur[:0]
				for _, l := range cur {
					if !strings.EqualFold(l, rm) {
						out = append(out, l)
					}
				}
				f.labels[key] = append([]string{}, out...)
			}
			for i := 0; ; i++ {
				v := q.Get(fmt.Sprintf("label[%d].tag.tag", i))
				if v == "" {
					break
				}
				dup := false
				for _, l := range f.labels[key] {
					if strings.EqualFold(l, v) {
						dup = true
					}
				}
				if !dup { // adds MERGE — what is there stays
					f.labels[key] = append(f.labels[key], title(v))
				}
			}
			sort.Strings(f.labels[key])
			fmt.Fprint(w, `<MediaContainer/>`)

		default: // section listing
			fmt.Fprint(w, `<MediaContainer>
				<Video ratingKey="5427" title="A Film">
					<Guid id="imdb://tt0000001"/><Guid id="tmdb://10096"/>
					<Label tag="Test"/>
				</Video>
				<Directory ratingKey="17095" title="A Show">
					<Guid id="tvdb://778411"/><Guid id="tmdb://31673"/>
				</Directory>
			</MediaContainer>`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func pmsClient() *Client { return New("reely-test") }

// A show may carry only a TVDB id, and matching on TMDB alone would skip
// exactly those.
func TestItemsCarryBothExternalIds(t *testing.T) {
	f := newFakePMS()
	items, err := pmsClient().Items(context.Background(), f.server(t), "tok", "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want a movie and a show", len(items))
	}
	byKey := map[int64]Item{}
	for _, it := range items {
		byKey[it.RatingKey] = it
	}
	if got := byKey[5427]; got.TmdbID != 10096 {
		t.Fatalf("movie tmdb = %d", got.TmdbID)
	}
	if got := byKey[17095]; got.TvdbID != 778411 || got.TmdbID != 31673 {
		t.Fatalf("show ids = tvdb %d / tmdb %d", got.TvdbID, got.TmdbID)
	}
	if got := byKey[5427]; len(got.Labels) != 1 || got.Labels[0] != "Test" {
		t.Fatalf("movie labels = %v", got.Labels)
	}
}

func owned(l string) bool { return strings.HasPrefix(strings.ToLower(l), "reely.") }

// The one that matters: a label somebody applied by hand must survive
// every pass reely makes.
func TestWritingLabelsLeavesHandMadeOnesAlone(t *testing.T) {
	f := newFakePMS()
	f.labels[5427] = []string{"Test", "Christmas"}
	url := f.server(t)

	err := pmsClient().SetLabels(context.Background(), url, "tok", "movie", "1", 5427,
		[]string{"reely.jolenes_family"}, nil, owned)
	if err != nil {
		t.Fatal(err)
	}
	got := f.labels[5427]
	for _, want := range []string{"Test", "Christmas", "Reely.jolenes_family"} {
		found := false
		for _, l := range got {
			if strings.EqualFold(l, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("labels = %v, missing %q", got, want)
		}
	}
}

// Revoking takes reely's label off and still leaves the rest.
func TestRevokingRemovesOnlyOurLabel(t *testing.T) {
	f := newFakePMS()
	f.labels[5427] = []string{"Test", "Reely.jolenes_family", "Reely.the_smiths"}
	url := f.server(t)

	err := pmsClient().SetLabels(context.Background(), url, "tok", "movie", "1", 5427,
		[]string{"reely.the_smiths"}, nil, owned)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ToLower(strings.Join(f.labels[5427], ","))
	if strings.Contains(got, "jolenes_family") {
		t.Fatalf("labels = %v, the revoked one is still there", f.labels[5427])
	}
	if !strings.Contains(got, "test") || !strings.Contains(got, "the_smiths") {
		t.Fatalf("labels = %v, lost something it should have kept", f.labels[5427])
	}
}

// Plex title-cases tags, so a second pass sees "Reely.x" where it wrote
// "reely.x". It must recognise its own work and do nothing.
func TestASecondPassIsANoOp(t *testing.T) {
	f := newFakePMS()
	url := f.server(t)
	c := pmsClient()
	want := []string{"reely.jolenes_family"}

	if err := c.SetLabels(context.Background(), url, "tok", "movie", "1", 5427, want, nil, owned); err != nil {
		t.Fatal(err)
	}
	first := append([]string{}, f.labels[5427]...)
	if err := c.SetLabels(context.Background(), url, "tok", "movie", "1", 5427, want, nil, owned); err != nil {
		t.Fatal(err)
	}
	if len(f.labels[5427]) != len(first) {
		t.Fatalf("second pass changed the labels: %v -> %v", first, f.labels[5427])
	}
}

// Both write endpoints answer 200 to a payload they ignore. A write that
// did not land has to be an error, not a success.
func TestAWriteThatDidNotLandIsAnError(t *testing.T) {
	f := newFakePMS()
	f.deaf = true
	err := pmsClient().SetLabels(context.Background(), f.server(t), "tok", "movie", "1", 5427,
		[]string{"reely.jolenes_family"}, nil, owned)
	if err == nil {
		t.Fatal("a 200 that changed nothing was reported as success")
	}
	if !strings.Contains(err.Error(), "did not stick") {
		t.Fatalf("error = %v", err)
	}
}

// An empty restriction does not restrict — it shares the whole library.
// It must never leave the process.
func TestAnEmptyRestrictionIsRefused(t *testing.T) {
	c := pmsClient()
	for _, tc := range [][2]string{{"", "label=reely.x"}, {"label=reely.x", ""}, {"", ""}} {
		err := c.SetRestrictions(context.Background(), "tok", 1, tc[0], tc[1])
		if err == nil {
			t.Fatalf("empty restriction (%q, %q) was allowed", tc[0], tc[1])
		}
		if !strings.Contains(err.Error(), "share everything") {
			t.Fatalf("error = %v", err)
		}
	}
}

// The scan nudge names the folder, so Plex looks there now rather than
// walking the whole library.
func TestScanAsksForOneFolder(t *testing.T) {
	f := newFakePMS()
	if err := pmsClient().Scan(context.Background(), f.server(t), "tok", "1", "/films/Arrival (2016)"); err != nil {
		t.Fatal(err)
	}
	if len(f.scans) != 1 || f.scans[0] != "/films/Arrival (2016)" {
		t.Fatalf("scans = %v", f.scans)
	}
}

func TestUnknownKindIsRefused(t *testing.T) {
	f := newFakePMS()
	err := pmsClient().SetLabels(context.Background(), f.server(t), "tok", "episode", "1", 1,
		[]string{"reely.x"}, nil, owned)
	if err == nil || !strings.Contains(err.Error(), "movie or show") {
		t.Fatalf("error = %v", err)
	}
}

// A section listing already returns every item's labels, so a caller
// that has them can skip the read. On a library of thousands that is the
// difference between a pass costing two requests and thousands.
func TestKnownLabelsSkipTheRead(t *testing.T) {
	f := newFakePMS()
	f.labels[5427] = []string{"Test"}
	url := f.server(t)

	err := pmsClient().SetLabels(context.Background(), url, "tok", "movie", "1", 5427,
		[]string{"reely.jolenes_family"}, []string{"Test"}, owned)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ToLower(strings.Join(f.labels[5427], ","))
	if !strings.Contains(got, "reely.jolenes_family") || !strings.Contains(got, "test") {
		t.Fatalf("labels = %v", f.labels[5427])
	}
}

// The real shape, copied from a live server's answer rather than from
// documentation — attribute names, ordering and all the extra detail
// Plex sends that this parser ignores.
//
// Two things it pins that guesswork got wrong before: Stream children
// come back from /library/metadata/{key} with no extra parameter, and
// an SDH track is marked with hearingImpaired rather than by its title.
func TestItemStreamsReadsTheTracksInAFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/metadata/9001" {
			t.Errorf("asked for %s", r.URL.Path)
		}
		if q := r.URL.RawQuery; q != "" {
			t.Errorf("sent %q — streams need no parameter", q)
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<MediaContainer size="1"><Video ratingKey="9001" title="A Film">
		<Media id="8150" duration="5856446" bitrate="9259" width="1920" height="1080"
		 aspectRatio="1.78" audioChannels="6" audioCodec="aac" videoCodec="hevc"
		 videoResolution="1080" container="mkv" videoFrameRate="24p" audioProfile="lc"
		 videoProfile="main" hasVoiceActivity="0">
		<Part id="8150" key="/library/parts/8150/1784341235/file.mkv" duration="5856446"
		 file="/data/media/movies/A Film (1999)/A Film (1999).mkv" size="6781091675"
		 audioProfile="lc" container="mkv" videoProfile="main">
		<Stream id="41136" streamType="1" default="1" codec="hevc" index="0" bitrate="8778"
		 language="English" languageTag="en" languageCode="eng" bitDepth="8"
		 chromaLocation="left" chromaSubsampling="4:2:0" codedHeight="1088" codedWidth="1920"
		 colorPrimaries="bt709" colorRange="tv" colorSpace="bt709" colorTrc="bt709"
		 frameRate="23.976" height="1080" level="120" profile="main" refFrames="1"
		 scanType="progressive" width="1920" displayTitle="1080p"
		 extendedDisplayTitle="1080p (HEVC Main)"></Stream>
		<Stream id="41137" streamType="2" selected="1" default="1" codec="aac" index="1"
		 channels="6" bitrate="481" language="English" languageTag="en" languageCode="eng"
		 profile="lc" samplingRate="48000" displayTitle="English (AAC 5.1)"
		 extendedDisplayTitle="English (AAC 5.1)"></Stream>
		<Stream id="41138" streamType="3" canAutoSync="0" default="1" codec="srt" index="2"
		 bitrate="0" language="English" languageTag="en" languageCode="eng"
		 displayTitle="English" extendedDisplayTitle="English (SRT)"></Stream>
		<Stream id="41139" streamType="3" canAutoSync="0" codec="srt" index="3" bitrate="0"
		 language="English" languageTag="en" languageCode="eng" hearingImpaired="1"
		 original="1" title="SDH" displayTitle="English SDH"
		 extendedDisplayTitle="SDH (English SRT)"></Stream>
		</Part></Media>
		<CommonSenseMedia id="30" oneLiner="Engaging but edgy."></CommonSenseMedia>
		</Video></MediaContainer>`)
	}))
	defer srv.Close()

	got, err := New("t").ItemStreams(context.Background(), srv.URL, "tok", 9001)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("streams = %d, want one video, one audio, two subtitle: %+v", len(got), got)
	}
	if got[0].Kind != "video" || got[0].Codec != "hevc" || got[0].Height != 1080 {
		t.Fatalf("video = %+v", got[0])
	}
	// the track that decides whether a client can play this unaided
	if got[1].Kind != "audio" || got[1].Codec != "aac" || got[1].Channels != 6 ||
		got[1].Language != "English" || got[1].Title != "English (AAC 5.1)" {
		t.Fatalf("audio = %+v", got[1])
	}
	if got[2].Kind != "subtitle" || got[2].Codec != "srt" || !got[2].Default || got[2].SDH {
		t.Fatalf("plain subtitle = %+v", got[2])
	}
	// title beats displayTitle where Plex sends both, and SDH is a flag
	// rather than something to read out of the name
	if !got[3].SDH || got[3].Title != "SDH" || got[3].Default {
		t.Fatalf("SDH subtitle = %+v", got[3])
	}
}

// A kind this parser does not know is dropped rather than guessed at.
func TestItemStreamsIgnoresAKindItDoesNotKnow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<MediaContainer><Video ratingKey="1"><Media><Part>
		<Stream id="1" streamType="2" codec="ac3" channels="2"></Stream>
		<Stream id="2" streamType="9" codec="whoknows"></Stream>
		</Part></Media></Video></MediaContainer>`)
	}))
	defer srv.Close()

	got, err := New("t").ItemStreams(context.Background(), srv.URL, "tok", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Codec != "ac3" {
		t.Fatalf("streams = %+v, want only the audio track", got)
	}
}

// An episode's tracks come from the show's key, because that is the only
// one reely caches — allLeaves answers with every episode under it and
// the wanted one is picked out by season and number.
func TestEpisodeStreamsPicksTheEpisodeOutOfTheShow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/metadata/770/allLeaves" {
			t.Errorf("asked for %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<MediaContainer size="3">
		<Video ratingKey="801" parentIndex="1" index="4" title="Pilot"><Media><Part>
		<Stream id="1" streamType="2" codec="ac3" channels="6"></Stream>
		</Part></Media></Video>
		<Video ratingKey="802" parentIndex="2" index="4" title="The One"><Media><Part>
		<Stream id="2" streamType="1" codec="h264" height="720"></Stream>
		<Stream id="3" streamType="2" codec="eac3" channels="2" language="English"></Stream>
		</Part></Media></Video>
		<Video ratingKey="803" parentIndex="2" index="5" title="The Next"><Media><Part>
		<Stream id="4" streamType="2" codec="truehd" channels="8"></Stream>
		</Part></Media></Video>
		</MediaContainer>`)
	}))
	defer srv.Close()

	got, err := New("t").EpisodeStreams(context.Background(), srv.URL, "tok", 770, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Codec != "h264" || got[1].Codec != "eac3" {
		t.Fatalf("streams = %+v, want S02E04's two tracks", got)
	}
}

// The shape a Plex LISTING actually returns: each episode carries Media
// and Part, and no Stream children at all. That is why this takes two
// calls — the test above was written from an assumption about allLeaves
// rather than from a server, passed, and shipped an episode page that
// silently showed nothing.
func TestEpisodeStreamsAsksTheEpisodeItselfWhenTheListingIsSilent(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		w.Header().Set("Content-Type", "application/xml")
		switch r.URL.Path {
		case "/library/metadata/770/allLeaves":
			fmt.Fprint(w, `<MediaContainer size="2">
			<Video ratingKey="801" parentIndex="2" index="3" title="Before"><Media id="1"
			 videoCodec="h264" audioCodec="aac" container="mkv"><Part id="1"
			 file="/data/series/Show/S02E03.mkv" size="1"></Part></Media></Video>
			<Video ratingKey="802" parentIndex="2" index="4" title="The One"><Media id="2"
			 videoCodec="hevc" audioCodec="eac3" container="mkv"><Part id="2"
			 file="/data/series/Show/S02E04.mkv" size="2"></Part></Media></Video>
			</MediaContainer>`)
		case "/library/metadata/802":
			fmt.Fprint(w, `<MediaContainer size="1"><Video ratingKey="802"><Media><Part>
			<Stream id="1" streamType="1" codec="hevc" height="1080"></Stream>
			<Stream id="2" streamType="2" codec="eac3" channels="6" language="English"></Stream>
			<Stream id="3" streamType="3" codec="srt" language="English" forced="1"></Stream>
			</Part></Media></Video></MediaContainer>`)
		default:
			t.Errorf("unexpected call: %s", r.URL.Path)
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	got, err := New("t").EpisodeStreams(context.Background(), srv.URL, "tok", 770, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("streams = %+v, want the three tracks from the episode itself", got)
	}
	if got[0].Codec != "hevc" || got[1].Codec != "eac3" || !got[2].Forced {
		t.Fatalf("streams = %+v", got)
	}
	// and it asked the RIGHT episode's key, not the first leaf's
	if len(asked) != 2 || asked[1] != "/library/metadata/802" {
		t.Fatalf("calls = %v, want the listing then S02E04's own key", asked)
	}
}

// An episode the server does not hold is a gap in the library rather
// than a failure: the page still draws, with nothing to show for it.
func TestEpisodeStreamsIsEmptyForAnEpisodePlexLacks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<MediaContainer size="1">
		<Video ratingKey="801" parentIndex="1" index="9"><Media><Part>
		<Stream id="1" streamType="2" codec="ac3"></Stream>
		</Part></Media></Video></MediaContainer>`)
	}))
	defer srv.Close()

	got, err := New("t").EpisodeStreams(context.Background(), srv.URL, "tok", 770, 3, 9)
	if err != nil {
		t.Fatalf("missing episode should not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("streams = %+v, want none", got)
	}
}
