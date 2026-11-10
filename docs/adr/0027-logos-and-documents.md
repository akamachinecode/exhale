# 0027. Own logos and documents: stored in the database, served as inert files

- Status: Accepted
- Date: 2026-11-10

## Context — what it has to do

The [feature scan](../research/2026-11-10-feature-scan.md) asks for three things. Logos: "upload your own and a larger bundled set. Never scrape." Documents: "attach a contract or invoice, and let the assistant read the notice period from it." Exhale is one binary, one process and one data folder (ADR 0009, 0020), and backups are SQLite snapshots of one file. Files people upload are also the easiest way to attack a web app: an SVG or HTML file served from Exhale's address runs scripts with the user's session.

## Criteria

- Backups, restore and moving to another machine keep working as they are: one file.
- A member sees only the files of subscriptions they can see. Every change is audited and can be undone.
- Nothing uploaded can ever run as part of Exhale's pages.
- The assistant can read a document without the document being able to act (ADR 0018, safety rule 4).
- The start of the web app does not get heavier for every brand added.

## Options

**Where files live.**

| | Files in `<data>/files` | BLOBs in SQLite |
|---|---|---|
| Backups | Need a new format (archive of DB + folder) and a new restore | Unchanged: `VACUUM INTO` copies them |
| Consistency | A crash can leave a file without a row, or the reverse | One transaction with the row and the audit entry |
| Undo, trash | Extra bookkeeping on disk | A `deleted_at` column |
| Speed | Better for large files | Fine for files of a few MB ([SQLite: 35% faster than the filesystem](https://www.sqlite.org/fasterthanfs.html) for small blobs) |
| Size of backups | Can be incremental | Every snapshot holds every file |

**SVG logos.** Sanitising SVG means an XML parser and an allow-list of elements and attributes that has to keep up with browsers (scripts, event handlers, `<foreignObject>`, external references, CSS). Well-known sanitisers have shipped bypasses. Re-encoding to PNG removes the whole class.

## Decision

**Storage.** One table, `files` (migration 00110), holds logos (`kind = logo`) and documents (`contract`, `invoice`, `other`) as BLOBs, with name, type, size, SHA-256, who added it and when. List queries never read the content. `subscription_id` and `charge_id` have no foreign key on purpose: deleting a subscription sets its files aside (`deleted_at`) so undo brings them back, and a charge that is planned again does not take an invoice with it. A deleted file is purged 30 days after `deleted_at`; the scheduler runs the purge every hour.

**Limits.** Documents: PDF, PNG, JPEG or WebP, recognised by their first bytes (the name and the browser's type are ignored), up to 10 MB each. Logos: PNG, JPEG or WebP up to 2 MB and 40 megapixels. A household keeps up to 500 MB of files, deleted ones of the last 30 days included.

**Logos.** Decoded and drawn again as a PNG of at most 256×256 (`golang.org/x/image` for WebP and resampling). Metadata (EXIF location), trailing data and anything else the file carried is gone. **SVG is refused**, with a message that says why. The web shows, in order: the uploaded logo, the bundled brand mark, the monogram. Removing the logo goes back to the other two.

**Serving.** Every file goes out with the type it was checked to be, `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'; sandbox`, `Cross-Origin-Resource-Policy: same-origin` and `Referrer-Policy: no-referrer`. Documents are always downloads (`Content-Disposition: attachment`), never shown as a page of Exhale. A logo address carries a version (`?v=` + the start of its SHA-256): with a matching version it is cached for a year (`immutable`), otherwise not; a new logo has a new address.

**Access and audit.** Use cases in `internal/app/files.go` check access through the subscription, so a private subscription's files are "not found" for other members. Adding and deleting documents, setting and removing a logo are writes through `Service.write` (audit entry, dry run, events) and can be undone. In the op catalog: `list_documents`, `delete_document`, `remove_logo`. Uploads (`POST /subscriptions/{id}/documents`, `POST /subscriptions/{id}/logo`), downloads and the full export archive are plain HTTP endpoints over the same use cases, because they carry files, not JSON: a tool call with megabytes of base64 helps no agent and would hand file contents to the model.

**Backups and export.** Backups hold the files with no change to the backup code. `export_data` lists each subscription's files (no content). The new **full export** (`GET /export/archive.zip`, Settings → Data) is a ZIP with `exhale-export.json` and every file, each listed in the JSON with its `path` in the archive.

**Reading a contract.** With the assistant on, `POST /assistant/documents/{id}/terms` sends the document to the model with one tool, `report_contract_terms`, and none of the catalog. Every value is checked (notice 1 to 366 days, from days, weeks or months counted as 30 days; a cancel link that is an http(s) address with a host and no credentials; real dates in a sane range; bounded single-line text) and then dry-run through `update_subscription`. Values equal to what Exhale has are left out. The result is a proposal: the web shows each value with the words of the document it came from, previews the ticked ones with `update_subscription` and `dryRun`, and applies them only when the person presses Apply. With the assistant off, the action is not shown and the endpoint answers 409.

**Bundled marks.** The table grows from 42 to 257 brands from simple-icons with name aliases, generated by `web/scripts/brands.mjs` with only the colour and path of each mark, and loaded as a chunk of its own after the first screen. The entry chunk went from 894 kB to 860 kB (281 to 265 kB gzip); the table is 235 kB (102 kB gzip). Microsoft, Adobe, Amazon, Disney and others are not in simple-icons at their owners' request: they show initials, or the logo someone uploads. Nothing is fetched from the internet.

## Consequences — cost we accept

- The database, and each backup, grows with the files. With 14 daily backups a household that keeps 100 MB of documents uses about 1.5 GB for backups.
- A deleted document stays in the database, and in the backups made meanwhile, for 30 days. The space it takes counts against the 500 MB.
- Importing a full export does not bring documents back; moving an instance with its files is done with a backup and restore.
- Logos lose their vector sharpness and any animation; 256 px is enough for a tile.
- The notes of the "Read dates" review are in English, like those of the import review list.

## Revisit when

- Households routinely keep more than a few hundred MB of files: move content to `<data>/files` with content-addressed names, and make backups an archive.
- People ask to read documents inline in Exhale: serve PDFs from a separate origin or render them in a sandboxed viewer.
