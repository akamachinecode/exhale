# Backups

Everything Exhale knows is in one file, the database `exhale.db` in the data directory: subscriptions, charges, accounts, settings, the Telegram bot token, and the logos and documents (contracts, invoices) you attach. Back up that and you have backed up Exhale.

Attached files make the database, and so each backup, bigger: up to 10 MB per document and 500 MB per household. A deleted document is kept for 30 days, so it can be restored with Undo, and is then removed for good ([ADR 0027](../adr/0027-logos-and-documents.md)).

A backup file contains all of that too. Keep backups as private as the data itself.

## What Exhale does by itself

While it runs, Exhale writes **one backup a day** and **keeps the newest 14**.

- The first one is made a minute after start, unless there is already one from the last 24 hours. Restarting never produces extra copies, and a day missed while the machine was off is made up at the next start.
- Files go to `<data dir>/backups`, named `auto-<date and time in UTC>.db`, for example `auto-20261110T093000Z.db`.
- Each file is a complete, standalone database, checked after writing. It appears only when it is finished, so any tool can copy the folder at any time.
- If a backup fails (disk full, folder not writable) the log says `automatic backup failed` and Exhale tries again within the hour. It keeps running.

| Setting | Flag | Default |
|---|---|---|
| `EXHALE_BACKUP_DIR` | `--backup-dir` | `<data dir>/backups` |
| `EXHALE_BACKUP_KEEP` | `--backup-keep` | `14`; `0` keeps all |
| `EXHALE_NO_BACKUPS` | `--no-backups` | off |

Exhale only ever deletes the `auto-` files it made. Manual and pre-upgrade copies are never deleted.

## Put them somewhere else

Backups in the same disk as the database do not survive that disk. Do one of these:

- **Point Exhale at another disk** or a mounted NAS share with `EXHALE_BACKUP_DIR`. On Linux the service must be allowed to write there ([Linux](linux.md#change-a-setting)); in Docker, mount the directory ([Docker](docker.md#backups)).
- **Copy the `backups` folder elsewhere** with what you already use: restic, rsync, Syncthing, your NAS's backup job.

Do not copy `exhale.db` itself while Exhale runs: a copy made in the middle of a write can be damaged. Copy the files in `backups`.

## Make one now

```sh
exhale backup                  # prints the path of the new file
exhale backup --out /mnt/usb   # or write it somewhere else
```

It is safe while the server runs. The file is named `manual-<date and time>.db`. How to run commands on each system: [Linux](linux.md#run-a-command), [Docker](docker.md#run-a-command), [macOS](macos.md#run-a-command).

## Before an upgrade

Nothing to do. When a new version needs to change the database, it first saves a copy as `<data dir>/backups/pre-migrate-v<N>-<date and time>.db`. If the upgrade fails, the error names that file.

## Restore

Restoring replaces the whole database with the backup. Changes made after the backup are lost.

1. **Stop Exhale.**
2. Restore the file:

   ```sh
   exhale restore /path/to/auto-20261110T093000Z.db --force
   ```

3. **Start Exhale.**

What `restore` does:

- It checks that the file is a healthy Exhale database before touching anything.
- Without `--force` it only restores into a data directory that has no database (a new machine, for example).
- With `--force` it replaces the current database, and **keeps it** next to the new one as `exhale.db.replaced-<date and time>`. Delete that file once you are sure.
- It refuses while Exhale is running.

The full commands per system:

```sh
# Linux service
sudo systemctl stop exhale
sudo -u exhale env EXHALE_DATA_DIR=/var/lib/exhale \
  exhale restore /var/lib/exhale/backups/auto-20261110T093000Z.db --force
sudo systemctl start exhale

# Docker Compose
docker compose stop exhale
docker compose run --rm exhale restore --force /data/backups/auto-20261110T093000Z.db
docker compose start exhale
```

## Move to another machine

1. Make a backup on the old machine (`exhale backup`) and copy the file over.
2. Install Exhale on the new machine. Do not start it yet, or stop it.
3. `exhale restore /path/to/manual-….db` (add `--force` if it had already started once).
4. Start it.

Use the same version of Exhale as the one that made the backup, or a newer one. A newer Exhale updates an older database on start.
