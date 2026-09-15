# Libraries and Collections

A **library** is a folder with a kind: movie libraries hold movies, show
libraries hold shows. Every library has its own quality profile (the
default new titles inherit) and its own place in the sidebar.

## Per-user collections, one file on disk

The same title may live in any number of libraries — that's how each
person gets their own collection. What makes it cheap is the hardlink
model:

- When a download imports into one library, the file is **hardlinked into
  every sibling collection** that wants the title. Each library's folder
  shows a complete, normally-named file; the bytes exist once.
- When someone **adds a title another collection already has**, the file
  links in instantly — collection complete, no download, no wait.
- When a **quality upgrade** lands anywhere, every hard-linked copy is
  lifted with it (a sibling already holding something better is left
  alone).
- When one person **deletes with files**, only their library's directory
  entries go. A hard-linked copy in another collection is a separate
  directory entry — it survives, and the bytes stay until the last
  library lets go.

The delete dialog spells this out every time: *Remove from library* keeps
files where they are; *Remove and delete files* deletes this library's
copies and reminds you that other collections' copies survive; and
*Delete files, keep in library* is for a wrong or broken file — the
title stays, monitored as it was, and a search for a replacement queues
immediately.

"On disk" also means on disk, always: a title's detail page verifies its
files against the filesystem as it loads, and the library scan
reconciles the whole library the same way — so a file deleted outside
reely (a file manager, another tool) stops showing as present and goes
back to wanted without any button-pressing. Neither check will clear
anything while the library folder itself is unreachable, so a briefly
unmounted share can't strip your library and trigger re-downloads.

For all of this to work, downloads and libraries must share one
filesystem — see [Getting Started](Getting-Started.md#install-docker--unraid).

## Roles and visibility

- **Admins** see everything: all libraries, plus a combined Movies/Shows
  view where a title in several collections appears once (the on-disk
  copy wins).
- **Users** are assigned libraries. Everything else is invisible — not
  just hidden in the UI: list endpoints filter server-side, and a title
  outside their scope answers **404**, so its ID confirms nothing.
- Anyone may add titles into a library they have access to, run its
  watched lists, search, upload, or delete within it. Install-wide
  configuration stays admin-only.

## Adding titles

Search (top bar) covers TMDB; adding lands the title monitored in the
chosen library and immediately queues a search for it. On any title's
page, **Add to library** puts it into another collection you can access —
instantly hard-linked when a sibling already has the file. Libraries can
also be **scanned**: existing files on disk are matched against TMDB and
attached in place.
