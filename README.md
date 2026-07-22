# cleanup_autoban

Expires old IPs from a FortiGate address group.

The FortiGate automation stitch stamps each auto-banned address with
`autoban:<epoch_ns>` in its comment. This walks the group and removes
entries older than `-days`, then deletes the address object.

Entries without an `autoban:` comment (manual blocks) are never touched.

## Build

```sh
go build -o expire_autoban .          # Linux/macOS
GOOS=windows GOARCH=amd64 go build -o expire_autoban.exe .   # Windows
```

Always pass `-o` — without it the binary overwrites the source file.

## Run

```sh
# preview, changes nothing
./expire_autoban -host 172.20.20.159 -token $TOKEN -days 7 -dry-run

# do it
./expire_autoban -host 172.20.20.159 -token $TOKEN -days 7
```

## Flags

| Flag | Default | |
|---|---|---|
| `-host` | — | FortiGate IP/hostname (required) |
| `-token` | — | REST API token (required) |
| `-port` | `443` | admin HTTPS port |
| `-group` | `admin-failed-login` | address group to expire |
| `-days` | `7` | age threshold |
| `-dry-run` | `false` | show what would change |
| `-insecure` | `true` | skip TLS verify (self-signed certs) |

Exits `0` on success, `1` on failure. Meant to run daily via cron or Task Scheduler.
