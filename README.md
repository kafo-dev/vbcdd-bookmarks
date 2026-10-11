# vbcdd-bookmarks

Termux tooling around the bookmark file a browser syncs with [Floccus](https://github.com/floccusaddon/floccus). The share sheet appends a link to a folder of its own, and a picker lists the whole tree for a fuzzy search and opens the choice.

> **Warning: Large Language Model-Generated Code**
>
> This code functions as of the time of writing, yet it has received little to no human review. It must not be regarded as reliable: bugs and stability issues are to be expected.

The program reads and writes one XBEL file over WebDAV. Floccus owns that file for every desktop browser; this program speaks the same protocol, so the two never write at once.

## Storage contract

`docs/bookmark-sync.md` carries the full reconciliation rules. The parts a writer must honour:

- **One file per profile.** The file name and its WebDAV directory come from the configuration.
- **XBEL only.** The file carries `<?xml version="1.0" encoding="UTF-8"?>`; Floccus rejects it otherwise.
- **The counter line.** `<!--- highestId :N: for Floccus bookmark sync browser extension -->` holds the next id Floccus assigns, which is `N+1`. A writer that lowers the counter makes Floccus reuse ids and lose items, so the value never drops below the highest id in the file.
- **The `Phone` folder.** A `<folder>` titled `Phone` that sits directly under `<xbel>`. The writer owns that folder and appends to its child list; every other element belongs to the browser. A `<folder>` appears in the browser as a folder.
- **The lock.** `<file>.lock` holds `<html><body>I am a lock file</body></html>`. A lock younger than 15 minutes belongs to a running sync; an older one is abandoned. Every writer removes the lock on every exit path, including failure, and a leftover lock blocks Floccus for the whole stale window.
- **The write.** `PUT <file>.temp`, then `MOVE` onto `<file>`, so a reader never sees a partial file. Floccus 5.x itself writes with a plain `PUT` and reads either way.
- **The read.** Floccus checks the length from `PROPFIND` against the body it fetched, and so does this program. A WebDAV server without `PROPFIND` fails every read; nginx's DAV module lacks it, while Apache `mod_dav`, Caddy's `webdav` directive, rclone `serve webdav`, SabreDAV and Nextcloud provide it.

A changed `href` counts as a removal plus a creation in Floccus, never as an edit.

## Build

```sh
go build .
```

The phone builds the binary with its own Go toolchain, which links the Android C library and reaches the system resolver. A cross-compiled `linux/arm` binary carries no `resolv.conf` on Android and cannot resolve a host name.

## Install

`install.sh` builds into `~/bin`, links the three hook names, installs the widget shortcut, and seeds the configuration file:

```sh
./install.sh
```

The layout on the phone:

| Path | Role |
|------|------|
| `~/.config/vbcdd/bookmarks/config.toml` | Endpoint, file name, credentials; mode 600 |
| `~/.cache/vbcdd/bookmarks/<file>` | Last fetched copy, for an offline read |
| `~/bin/bookmarks` | The binary |
| `~/bin/bookmarks-add`, `~/bin/bookmarks-pick` | Command names |
| `~/bin/termux-url-opener` | Android share hook, from Termux:API |
| `~/.shortcuts/Bookmarks` | Widget entry that runs the picker |

`~/bin` holds no place on the default Termux `PATH`, and the share hook needs none: Termux:API starts `~/bin/termux-url-opener` by path. A shell that calls `bookmarks` by name adds the directory to `PATH`.

Packages: `fzf` for the picker, `termux-api` plus the Termux:API app for the share hook, the toast, and the URL opener, and `golang` to build.

## Configuration

`~/.config/vbcdd/bookmarks/config.toml` holds four fields:

```toml
url = "https://webdav.example.org/"
file = "Documents/bookmarks.xbel"
user = "username"
password = "password"
```

`url` is the WebDAV directory with a trailing slash, `file` is the path below it. The directory itself must exist, as it does for a Floccus profile: the writer creates the file and not the directory. The tool creates the cache directory, and the file arrives on the first write.

The matching Floccus profile uses the same URL, the same file name, the file type `xbel`, HTTP Basic credentials, and an empty passphrase: an encrypted file needs AES-GCM, which this program does not implement. Select the browser folder to sync in the profile, and the `Phone` folder arrives as a top-level folder beside it. See the desktop section of `docs/bookmark-sync.md`.

## Commands

```sh
bookmarks add <url>          # append a link to the Phone folder
bookmarks pick [--delete]    # fzf over every bookmark, Enter opens the choice
bookmarks unlock             # remove a leftover lock file
bookmarks version
```

`--config PATH` selects another configuration file. `add` takes `--title` to skip the page fetch; without it the program reads `<title>` with an eight second ceiling and stores the URL when the page does not answer.

Inside the picker:

| Key | Action |
|-----|--------|
| type | Fzf matches the title, the folder, and the host |
| Enter | Open the choice with the Android URL handler |
| `ctrl-p` | Limit the list to the `Phone` folder |
| `ctrl-d` | delete the choice, after a confirmation (`--delete` only) |

Deletion removes the element from the file, and Floccus drops the bookmark from the browser on the next sync.

## Failure modes

| Symptom | Cause | Action |
|---------|-------|--------|
| `held by another writer` | A lock younger than 15 minutes | Wait for the sync, or run `bookmarks unlock` when no sync runs |
| Floccus reports a resource-locked error | A leftover lock | Run `bookmarks unlock` |
| The picker shows an offline copy | The read failed and the cache answered | Check the endpoint and the credentials |
| Floccus reports a file-unreadable error | The file lost its XML declaration | Restore the file from the cache or a copy |
| Duplicate bookmarks after a sync | The counter line fell below an id in the file | Restore a copy, then resync |
| Links from the phone never arrive | The `Phone` folder sits below the root of the file | Place it as a direct child of `<xbel>` |
| Every sync fails on the read | The WebDAV server lacks `PROPFIND` | Move to a server that provides it |

## Development

```sh
./scripts/check      # gofumpt, go vet, staticcheck, golangci-lint, tests, shell
git config core.hooksPath scripts
```

The tests run a WebDAV directory in memory, so they cover the lock, the temporary-file write and the counter without a server. `xbel_test.go` pins the serializer to the bytes Floccus writes.

## License

Released into the public domain under the [Unlicense](LICENSE).
