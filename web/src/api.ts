// Typed client for the Reely API. One request() helper, thin namespaces on
// top — errors surface as thrown Error(message) for toasts to catch.

export interface ApiLibraryStats {
  id: number
  name: string
  kind: string
  path: string
  titles: number
  onDisk: number
  missing: number
  bytes: number
}

export interface ApiStats {
  movies: number
  moviesOnDisk: number
  shows: number
  episodes: number
  episodesOnDisk: number
  totalBytes: number
  grabs30d: number
  imports30d: number
  failures30d: number
  blocklisted: number
  libraries: ApiLibraryStats[]
}

export interface ApiBackup {
  name: string
  size: number
  createdAt: string
}

// Splitting the library: a group is a set of people who see the same
// titles. Label is what Plex sees on the item; name is what you see.
export interface ApiGroup {
  id: number
  name: string
  label: string
  ownerUserId?: number
  personal: boolean
  members: number
  titles: number
  // the group holding what was already in Plex before the split.
  // Everybody is in it and nothing new ever goes there.
  backfill: boolean
  // the group that IS the household: every account joins it as it is
  // created, and like the backfill group it is never a request default.
  // The two differ on membership — a backfill group's is frozen at seed,
  // because a latecomer never had the old library.
  everyone: boolean
  createdAt: string
  memberIds?: number[]
  // whether you are in this group yourself: an owner is in households
  // like anybody else, and what they add defaults to their own
  mine?: boolean
}

// Who can see a title, and which group grants it. Two rows for the same
// person mean two groups grant it — worth showing, because revoking one
// changes nothing.
export interface ApiViewer {
  userId: number
  username: string
  groupId: number
  groupName: string
  label: string
  personal: boolean
}

// One of your own groups. Personal marks your own — the one a request
// always reaches, and the one you cannot opt out of.
export interface ApiMyGroup {
  id: number
  name: string
  personal: boolean
  backfill: boolean
  everyone: boolean
}

export interface ApiShareState {
  userId: number
  managed: boolean
  movies: string
  shows: string
  writtenAt?: string
  groups?: ApiMyGroup[]
  // the Plex account this person watches on, when it is not the one
  // they sign in with. 0/absent means it is.
  shareAccountId?: number
  plexAccountId?: number
}

// One Plex account this server is shared with. Not a reely account: the
// point of naming one is that reely may have no user row for it.
export interface ApiPlexAccount {
  accountId: number
  username: string
  email: string
}

export interface ApiSharingResult {
  pending: number
  // the titles waiting, named — up to a cap. pending is always the full
  // count.
  waiting?: { title: string; onDisk: boolean }[]
  // how many of those reely holds a file for: not downloads to wait for,
  // but files Plex has under a different title
  mismatched?: number
  labelled: number
  shares: number
  drifted?: string[]
  // set when restrictions were deliberately not written because
  // labelling did not finish: nobody's access changed, and the next pass
  // tries again
  held?: boolean
  // grants dropped because nothing on the install has that title any
  // more: no row in reely, no item on the media server
  pruned?: number
  errors?: string[]
}

export interface ApiUser {
  id: number
  username: string
  role: "admin" | "user"
  libraryIds?: number[]
  createdAt: string
  // mayAdd separates the two kinds of 'user': one that adds titles to its
  // libraries, and a requester, who browses the same ones and asks.
  mayAdd: boolean
  active: boolean
  autoApproveMovies: boolean
  autoApproveShows: boolean
  // null is no limit, which is what every account starts with
  quotaMoviesWeek?: number
  quotaShowsWeek?: number
  // Where this account's requests land. Absent when they hold several
  // libraries and haven't picked — the only case there's a question.
  defaultLibraryId?: number
  authProvider: "local" | "plex"
  plexUsername?: string
}

export interface ApiLibrary {
  id: number
  name: string
  path: string
  kind: "movies" | "shows"
  monitorNew: boolean
  qualityProfileId: number
  createdAt: string
}

export interface ApiFormatSpec {
  kind: "title" | "group" | "source" | "resolution"
  value: string
  negate: boolean
  required: boolean
}

export interface ApiCustomFormat {
  id: number
  trashId?: string
  appliesTo: "movies" | "shows"
  name: string
  score: number
  specs: ApiFormatSpec[]
}

export interface ApiQualityProfile {
  id: number
  name: string
  qualities: string[]
  cutoff: string
  sources: string[]
  sourceCutoff: string
  required: string[]
  blocked: string[]
  preferred: { term: string; score: number }[]
  // the format-score floor: null = no floor, 0 = reject anything negative
  minFormatScore: number | null
  upgrades: boolean
  hdr: "allow" | "require" | "block"
  minMbPerMin: number
  maxMbPerMin: number
}

export interface ApiRecentShowImport {
  showId: number
  showTitle: string
  poster: string
  libraryId: number
  episodeId: number
  season: number
  episode: number
  episodeTitle: string
  count: number
  importedAt: string
}

// One streaming service's row on Explore, for one kind.
export interface ApiProviderRow {
  key: string
  name: string
  kind: "movie" | "show"
  results: ApiSearchResult[] | null
}

// What a re-match would rename if asked to. `placed` is only the box's
// starting state: reely cannot tell a library somebody arranged by hand
// from one imported at its release names, so the choice is the owner's.
export interface ApiRenameOutlook {
  files: number
  placed: boolean
  path: string
}

export interface ApiSearchResult {
  tmdbId: number
  // set on TVDB-sourced show results (a TVDB key is configured); such
  // rows may have no tmdbId at all
  tvdbId?: number
  kind: "movie" | "show"
  title: string
  year: number
  overview: string
  poster: string
  popularity: number
}

// One show in the TVDB migration report.
export interface ApiMigrationRow {
  id: number
  title: string
  year?: number
  newTitle?: string
  newSeasons?: number
  reason?: string
}

// One file that is not where its naming template says it should be.
export interface ApiOrganizeMove {
  from: string
  to: string
  title: string
  kind: "movie" | "episode"
}

export interface ApiMovie {
  id: number
  tmdbId: number
  title: string
  year: number
  overview: string
  runtime: number
  genres: string[] | null
  releaseDate: string
  digitalRelease: string
  /** 'released' (default): automation waits until the movie is out; 'announced': no gate */
  minAvailability: string
  poster: string
  backdrop: string
  imdbId: string
  libraryId: number
  qualityProfileId: number
  monitored: boolean
  // set from the download client's queue, not stored on the row
  downloading?: boolean
  filePath: string
  fileSize: number
  quality: string
  source: string
  addedAt: string
  /** when the file landed; empty if there is none, or it arrived by scan */
  importedAt: string
  // which groups may see this title. Present for the owner only — it
  // is the shape of every household on the install.
  groupIds?: number[]
}

export interface ApiShow {
  id: number
  tmdbId: number
  title: string
  year: number
  overview: string
  status: string
  genres: string[] | null
  poster: string
  backdrop: string
  imdbId: string
  tvdbId?: number
  // release season = catalog season + offset, for shows TMDB splits
  // differently than release groups number them (revivals)
  seasonOffset?: number
  // which provider this show's metadata comes from: "tmdb" | "tvdb"
  source?: string
  libraryId: number
  qualityProfileId: number
  monitored: boolean
  // set from the download client's queue, not stored on the row
  downloading?: boolean
  addedAt: string
  episodes: number
  /** episodes whose air date has passed — the denominator for "missing" */
  aired: number
  onDisk: number
  // monitored, aired, and no file — what "missing" is allowed to mean
  wanted: number
  // monitored episodes that have aired: the denominator judgment runs on
  monitoredAired: number
  // which groups may see this title. Present for the owner only — it
  // is the shape of every household on the install.
  groupIds?: number[]
}

export interface ApiPreviewEpisode {
  season: number
  episode: number
  title: string
  overview: string
  airDate: string
  runtime: number
}

export interface ApiPreviewSeason {
  number: number
  name: string
  episodes: ApiPreviewEpisode[] | null
}

export interface ApiPreview {
  tmdbId: number
  tvdbId?: number
  kind: "movie" | "show"
  title: string
  year: number
  overview: string
  status?: string
  runtime?: number
  genres: string[] | null
  releaseDate?: string
  poster: string
  backdrop: string
  imdbId?: string
  cast?: ApiCastMember[] | null
  seasons?: ApiPreviewSeason[] | null
}

export interface ApiCastMember {
  // the person's own row — what tells two people apart when neither has
  // a tmdbId
  id: number
  // 0 for somebody reely only knows through TVDB: no filmography to open
  tmdbId: number
  name: string
  character: string
  photo: string
}

export interface ApiEpisode {
  id: number
  showId: number
  season: number
  episode: number
  title: string
  overview: string
  airDate: string
  runtime: number
  monitored: boolean
  // set from the download client's queue, not stored on the row
  downloading?: boolean
  filePath: string
  fileSize: number
  quality: string
  source: string
  // what this episode's RELEASES are numbered, from TheXEM, when the
  // scene disagrees with TheTVDB. Absent for almost every episode.
  sceneSeason?: number
  sceneEpisode?: number
}

// One track inside a file, as the media server reports it. reely asks
// Plex rather than opening the file: Plex probed it on the way in and
// already knows, and this way the answer matches what the player will
// actually be handed.
export interface ApiStream {
  kind: "video" | "audio" | "subtitle"
  codec: string
  language?: string
  title?: string
  channels?: number
  width?: number
  height?: number
  forced?: boolean
  default?: boolean
  // hearing-impaired: the same language as the plain track beside it
  sdh?: boolean
  // a subtitle sitting next to the file rather than inside it
  external?: boolean
}

// A request: somebody asked for a title in a library. The requester's own
// listing never carries username — they are told a title has been asked
// for, not by whom. Only the owner's queue names anyone.
export interface ApiRequest {
  id: number
  libraryId: number
  kind: "movie" | "show"
  tmdbId: number
  tvdbId?: number
  title: string
  year?: number
  poster: string
  seasons?: number[]
  status: "pending" | "approved" | "denied"
  createdAt: string
  username?: string
  libraryName?: string
}

export interface ApiSeason {
  number: number
  name: string
  episodes: ApiEpisode[] | null
}

export interface ApiMovieDetail extends ApiMovie {
  cast: ApiCastMember[] | null
}

export interface ApiPerson {
  tmdbId: number
  name: string
  biography: string
  birthday: string
  deathday: string
  placeOfBirth: string
  knownFor: string
  photo: string
}

export interface ApiWatchedList {
  id: number
  name: string
  source: "tmdb_chart" | "tmdb_list" | "trakt_chart" | "trakt_list" | "mdblist"
  config: string
  libraryId: number
  itemLimit: number
  enabled: boolean
  lastSynced?: string
  // who gets what this list adds, from here on. Empty means nobody,
  // which is what a list did before it could say.
  groupIds?: number[]
}

export interface ApiBlocklistEntry {
  id: number
  releaseTitle: string
  // which indexer's copy is banned; empty means the name is banned
  // everywhere, which is how bans made before scoping still read
  indexer?: string
  movieId?: number
  showId?: number
  forTitle?: string
  reason?: string
  createdAt: string
}

export interface ApiIndexerHealth {
  name: string
  enabled: boolean
  healthy: boolean
  disabledTill?: string
}

export interface ApiHealth {
  tmdb: { configured: boolean }
  prowlarr: {
    configured: boolean; ok: boolean; error?: string
    indexers: ApiIndexerHealth[]; warnings: string[]
  }
  sab: {
    configured: boolean; ok: boolean; error?: string
    version?: string; paused?: boolean; diskFreeGb?: number
  }
  /** qBittorrent, reported apart from SAB: an install may run both, and
   *  which one is unwell is the first thing worth knowing. */
  qbit: {
    configured: boolean; ok: boolean; error?: string
    version?: string; diskFreeGb?: number; torrents?: number
    /** qBittorrent's nearest thing to SAB's paused. */
    altSpeedOn?: boolean
  }
  plex: ApiPlexHealth
}

// The two Plex legs fail apart and for different reasons: plex.tv says
// who the server is shared with, the media server says where its
// libraries sit on disk. A wrong server address leaves the first
// perfectly happy while nobody reaches a library, so they are reported
// separately — and the mapping is what actually answers "is it working".
export interface ApiPlexHealth {
  configured: boolean
  ok: boolean
  error?: string
  serverOk: boolean
  serverError?: string
  /** What the server calls itself; absent when it couldn't be read. */
  serverName?: string
  people: number
  pending: number
  unmatched: number
  libraries: { title: string; key: string; type: string; matched?: string }[]
}

// localId is present only when the title is in a library this user may see.
export interface ApiPersonCredit {
  kind: "movie" | "show"
  tmdbId: number
  title: string
  year: number
  character: string
  poster: string
  localId?: number
  onDisk?: boolean
}

// What an automatic search did. For one title the search runs inline:
// grabbed names the release sent to SABnzbd, or reason says why nothing
// was. For a whole show the searches are queued and only queued is set.
export interface SearchNowResult {
  queued: number
  grabbed?: string
  reason?: string
}

export interface ApiRelease {
  title: string
  indexer: string
  protocol: string
  publishDate: string
  downloadUrl: string
  size: number
  quality: string
  source: string
  languages?: string[]
  hdr: boolean
  proper: boolean
  accepted: boolean
  formatScore: number
  terms?: string[]
  score: number
  reason?: string
}

export interface ApiShowDetail extends ApiShow {
  seasons: ApiSeason[] | null
  cast: ApiCastMember[] | null
}

export interface ApiQueueItem {
  nzo_id: string
  // which client is working on this row — the queue merges both
  protocol: string
  filename: string
  status: string
  cat: string
  mb: number
  mbleft: number
  percentage: number
  timeleft: string
  priority: string // Force / High / Normal / Low
}

// One search waiting its paced turn. The queue lives in memory, so this
// list empties when reely restarts.
export interface ApiQueuedSearch {
  key: string
  kind: "movie" | "episode"
  title: string
  movieId?: number
  showId?: number
  season?: number
  episode?: number
}

export interface ApiImportProblem {
  nzoId: string
  name: string
  error: string
}

export interface ApiHistoryEntry {
  id: number
  kind: string
  movieId?: number
  showId?: number
  episodeId?: number
  detail: string
  createdAt: string
  title?: string
  season?: number
  episode?: number
}

export interface ApiCalendarItem {
  kind: "episode" | "movie"
  date: string
  title: string
  season?: number
  episode?: number
  episodeTitle?: string
  movieId?: number
  showId?: number
  episodeId?: number
  libraryId: number
  onDisk: boolean
  // the title's artwork, so a row is recognisable at a glance; an
  // episode carries its show's
  poster?: string
  // the external ids, because the portal opens a title by its metadata
  // id — reely's own detail routes are not served out there
  tmdbId?: number
  tvdbId?: number
}

export interface ApiUploadResult {
  file: string
  season?: number
  episode?: number
  error?: string
}

async function request<T = unknown>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    headers: init?.body ? { "Content-Type": "application/json" } : undefined,
    ...init,
  })
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`
    try {
      const body = await res.json()
      if (body.error) message = body.error
    } catch { /* non-JSON error body */ }
    throw new Error(message)
  }
  // A 204 is a success with nothing in it, and several endpoints answer
  // that way — deleting a group, setting a title's groups, setting a
  // list's. Parsing an empty body throws, so each of those reported
  // failure in a toast for work that had already succeeded, while the
  // change itself had gone through.
  //
  // Read as text and parse only when there is something to parse, which
  // also covers a 200 that happens to come back empty.
  if (res.status === 204) return null as T
  const text = await res.text()
  return (text ? JSON.parse(text) : null) as T
}

async function uploadFiles<T = unknown>(path: string, files: FileList | File[], field = "files"): Promise<T> {
  const form = new FormData()
  for (const f of Array.from(files)) form.append(field, f)
  const res = await fetch(path, { method: "POST", body: form })
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`
    try {
      const body = await res.json()
      if (body.error) message = body.error
    } catch { /* non-JSON error body */ }
    throw new Error(message)
  }
  return res.json() as Promise<T>
}

export const auth = {
  // external is true when this page is served by the internet-facing
  // listener, where the admin routes don't exist at all. The app hides
  // the controls that would call them rather than rendering tabs whose
  // every request answers 404.
  me: () => request<{ authRequired: boolean; user?: ApiUser; external?: boolean }>(
    "/api/v1/auth/me"),
  login: (username: string, password: string) =>
    request<ApiUser>("/api/v1/auth/login", { method: "POST", body: JSON.stringify({ username, password }) }),
  logout: () => request("/api/v1/auth/logout", { method: "POST", body: "{}" }),
  users: () => request<{ users: ApiUser[] | null }>("/api/v1/users"),

  // Signing in with Plex is two calls: ask for a PIN, send the person to
  // plex.tv, then poll until they've approved it. "pending" is the
  // ordinary answer while they're still over there, not a failure.
  plexPin: () =>
    request<{ id: number; code: string; url: string; expiresIn: number }>(
      "/api/v1/auth/plex/pin", { method: "POST", body: "{}" }),
  plexCheck: (pinId: number, link = false) =>
    request<{ status: "ok" | "pending" | "linked"; user?: ApiUser
      account?: string; servers?: { name: string; machineId: string; owned: boolean }[] }>(
      `/api/v1/auth/plex/check${link ? "?link=1" : ""}`,
      { method: "POST", body: JSON.stringify({ pinId }) }),
  createUser: (username: string, password: string, role?: string, libraryIds?: number[]) =>
    request<ApiUser>("/api/v1/users", {
      method: "POST", body: JSON.stringify({ username, password, role, libraryIds }),
    }),
  deleteUser: (id: number) => request(`/api/v1/users/${id}`, { method: "DELETE" }),
  // How somebody signs in and what they may do are separate questions,
  // so this works on a Plex account as readily as a local one.
  setUserRole: (id: number, role: "admin" | "user") =>
    request<{ role: string }>(`/api/v1/users/${id}/role`, {
      method: "PUT", body: JSON.stringify({ role }),
    }),
  setUserLibraries: (id: number, libraryIds: number[]) =>
    request(`/api/v1/users/${id}/libraries`, {
      method: "PUT", body: JSON.stringify({ libraryIds }),
    }),
}

export const api = {
  status: () => request<{ app?: string; version?: string; status: string }>("/api/v1/system/status"),
  libraries: () => request<{ libraries: ApiLibrary[] | null }>("/api/v1/libraries"),
  createLibrary: (name: string, path: string, kind: "movies" | "shows") =>
    request<ApiLibrary>("/api/v1/libraries", {
      method: "POST", body: JSON.stringify({ name, path, kind }),
    }),
  removeLibrary: (id: number) => request(`/api/v1/libraries/${id}`, { method: "DELETE" }),
  scanLibrary: (id: number) =>
    request<{ status: string; library: string }>(`/api/v1/libraries/${id}/scan`, { method: "POST", body: "{}" }),
  // Organize is always two steps: the preview says exactly what would move,
  // and only an explicit apply touches a file.
  organizePreview: (id: number) =>
    request<{
      library: string; moves: ApiOrganizeMove[] | null; emptied: string[] | null
      // folders that will survive because something reely did not place
      // is still in them — named so nobody has to go hunting
      leftover: string[] | null
    }>(
      `/api/v1/libraries/${id}/organize`),
  organizeApply: (id: number) =>
    request<{
      moved: number; planned: number; removed: string[] | null
      kept: string[] | null
      errors: string[] | null
    }>(
      `/api/v1/libraries/${id}/organize`, { method: "POST", body: JSON.stringify({ apply: true }) }),
  movies: (libraryId?: number) =>
    request<{ movies: ApiMovie[] | null; imageBase: string }>(
      `/api/v1/movies${libraryId ? `?libraryId=${libraryId}` : ""}`),
  shows: (libraryId?: number) =>
    request<{ shows: ApiShow[] | null; imageBase: string }>(
      `/api/v1/shows${libraryId ? `?libraryId=${libraryId}` : ""}`),
  // groupIds is who the title is for. Omitted means the adder's own —
  // which is what most adds are; naming somebody else's group is how an
  // owner adds a title for whoever asked them for it.
  addMovie: (tmdbId: number, libraryId: number, groupIds?: number[]) =>
    request<{ movie: ApiMovie }>("/api/v1/movies", {
      method: "POST", body: JSON.stringify({ tmdbId, libraryId, groupIds }),
    }),
  addShow: (tmdbId: number, libraryId: number, monitoredSeasons?: number[],
            tvdbId?: number, groupIds?: number[]) =>
    request<{ show: ApiShow }>("/api/v1/shows", {
      method: "POST",
      body: JSON.stringify({ tmdbId, tvdbId: tvdbId ?? 0, libraryId,
        monitoredSeasons: monitoredSeasons ?? null, groupIds }),
    }),
  // src "tvdb": the id is a TVDB series id (show results when a TVDB key
  // is configured)
  preview: (kind: "movie" | "show", id: number, src?: "tvdb") =>
    request<{ preview: ApiPreview; imageBase: string; inLibraries: number[] | null }>(
      `/api/v1/preview/${kind}/${id}${src ? `?src=${src}` : ""}`),
  episode: (id: number) =>
    request<{
      episode: ApiEpisode
      show: { id: number; tmdbId: number; title: string; year: number; poster: string; backdrop: string; libraryId: number }
      imageBase: string
    }>(`/api/v1/episodes/${id}`),
  refreshTitle: (kind: "movie" | "show", id: number) =>
    request<{ ok: boolean }>(`/api/v1/${kind === "movie" ? "movies" : "shows"}/${id}/refresh`, { method: "POST" }),
  movie: (id: number) =>
    request<{ movie: ApiMovieDetail; imageBase: string }>(`/api/v1/movies/${id}`),
  // The tracks in one file, as Plex reports them. Movies and episodes
  // only — a show is many files and has no single answer. Always
  // succeeds: an install with no Plex gets an empty list.
  titleStreams: (kind: "movie" | "episode", id: number) =>
    request<{ streams: ApiStream[] }>(
      `/api/v1/${kind === "movie" ? "movies" : "episodes"}/${id}/streams`),
  show: (id: number) =>
    request<{ show: ApiShowDetail; imageBase: string }>(`/api/v1/shows/${id}`),
  person: (tmdbId: number) =>
    request<{ person: ApiPerson; credits: ApiPersonCredit[] | null; imageBase: string }>(
      `/api/v1/people/${tmdbId}`),
  health: () => request<ApiHealth>("/api/v1/health"),
  blocklist: (limit = 50, offset = 0) =>
    request<{ blocklist: ApiBlocklistEntry[]; total: number }>(`/api/v1/blocklist?limit=${limit}&offset=${offset}`),
  lists: () => request<{
    lists: ApiWatchedList[]
    // the groups a list's audience is chosen from — owner only, and
    // carried here so the portal never needs the install-wide sharing
    // endpoint, which stays LAN-only
    shareGroups: ApiGroup[] | null
    traktConfigured: boolean
    mdblistConfigured: boolean
  }>("/api/v1/lists"),
  createList: (l: { name: string; source: string; config: object; libraryId: number; itemLimit: number }) =>
    request<ApiWatchedList>("/api/v1/lists", { method: "POST", body: JSON.stringify(l) }),
  setListGroups: (id: number, groupIds: number[]) =>
    request(`/api/v1/lists/${id}/groups`, { method: "PUT", body: JSON.stringify({ groupIds }) }),
  setListEnabled: (id: number, enabled: boolean) =>
    request(`/api/v1/lists/${id}/enabled`, { method: "PUT", body: JSON.stringify({ enabled }) }),
  removeList: (id: number) => request(`/api/v1/lists/${id}`, { method: "DELETE" }),
  // added and requested are apart because they are different outcomes: a
  // list belonging to an account that asks files requests rather than
  // adding, and "0 added" would read as a failed sync
  syncList: (id: number) =>
    request<{ added: number; requested: number }>(
      `/api/v1/lists/${id}/sync`, { method: "POST", body: "{}" }),
  stats: () => request<ApiStats>("/api/v1/stats"),
  backups: () => request<{ backups: ApiBackup[] | null }>("/api/v1/backups"),
  createBackup: () => request<ApiBackup>("/api/v1/backups", { method: "POST", body: "{}" }),
  deleteBackup: (name: string) => request(`/api/v1/backups/${encodeURIComponent(name)}`, { method: "DELETE" }),
  restoreBackup: (name: string) =>
    request<{ staged: boolean }>("/api/v1/restore", { method: "POST", body: JSON.stringify({ name }) }),
  uploadRestore: (file: File) => uploadFiles<{ staged: boolean }>("/api/v1/restore", [file], "file"),
  importMoviePath: (id: number, path: string) =>
    request<{ status: string }>(`/api/v1/movies/${id}/import-path`, { method: "POST", body: JSON.stringify({ path }) }),
  importShowPath: (id: number, path: string) =>
    request<{ results: ApiUploadResult[] | null }>(`/api/v1/shows/${id}/import-path`, { method: "POST", body: JSON.stringify({ path }) }),
  listsCadence: () => request<{ minutes: number }>("/api/v1/lists/cadence"),
  putListsCadence: (minutes: number) =>
    request<{ minutes: number }>("/api/v1/lists/cadence", { method: "PUT", body: JSON.stringify({ minutes }) }),
  mdblistKey: () => request<{ set: boolean }>("/api/v1/mdblist/key"),
  putMdblistKey: (value: string) =>
    request("/api/v1/mdblist/key", { method: "PUT", body: JSON.stringify({ value }) }),
  blocklistRemove: (ids: number[]) =>
    request<{ removed: number }>("/api/v1/blocklist/remove", {
      method: "POST", body: JSON.stringify({ ids }),
    }),
  setMovieMonitored: (id: number, monitored: boolean) =>
    request(`/api/v1/movies/${id}/monitor`, { method: "PUT", body: JSON.stringify({ monitored }) }),
  setShowMonitored: (id: number, monitored: boolean) =>
    request(`/api/v1/shows/${id}/monitor`, { method: "PUT", body: JSON.stringify({ monitored }) }),
  setSeasonMonitored: (showId: number, season: number, monitored: boolean) =>
    request(`/api/v1/shows/${showId}/seasons/${season}/monitor`,
      { method: "PUT", body: JSON.stringify({ monitored }) }),
  setEpisodeMonitored: (id: number, monitored: boolean) =>
    request(`/api/v1/episodes/${id}/monitor`, { method: "PUT", body: JSON.stringify({ monitored }) }),
  // requests — the open ones in the libraries this account can reach
  // mine narrows it to your own asks. The wide listing is what Explore
  // reads to say "already requested", so it stays the default.
  requests: (mine?: boolean) =>
    request<{ requests: ApiRequest[] }>(`/api/v1/requests${mine ? "?mine=1" : ""}`),
  createRequest: (body: {
    kind: "movie" | "show"; tmdbId?: number; tvdbId?: number
    title: string; year?: number; poster?: string; seasons?: number[]; libraryId?: number
    // which of your own groups this should reach once approved. Absent
    // means all of them; an empty list keeps it to you.
    audience?: number[]
  }) => request<{ id: number; status: string }>("/api/v1/requests", {
    method: "POST", body: JSON.stringify(body),
  }),
  setDefaultLibrary: (libraryId: number) =>
    request<{ libraryId: number }>("/api/v1/users/me/default-library", {
      method: "PUT", body: JSON.stringify({ libraryId }),
    }),
  requestQueue: () =>
    request<{ requests: ApiRequest[]; imageBase: string }>("/api/v1/requests/queue"),
  decideRequest: (id: number, decision: "approve" | "deny") =>
    request<{ status: string }>(`/api/v1/requests/${id}/${decision}`, { method: "POST" }),
  setRequestSettings: (userId: number, body: {
    mayAdd: boolean; autoApproveMovies: boolean; autoApproveShows: boolean
    quotaMoviesWeek: number | null; quotaShowsWeek: number | null
  }) => request(`/api/v1/users/${userId}/requests`, { method: "PUT", body: JSON.stringify(body) }),
  // linked is the install (a token that reads the sharing list);
  // accountLinked is whether the signed-in admin's own account is tied to
  // a Plex identity, which is what lets them sign in with Plex.
  plexStatus: () =>
    request<{ linked: boolean; accountLinked: boolean; machineId: string; serverUrl: string }>(
      "/api/v1/plex/status"),
  plexSync: () =>
    request<{ accounts: number; deactivated: number; unmatched: number; pendingInvites: number }>(
      "/api/v1/plex/sync", { method: "POST", body: "{}" }),
  plexUnlink: () => request("/api/v1/plex/link", { method: "DELETE" }),
  // the same check the health panel shows, so the two can't disagree
  plexTest: () => request<ApiPlexHealth>("/api/v1/plex/test"),
  formats: () => request<{ formats: ApiCustomFormat[] }>("/api/v1/formats"),
  importFormats: (formats: Omit<ApiCustomFormat, "id">[]) =>
    request<{ imported: number }>("/api/v1/formats", { method: "POST", body: JSON.stringify({ formats }) }),
  setFormatScore: (id: number, score: number) =>
    request(`/api/v1/formats/${id}`, { method: "PUT", body: JSON.stringify({ score }) }),
  deleteFormat: (id: number) => request(`/api/v1/formats/${id}`, { method: "DELETE" }),
  trashFormats: (kind: "movies" | "shows") =>
    request<{ formats: (Omit<ApiCustomFormat, "id"> & { trashId: string })[] | null }>(
      `/api/v1/trash/formats?kind=${kind}`),
  recentEpisodes: () =>
    request<{ shows: ApiRecentShowImport[] | null; imageBase: string }>("/api/v1/episodes/recent"),
  explore: () =>
    request<{
      movies: ApiSearchResult[] | null; shows: ApiSearchResult[] | null
      popularMovies: ApiSearchResult[] | null; popularShows: ApiSearchResult[] | null
      topMovies: ApiSearchResult[] | null; topShows: ApiSearchResult[] | null
      providers: ApiProviderRow[] | null
      imageBase: string
    }>("/api/v1/explore"),
  // "more like this" for one title — a live TMDB call, per title
  similar: (kind: "movie" | "tv", tmdbId: number) =>
    request<{ results: ApiSearchResult[] | null; imageBase: string }>(`/api/v1/similar/${kind}/${tmdbId}`),
  search: (q: string, kind?: "movie" | "show") =>
    request<{ results: ApiSearchResult[] | null; imageBase: string }>(
      `/api/v1/search?q=${encodeURIComponent(q)}${kind ? `&kind=${kind}` : ""}`),
  // Re-match: this file is not the film reely thinks it is. Only the
  // identity and what follows from it change — the file, library,
  // profile and history stay.
  rematchMovie: (id: number, tmdbId: number, rename: boolean) =>
    request<{ movie: ApiMovie; renamed: number }>(`/api/v1/movies/${id}/rematch`, {
      method: "POST", body: JSON.stringify({ tmdbId, rename }),
    }),
  rematchShow: (id: number, tmdbId: number | undefined, tvdbId: number | undefined, rename: boolean) =>
    request<{ show: ApiShow; renamed: number }>(`/api/v1/shows/${id}/rematch`, {
      method: "POST",
      body: JSON.stringify({ tmdbId: tmdbId ?? 0, tvdbId: tvdbId ?? 0, rename }),
    }),
  // what a re-match would rename, so the dialog can name the files
  // instead of asking for a blind yes
  rematchFiles: (kind: "movie" | "show", id: number) =>
    request<ApiRenameOutlook>(`/api/v1/${kind === "movie" ? "movies" : "shows"}/${id}/rematch/files`),
  profiles: () => request<{ profiles: ApiQualityProfile[] | null }>("/api/v1/profiles"),
  createProfile: (p: Omit<ApiQualityProfile, "id">) =>
    request<ApiQualityProfile>("/api/v1/profiles", { method: "POST", body: JSON.stringify(p) }),
  updateProfile: (id: number, p: Omit<ApiQualityProfile, "id">) =>
    request<ApiQualityProfile>(`/api/v1/profiles/${id}`, { method: "PUT", body: JSON.stringify(p) }),
  removeProfile: (id: number) => request(`/api/v1/profiles/${id}`, { method: "DELETE" }),
  setLibraryProfile: (libraryId: number, qualityProfileId: number) =>
    request(`/api/v1/libraries/${libraryId}/profile`, {
      method: "PUT", body: JSON.stringify({ qualityProfileId }),
    }),
  setMovieProfile: (id: number, qualityProfileId: number) =>
    request(`/api/v1/movies/${id}/profile`, {
      method: "PUT", body: JSON.stringify({ qualityProfileId }),
    }),
  setMovieAvailability: (id: number, minAvailability: "released" | "announced") =>
    request(`/api/v1/movies/${id}/availability`, {
      method: "PUT", body: JSON.stringify({ minAvailability }),
    }),
  setShowProfile: (id: number, qualityProfileId: number) =>
    request(`/api/v1/shows/${id}/profile`, {
      method: "PUT", body: JSON.stringify({ qualityProfileId }),
    }),
  // release-numbering mapping for shows TMDB splits differently than the
  // scene numbers them; tvdbId 0 keeps TMDB's own external id
  setShowNumbering: (id: number, seasonOffset: number, tvdbId: number) =>
    request(`/api/v1/shows/${id}/numbering`, {
      method: "PUT", body: JSON.stringify({ seasonOffset, tvdbId }),
    }),
  // Searching one title runs inline and answers with the release it sent
  // to SABnzbd, or the reason it sent nothing. A whole show is queued
  // instead — only `queued` comes back for those.
  searchMovieNow: (id: number) =>
    request<SearchNowResult>(`/api/v1/movies/${id}/search`, { method: "POST", body: "{}" }),
  searchShowNow: (id: number, season = 0, episode = 0) =>
    request<SearchNowResult>(`/api/v1/shows/${id}/search`, {
      method: "POST", body: JSON.stringify({ season, episode }),
    }),
  // Everything monitored and still missing in one kind, optionally within
  // one library. Always queued — this can be hundreds of searches.
  searchMissing: (kind: "movies" | "shows", libraryId = 0) =>
    request<{ queued: number }>("/api/v1/search/missing", {
      method: "POST", body: JSON.stringify({ kind, libraryId }),
    }),
  deleteMovie: (id: number, deleteFiles: boolean) =>
    request(`/api/v1/movies/${id}${deleteFiles ? "?deleteFiles=true" : ""}`, { method: "DELETE" }),
  deleteShow: (id: number, deleteFiles: boolean) =>
    request(`/api/v1/shows/${id}${deleteFiles ? "?deleteFiles=true" : ""}`, { method: "DELETE" }),
  // deletes the file(s) but keeps the title in the library, monitored as
  // it was — a replacement search queues right away
  deleteMovieFile: (id: number) =>
    request<{ filesDeleted: number; queued: number }>(`/api/v1/movies/${id}/file`, { method: "DELETE" }),
  deleteShowFiles: (id: number) =>
    request<{ filesDeleted: number; queued: number }>(`/api/v1/shows/${id}/files`, { method: "DELETE" }),
  movieReleases: (id: number) =>
    request<{ releases: ApiRelease[] | null }>(`/api/v1/movies/${id}/releases`),
  showReleases: (id: number, season: number, episode?: number) =>
    request<{ releases: ApiRelease[] | null }>(
      `/api/v1/shows/${id}/releases?season=${season}${episode ? `&episode=${episode}` : ""}`),
  grabMovie: (id: number, r: ApiRelease) =>
    request(`/api/v1/movies/${id}/grab`, {
      method: "POST",
      body: JSON.stringify({
        title: r.title, downloadUrl: r.downloadUrl, indexer: r.indexer,
        protocol: r.protocol, size: r.size,
      }),
    }),
  grabShow: (id: number, r: ApiRelease, season: number, episode?: number) =>
    request(`/api/v1/shows/${id}/grab`, {
      method: "POST",
      body: JSON.stringify({
        title: r.title, downloadUrl: r.downloadUrl, indexer: r.indexer,
        protocol: r.protocol, size: r.size,
        season, episode: episode ?? 0,
      }),
    }),
  calendar: (days = 30) =>
    request<{
      upcoming: ApiCalendarItem[] | null
      missing: ApiCalendarItem[] | null
      imageBase: string
    }>(`/api/v1/calendar?days=${days}`),
  activity: (page = 50,
    offsets: { history?: number; queue?: number; searches?: number } = {},
    filters: { queue?: string; searches?: string } = {}) =>
    request<{
      queue: ApiQueueItem[] | null
      queueTotal: number
      problems: ApiImportProblem[] | null
      history: ApiHistoryEntry[] | null
      historyTotal: number
      searches: ApiQueuedSearch[] | null
      searchesTotal: number
    }>(`/api/v1/activity?historyLimit=${page}&historyOffset=${offsets.history ?? 0}` +
      `&queueLimit=${page}&queueOffset=${offsets.queue ?? 0}` +
      `&searchLimit=${page}&searchOffset=${offsets.searches ?? 0}` +
      `&queueFilter=${encodeURIComponent(filters.queue ?? "")}` +
      `&searchFilter=${encodeURIComponent(filters.searches ?? "")}`),
  // all: true cancels everything the caller is looking at — the whole
  // queue, or everything matching filter when one is set. blocklist bans
  // each cancelled release so the automatic paths don't re-grab it.
  activityCancel: (arg: { nzoIds?: string[]; all?: boolean; filter?: string; blocklist?: boolean }) =>
    request<{ cancelled: number; failed: number }>("/api/v1/activity/cancel", {
      method: "POST",
      body: JSON.stringify({
        nzoIds: arg.nzoIds ?? [], all: arg.all ?? false, filter: arg.filter ?? "",
        blocklist: arg.blocklist ?? false,
      }),
    }),
  // SAB's own priority scale: 2 Force (starts immediately), 1 High,
  // 0 Normal, -1 Low
  activityPriority: (nzoIds: string[], priority: number) =>
    request<{ changed: number; failed: number }>("/api/v1/activity/priority", {
      method: "POST",
      body: JSON.stringify({ nzoIds, priority }),
    }),
  // holds a download where it is, keeping what has already arrived, and
  // sets it going again — per job, so the rest of the queue runs on
  activityPause: (nzoIds: string[]) =>
    request<{ changed: number; failed: number }>("/api/v1/activity/pause", {
      method: "POST",
      body: JSON.stringify({ nzoIds }),
    }),
  activityResume: (nzoIds: string[]) =>
    request<{ changed: number; failed: number }>("/api/v1/activity/resume", {
      method: "POST",
      body: JSON.stringify({ nzoIds }),
    }),
  // bans the release a "grabbed" history row records — the undo for a
  // grab that turned out to be the wrong file
  historyBlocklist: (id: number) =>
    request<{ blocked: string }>(`/api/v1/history/${id}/blocklist`, { method: "POST", body: "{}" }),
  searchesCancel: (arg: { keys?: string[]; all?: boolean; filter?: string }) =>
    request<{ cancelled: number }>("/api/v1/activity/searches/cancel", {
      method: "POST",
      body: JSON.stringify({ keys: arg.keys ?? [], all: arg.all ?? false, filter: arg.filter ?? "" }),
    }),
  activityRetry: (nzoId: string) =>
    request("/api/v1/activity/retry", { method: "POST", body: JSON.stringify({ nzoId }) }),
  activityDelete: (nzoId: string) =>
    request("/api/v1/activity/delete", { method: "POST", body: JSON.stringify({ nzoId }) }),
  // season pins every file of the job to that season; episode pins a
  // single-file job to one exact episode (both optional, show only)
  activityResolve: (nzoId: string, target: { movieId?: number; showId?: number; season?: number; episode?: number }) =>
    request("/api/v1/activity/resolve", {
      method: "POST", body: JSON.stringify({
        nzoId, movieId: target.movieId ?? 0, showId: target.showId ?? 0,
        season: target.season ?? 0, episode: target.episode ?? 0,
      }),
    }),
  uploadMovie: (id: number, files: FileList | File[]) => uploadFiles(`/api/v1/movies/${id}/upload`, files),
  uploadShow: (id: number, files: FileList | File[]) =>
    uploadFiles<{ results: ApiUploadResult[] | null }>(`/api/v1/shows/${id}/upload`, files),
  getSetting: (key: string) =>
    request<{ key: string; value?: string; set?: boolean }>(`/api/v1/settings/${key}`),
  qualityRepairStatus: () =>
    request<{ pending: number; pendingQuality: number; pendingSource: number }>(
      "/api/v1/system/quality-repair"),
  qualityRepair: () =>
    request<{ qualityFixed: number; sourceFixed: number; unknown: number }>(
      "/api/v1/system/quality-repair", { method: "POST" }),
  // TVDB migration: the admin sweep that re-sources shows from TMDB to
  // TheTVDB, plus the per-show manual fix for what the sweep couldn't place
  tvdbMigrationStatus: () =>
    request<{
      enabled: boolean; tmdbShows: number; tvdbShows: number; ready: number
      unresolved: ApiMigrationRow[] | null
    }>("/api/v1/system/tvdb-migration"),
  tvdbMigrate: () =>
    request<{
      migrated: ApiMigrationRow[] | null
      unresolved: ApiMigrationRow[] | null
      failed: ApiMigrationRow[] | null
    }>("/api/v1/system/tvdb-migration", { method: "POST", body: "{}" }),
  migrateShowTvdb: (id: number, tvdbId: number) =>
    request<ApiMigrationRow>(`/api/v1/shows/${id}/migrate-tvdb`, {
      method: "POST", body: JSON.stringify({ tvdbId }),
    }),
  putSetting: (key: string, value: string) =>
    request(`/api/v1/settings/${key}`, { method: "PUT", body: JSON.stringify({ value }) }),
  // renders both naming templates against a fixed example, using the same
  // code the importer does, so the preview can't drift from the real thing
  namingPreview: (movie: string, show: string) =>
    request<{
      movie: { path: string; error: string }
      show: { path: string; error: string }
      tokens: { movie: string[]; show: string[] }
      defaults: { movie: string; show: string }
    }>("/api/v1/settings/naming/preview", {
      method: "POST", body: JSON.stringify({ movie, show }),
    }),

  // Sharing. All of it is the owner's — none is reachable from the
  // portal, so none of it is called from the requesting side.
  groups: () => request<ApiGroup[]>("/api/v1/sharing/groups"),
  createGroup: (name: string) =>
    request<ApiGroup>("/api/v1/sharing/groups", {
      method: "POST", body: JSON.stringify({ name }),
    }),
  renameGroup: (id: number, name: string) =>
    request<ApiGroup>(`/api/v1/sharing/groups/${id}`, {
      method: "PATCH", body: JSON.stringify({ name }),
    }),
  // The Plex webhook: reely learns the moment Plex has SCANNED a new
  // item, which is the moment sharing can label it. Needs Plex Pass.
  plexHook: () => request<{ set: boolean; url: string }>("/api/v1/plex/webhook/token"),
  newPlexHookToken: () =>
    request<{ token: string }>("/api/v1/plex/webhook/token", { method: "POST" }),
  clearPlexHookToken: () =>
    request("/api/v1/plex/webhook/token", { method: "DELETE" }),
  setGroupEveryone: (id: number, everyone: boolean) =>
    request(`/api/v1/sharing/groups/${id}/everyone`,
      { method: "PUT", body: JSON.stringify({ everyone }) }),
  deleteGroup: (id: number) =>
    request(`/api/v1/sharing/groups/${id}`, { method: "DELETE" }),
  setGroupMembers: (id: number, userIds: number[]) =>
    request<ApiGroup>(`/api/v1/sharing/groups/${id}/members`, {
      method: "PUT", body: JSON.stringify({ userIds }),
    }),
  // A show is addressed by whichever id we hold: one sourced from
  // TheTVDB may have no TMDB id at all.
  titleGroups: (kind: "movie" | "show", id: number, source?: "tvdb") =>
    request<{ viewers: ApiViewer[]; groupIds: number[] }>(
      `/api/v1/sharing/titles/${kind}/${id}${source ? `?source=${source}` : ""}`),
  setTitleGroups: (kind: "movie" | "show", id: number, groupIds: number[],
                   title: string, source?: "tvdb") =>
    request(`/api/v1/sharing/titles/${kind}/${id}${source ? `?source=${source}` : ""}`, {
      method: "PUT", body: JSON.stringify({ groupIds, title }),
    }),
  // No group to pick: the backfill group is assembled rather than
  // chosen — everybody who can already see the library is in it, and
  // everything already there goes to it.
  // Many titles at once — its own call rather than a loop, because the
  // per-title one schedules a Plex pass each time.
  bulkShare: (
    mode: "add" | "remove" | "replace",
    groupIds: number[],
    titles: { kind: "movie" | "show"; tmdbId?: number; tvdbId?: number; title: string }[],
  ) => request<{ changed: number }>("/api/v1/sharing/titles/bulk", {
    method: "POST", body: JSON.stringify({ mode, groupIds, titles }),
  }),
  seedSharing: (name?: string) =>
    request<{ added: number; members: number; group: ApiGroup }>(
      "/api/v1/sharing/seed", { method: "POST", body: JSON.stringify({ name }) }),
  shareStates: () => request<ApiShareState[]>("/api/v1/sharing/users"),
  plexAccounts: () =>
    request<{ accounts: ApiPlexAccount[] | null }>("/api/v1/sharing/plex-accounts"),
  setManaged: (id: number, managed: boolean, shareAccountId?: number) =>
    request<{ managed: boolean }>(`/api/v1/sharing/users/${id}/managed`, {
      method: "PUT", body: JSON.stringify({ managed, shareAccountId }),
    }),
  // your own groups — yours and any household you are in. Safe on the
  // portal: it is not the install-wide list.
  myGroups: () => request<ApiMyGroup[]>("/api/v1/sharing/me/groups"),
  reconcileSharing: () =>
    request<ApiSharingResult>("/api/v1/sharing/reconcile", { method: "POST", body: "{}" }),
}
