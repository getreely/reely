package metadata

import "errors"

// Adult content is filtered out of reely unconditionally. There is no
// setting for it: a toggle would be a footgun with no legitimate use in a
// household media server, and the *arrs don't offer one either.
//
// The filtering is deliberately layered, because any single layer can be
// bypassed by a path nobody thought about:
//
//  1. Ask for less — get() pins include_adult=false on every TMDB request,
//     so no endpoint can forget it. TMDB defaults this to false on the
//     endpoints that accept it, but a default we don't assert is a
//     coincidence, not a guarantee.
//  2. Drop what comes back — every list of results is filtered on TMDB's
//     own `adult` flag as it is converted, so a title slipping past the
//     request parameter still never reaches a page.
//  3. Refuse to act — Movie and Show reject a flagged title outright, so
//     it cannot be previewed, added, or refreshed even by tmdb id. This is
//     the layer that matters: the id is guessable, and the other two only
//     govern discovery.
//
// Indexer results are filtered separately, on newznab category, in the
// prowlarr package. Release *names* are deliberately not pattern-matched —
// that removes legitimate titles (Shame, Sex Education, Nymphomaniac) for
// no gain over the category, which is the precise signal.

// ErrAdultContent is returned when a requested title is flagged adult by
// TMDB. Callers surface it as "not found": reely does not serve the title,
// so its existence is not reely's to report.
var ErrAdultContent = errors.New("this title is not available in reely")
