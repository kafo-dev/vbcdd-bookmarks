# Bookmark Sync: Floccus on the Desktop, fzf in Termux

## Purpose

Keep one bookmark file in a WebDAV directory. Desktop browsers sync their own bookmark trees to that file with Floccus. Termux on the phone reads the same file for a fuzzy picker and appends links sent through the Android share sheet into a folder titled `Phone`.

## Architecture

```text
desktop browser  <-- Floccus, WebDAV -->  WebDAV directory on a server
                                                    |
                                    Termux on the phone
                                      - share hook: append to the Phone folder
                                      - fzf picker: read, open in browser
```

Floccus owns the file. Termux reads and writes the same file through the same lock and write protocol, so the two never write at once.

## Storage contract

### File and formats

One file per Floccus profile. Default name `bookmarks.xbel`, default type `xbel`. Two formats are valid:

| Type | Content type | Reader check |
|---|---|---|
| `xbel` | `application/xml` | the file contains `<?xml version="1.0" encoding="UTF-8"?>` |
| `html` | `text/html` | the file contains `<!DOCTYPE NETSCAPE-Bookmark-file-1>` |

Use `xbel`. It is strict XML with one element per line, which removes the escaping traps of Netscape HTML.

### Tree shape

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE xbel PUBLIC "+//IDN python.org//DTD XML Bookmark Exchange Language 1.0//EN//XML" "http://pyxml.sourceforge.net/topics/dtds/xbel.dtd">
<xbel version="1.0">
<!--- highestId :42: for Floccus bookmark sync browser extension -->
  <bookmark href="https://example.org" id="7">
    <title>Example</title>
  </bookmark>
  <folder id="1">
    <title>Phone</title>
    <bookmark href="https://example.net" id="41">
      <title>Saved on the phone</title>
    </bookmark>
  </folder>
</xbel>
```

The root folder carries id 0 and is the `<xbel>` element itself. A `<bookmark>` takes `href` and `id` attributes and one `<title>` child. A `<folder>` takes an `id` attribute and one `<title>` child.

### Counter line

- The line holds `<!--- highestId :`.
- The value sits between the first colon of the line and the last colon of the line. Place no other colon after the value on that line.
- The value stays at or above the maximum id in the file. Floccus assigns server ids as `++highestId`.
- A writer that omits the update makes Floccus reuse ids, which loses items.

### Identifiers and order

- Integer ids, unique within the file.
- Children append to the end of a folder's child list. That order is the display order.

### Folders

The children of the `<xbel>` element are the children of the browser folder selected in the Floccus profile. The profile option for that folder is the local folder.

One subfolder belongs to the phone:

- A `<folder>` with a `<title>` of exactly `Phone`, as a direct child of `<xbel>`.
- The writer looks for it among the direct children of `<xbel>` only, never at a deeper level, and creates it when absent.
- Bookmarks from the phone append to the end of the child list of that folder and of no other.
- A folder element without a mapping appears in the browser as a folder, the same rule that carries an appended bookmark across.

### Lock

- Path: `<file>.lock`, content type `text/html`, body `<html><body>I am a lock file</body></html>`.
- Floccus reads the lock before a sync: 404 means free; 200 with a `Last-Modified` within 15 minutes means busy and the cycle fails with a resource-locked error; 200 with a `Last-Modified` older than 15 minutes counts as stale and the cycle proceeds; 200 without `Last-Modified` counts as busy.
- Floccus refreshes the lock every 2 minutes during a sync and deletes it at the end.
- Every writer deletes the lock in all exit paths, including failure. A leftover lock blocks Floccus for 15 minutes.

### Write

- `PUT <file>.temp`, then `MOVE <file>.temp` to `<file>` with header `Destination: <file>` and `Overwrite: T`. On a 409 the destination exists; delete it and repeat the MOVE.
- Content type `application/xml` for XBEL, `text/html` for Netscape HTML.
- A plain `PUT` onto `<file>` also works and skips the atomic rename.

### WebDAV server requirements

| Method | Purpose |
|---|---|
| `GET` | Read the file and the lock. |
| `PUT` | Write the file, the temporary file, and the lock. |
| `MOVE` | Replace the file in one step. |
| `DELETE` | Remove the lock and the temporary file. |
| `PROPFIND`, `Depth: 0` | The file size check Floccus performs on every read. |

A server without `PROPFIND` fails every read: nginx's `ngx_http_dav_module` implements DAV without it. Apache `mod_dav`, rclone `serve webdav`, WsgiDAV, SabreDAV and Nextcloud satisfy the list. Authentication is HTTP Basic.

## Merge rules

Floccus stores, per profile, a mapping of local bookmark id to server id with an item hash, plus a cache of the tree, in the extension storage. A sync reconciles the browser tree and the file against that mapping:

- An element in the file without a mapping appears in the browser.
- A mapped element whose hash changed is updated in the browser.
- A mapped element missing from the file is removed from the browser.

Two consequences for an external writer:

- A changed `href` counts as removal plus creation, not as an edit. Remove the `<bookmark>` element and append another one.
- Each browser holds one profile per file. A second profile keeps its own mapping and reads the elements of the first as unmapped.

An element appended under the root of the file arrives in the browser on the next sync, and an element removed from the file leaves the browser on the next sync.

## Desktop setup

1. Create a directory for bookmarks on the WebDAV server, for example `/bookmarks/`.
2. Install Floccus in the browser and add an account of type WebDAV.
3. Server URL: the directory URL with a trailing slash. File name: `bookmarks.xbel`. File type: `xbel`. Credentials: HTTP Basic.
4. Select the folder to sync in the profile, the local folder option. The `Phone` folder appears as its single subfolder and is written by the phone.
5. Sync strategy bidirectional, or unidirectional where the desktop only publishes.
6. Repeat for the other desktop browsers against the same file.
7. Leave the passphrase field empty. An encrypted file needs AES-GCM on the phone, which the Termux toolchain does not provide by default.

## Phone setup

Packages: `fzf`, `curl`, `python3`. For opening a URL, one of:

- `termux-open-url <url>`, from `termux-api` plus the Termux:API app;
- `termux-open --chooser <url>`, which shows the app chooser;
- `termux-am start -a android.intent.action.VIEW -d <url>`, without the Termux:API app.

Files to create:

| Path | Role |
|---|---|
| `~/.config/vbcdd/bookmarks/config.toml` | base URL, file name, user, password; mode 600 |
| `~/.cache/vbcdd/bookmarks/bookmarks.xbel` | last fetched file, for offline reads |
| `~/bin/bookmarks-add` | Append a link to the `Phone` folder. |
| `~/bin/bookmarks-pick` | List the bookmarks, then open the choice. |
| `~/bin/termux-url-opener` | The Android share hook, which calls `bookmarks-add`. |
| `~/.shortcuts/Bookmarks` | Launcher for `bookmarks-pick`, exposed by the Termux:Widget plugin. |

Scripts under `~/.shortcuts/` run in a foreground session, which the picker needs. Scripts under `~/.shortcuts/tasks/` run without a terminal.

### Writer steps

`bookmarks-add <url>`:

1. Abort while `<file>.lock` exists and is younger than 15 minutes.
2. Take the lock with a `PUT` of `<file>.lock`.
3. `GET` the file. On 404, start an empty tree.
4. Find the direct child `<folder>` of `<xbel>` whose `<title>` reads exactly `Phone`. Create it with a free `id` above the counter when absent.
5. Append a `<bookmark>` inside that folder, with `href`, a free `id` above the counter, and a `<title>` taken from the page or from the URL.
6. Raise the counter line.
7. `PUT <file>.temp`, then `MOVE` onto `<file>`.
8. `DELETE <file>.lock` in a trap across every exit path.
9. Store the written file in the cache.
10. Report the result with `termux-toast` where available.

Fetch the page title with `curl --max-time 8` and read `<title>`; an empty title on failure is acceptable. Escape `&`, `<` and `>` inside title text.

### Reader steps

`bookmarks-pick`:

1. `GET` the file with a short timeout into the cache. Fall back to the cache on failure.
2. Extract every `<bookmark>` with its title, host, `href` and folder title.
3. Feed `title<TAB>folder<TAB>host<TAB>href` to `fzf --delimiter='\t' --with-nth=1,3 --height 50%`.
4. Open the selection with `termux-open-url`.
5. Where deletion is wanted, add a key binding that removes the element and writes the file through the same lock and write protocol.
6. A key binding that prepends `Phone<TAB>` to the search field limits the list to the folder written by the phone.

## Failure modes

| Symptom | Cause | Action |
|---|---|---|
| Floccus reports a resource-locked error | A lock file exists and is younger than 15 minutes. | Delete `<file>.lock` when no writer runs. |
| Floccus reports a file-unreadable error | The file lost the XML declaration, or a write was truncated. | Restore the file from the cache, or from a copy. |
| Duplicate bookmarks after a sync | The counter line was not raised, or the profile mapping was lost. | Restore a copy of the file, then resync. |
| A link edited on the phone does not reach the desktop | A changed `href` counts as a removal plus a creation. | Remove the element and append another. |
| Links from the phone never appear on the desktop | The `Phone` folder sits below the root of the file, outside the synced tree. | Place it as a direct child of `<xbel>`. |
| Every sync fails on the read | The WebDAV server lacks `PROPFIND`. | Move to a server from the requirements table. |

## Test procedure

1. Copy the file into a scratch WebDAV directory.
2. Point a throwaway Floccus profile at the scratch directory and an empty browser profile.
3. Append a link with the writer when no `Phone` folder exists; confirm both the folder and the link arrive in the browser on the next sync.
4. Append a second link; confirm the writer reuses the folder instead of adding another one.
5. Delete a link in the browser; confirm the element leaves the file.
6. Stop the writer between the `PUT` and the `MOVE`; confirm the file stays intact and the lock is deleted.
7. Parse the counter line after each run and assert the value is at or above the maximum id in the file.

## Adoption order

1. Floccus on one desktop over WebDAV. Confirm both directions between the browser and the file.
2. The other desktop browsers.
3. The phone reader: cache, picker, open.
4. The phone writer: share hook, the `Phone` folder, the lock, the append.
5. Deletion from the picker, where wanted.

## Open decisions

- Deletion from the phone picker.
- Picker order: title, or time of addition.
- Title fetching on the phone or on the desktop.
- Extension version pinning. The contract in this document comes from the `develop` branch of the Floccus repository. Re-check the adapter after an upgrade, and keep the browser extensions on one version across machines.

## Sources

- Floccus WebDAV adapter, file formats, lock, and write protocol: <https://github.com/floccusaddon/floccus/blob/develop/src/lib/adapters/WebDav.ts>
- XBEL tree shape: <https://github.com/floccusaddon/floccus/blob/develop/src/lib/serializers/Xbel.ts>
- Server id assignment: <https://github.com/floccusaddon/floccus/blob/develop/src/lib/adapters/Caching.ts>
- Merge state and mappings: <https://github.com/floccusaddon/floccus/blob/develop/src/lib/strategies/Default.ts>
- Share hook name and path: <https://wiki.termux.com/wiki/Intents_and_Hooks>
- Home-screen shortcuts: <https://github.com/termux/termux-widget>
