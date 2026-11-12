# Docker

The image is `ghcr.io/akamachinecode/exhale`, for `amd64`, `arm64` and `arm/v7`. It contains only the Exhale binary (no shell) and runs as an unprivileged user.

## Start

```sh
docker run -d --name exhale --restart unless-stopped \
  -p 8484:8484 -v exhale-data:/data \
  ghcr.io/akamachinecode/exhale:latest

docker logs exhale
```

The log shows the one-time setup code. Open `http://<server>:8484`, enter it and create your account. In Docker the code is always asked, because every request reaches Exhale through Docker's network.

## Compose

[`deploy/compose.yaml`](../../deploy/compose.yaml) is the same thing as a file:

```yaml
services:
  exhale:
    image: ghcr.io/akamachinecode/exhale:latest
    restart: unless-stopped
    ports:
      - "8484:8484"
    volumes:
      - exhale-data:/data
volumes:
  exhale-data:
```

```sh
docker compose up -d
docker compose logs exhale
```

## Tags

| Tag | Follows |
|---|---|
| `latest` | The newest release |
| `v0.1.0` | Exactly that version |
| `v0.1`, `v0` | The newest release in that line |

Pin a version tag if you want to choose when to upgrade.

## Data

Everything Exhale stores is in `/data`: the database (`exhale.db`) and its backups (`/data/backups`). Keep it in a volume.

The container runs as user and group `65532`. A named volume, as above, gets the right owner by itself. If you mount a host directory instead, give it to that user first:

```sh
sudo install -d -o 65532 -g 65532 -m 0700 /srv/exhale
# then: -v /srv/exhale:/data
```

## Settings

Set them as environment variables; the full list is in [Install Exhale](README.md#settings). The image already sets `EXHALE_DATA_DIR=/data` and `EXHALE_LISTEN=0.0.0.0:8484`.

```yaml
    environment:
      EXHALE_SECURE_COOKIES: "true"   # only when served over HTTPS
      EXHALE_BACKUP_KEEP: "30"
```

## The port

`-p 8484:8484` makes Exhale reachable from your network over plain HTTP. That is fine at home. **Never forward that port to the internet.** With a reverse proxy on the same machine, publish it to the machine only, `-p 127.0.0.1:8484:8484`, and follow [HTTPS and remote access](https.md).

## Run a command

```sh
docker exec exhale exhale setup-code
docker exec -it exhale exhale user reset-password --email you@example.com
docker exec exhale exhale user reset-2fa --email you@example.com
docker exec exhale exhale backup
```

## Health

Exhale answers `GET /healthz` with `ok`. The image has no shell or `curl`, so it defines no Docker `HEALTHCHECK`; check from outside:

```sh
curl -f http://127.0.0.1:8484/healthz
```

Inside the process, a part that fails (the Telegram connection, for example) is restarted on its own without stopping the rest. `restart: unless-stopped` covers the whole process.

## Backups

Exhale writes one backup a day to `/data/backups` and keeps 14. They are in the same volume as the database, so they do not protect you from losing the disk. Mount another disk and send them there:

```yaml
    volumes:
      - exhale-data:/data
      - /mnt/backups/exhale:/backups     # writable by uid 65532
    environment:
      EXHALE_BACKUP_DIR: /backups
```

To restore one, stop the container and run the restore in a one-off container on the same volume:

```sh
docker compose stop exhale
docker compose run --rm exhale restore --force /data/backups/auto-20261110T093000Z.db
docker compose start exhale
```

More in [Backups](backups.md).

## Upgrade

Owners see new versions in the web (Settings → General → Updates). A container cannot replace itself; run:

```sh
docker compose pull && docker compose up -d
```

With `docker run`: pull the image, remove the container and start it again with the same command. The volume keeps your data.

On the first start of a new version Exhale updates its database by itself and first saves a copy as `/data/backups/pre-migrate-…db`.

## Stop

`docker stop` gives the container 10 seconds; Exhale finishes within 8.
