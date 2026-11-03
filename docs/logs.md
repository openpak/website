# Service logs

Every OpenPak service writes its log to stdout/stderr; podman hands that to the user
journal. `openpak-logd` (`cmd/openpak-logd`) is the host collector that keeps it.

## Where logs are

| What | Where | Kept |
| --- | --- | --- |
| Day files | `~/openpak-logs/<service>/<YYYY-MM-DD>.log` (UTC day; `.log.gz` after 2 days) | 90 days |
| Database | Postgres database `logs`, table `log_lines`, one partition per UTC day (`log_lines_YYYYMMDD`) | 90 days |
| Viewer | `https://openpak.org/admin/logs` (admins only): service, day, level, text search, tail, download | 90 days |
| journald | `journalctl --user -u openpak-<service>` | `SystemMaxUse=6G` (about 3 weeks) |

`<service>` is the quadlet unit name without `.service` (`openpak-nx-baas`). A line is
`2026-11-03T20:55:01.123Z LEVEL message`; the level is read from the text (Go's `log` writes
everything to stderr, so journald's priority is useless).

Why both: the files are the plain-text copy that survives a broken database or website; the
database is what the viewer queries and what the nightly `openpak-backup` already dumps
(it dumps every database), so the logs are backed up with everything else. Nothing is
backed up separately.

## How it works

- `openpak-logd.service` (user unit) runs `journalctl --user -o json -f` and, every 2 s,
  appends the new lines to the day files, saves the journal cursor, and POSTs them
  (NDJSON, `X-Internal-Key`) to the website's `/internal/logs/ingest`. The website owns the
  Postgres side because the host cannot reach Postgres. If the website is down, lines wait in
  memory (up to 50 000) and the day files carry on regardless.
- Skipped: podman's health-check lines and other transient hash-named units, `init.scope`.
- Redaction: passwords, tokens, cookies, `Authorization`, JWTs, PEM blocks and `key=value`
  secrets are masked before a line is written anywhere. This is a safety net; services must
  not log secrets, request bodies, or query strings on auth endpoints in the first place.
- `openpak-logd-prune.timer` runs `openpak-logd prune` daily: gzips day files older than 2
  days, deletes files older than 90 days, and prints the free disk space.
- The website drops partitions older than 90 days (at start and every 6 hours) and creates
  the next days' partitions. Retention is dropping a partition, never a `DELETE`.

## Status box

The status box (`ubuntu@145.241.228.207`, amd64) runs the same collector with no ingest URL:
day files only, under `~/openpak-logs/`, same 90-day retention and gzip, same timer
(`openpak-logd` and `openpak-logd-prune.timer`, user units) and the same journald limits.
Its logs are not in the `/admin/logs` viewer.

## Size

About 300 000 lines (75 MB) a day across all services before request logging was added,
roughly 60 MB a day in Postgres with its index, 8 MB a day gzipped. 90 days is about 5 GB in
Postgres and 1 GB of files. Postgres connections: the logs pool holds at most 3.

## Configuration (host, `~/.config/openpak-logd.env`)

| Variable | Default | Meaning |
| --- | --- | --- |
| `LOGD_DIR` | `~/openpak-logs` | Day files root. |
| `LOGD_INGEST_URL` / `LOGD_INGEST_KEY` | unset | Website ingest and its key (`LOGS_INGEST_KEY`). Unset: files only. |
| `LOGD_DB_PREFIX` | unset | Prefix for service names in the database. |
| `LOGD_KEEP_DAYS` | 90 | Retention for files. |
| `LOGD_GZIP_AFTER_DAYS` | 2 | Gzip day files older than this. |
| `LOGD_BACKFILL` | 48h | First start only: how far back to read the journal. |
| `LOGD_JOURNAL_ARGS` | `--user` | Arguments for `journalctl` (select the units). |

Website: `LOGS_DATABASE_URL` (database `logs`; unset hides `/admin/logs`) and
`LOGS_INGEST_KEY` (unset refuses ingest). The database role must own `logs`.

## What a service should log

One line per request or command: method, path or command, status or result, duration. Never
secrets, passwords, tokens, cookies, certificates, bodies, or query strings on auth endpoints.
