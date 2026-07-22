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

Native PowerShell build:

```powershell
$env:GOOS = 'windows'; $env:GOARCH = 'amd64'; go build -o expire_autoban.exe .
```

Use `-o` to choose the executable name and location.

## Run

```sh
# Use the FortiGate management IP or hostname for your environment.
FORTIGATE_HOST=192.0.2.10

# preview, changes nothing
./expire_autoban -host "$FORTIGATE_HOST" -token "$TOKEN" -days 7 -dry-run

# do it
./expire_autoban -host "$FORTIGATE_HOST" -token "$TOKEN" -days 7

# read the token from standard input instead of passing it as an argument
printf '%s\n' "$TOKEN" | ./expire_autoban -host "$FORTIGATE_HOST" -token-stdin -days 7 -dry-run
```

## Flags

| Flag | Default | |
|---|---|---|
| `-host` | — | FortiGate IP/hostname (required) |
| `-token` | — | REST API token (required unless `-token-stdin` is used) |
| `-token-stdin` | `false` | read the REST API token from standard input |
| `-port` | `443` | admin HTTPS port |
| `-group` | `admin-failed-login` | address group to expire |
| `-days` | `7` | age threshold; must be finite and greater than or equal to `0` |
| `-dry-run` | `false` | show what would change |
| `-insecure` | `true` | skip TLS verify (self-signed certs) |

Exits `0` on success, `1` on failure. Meant to run daily via cron or Task Scheduler.

## Windows Task Scheduler

`run.ps1` reads a DPAPI-encrypted token file and pipes the decrypted value to
the executable. The token is therefore not placed in the task arguments or in
the executable's command line.

Create the secret file once while signed in as the same dedicated Windows
account that the scheduled task will run as. Run this from the folder that
contains `run.ps1` so the secret file is stored alongside it:

```powershell
Read-Host 'FortiGate API token' -AsSecureString |
    ConvertFrom-SecureString |
    Set-Content -NoNewline '.\fgt-token.dpapi'
```

DPAPI user-scoped secrets can be decrypted only by that account on that
computer. Restrict the token-file directory to the task account and
administrators. Configure the task to run `powershell.exe` with:

```text
-NoProfile -NonInteractive -File "C:\Path\To\CleanupAutoban\run.ps1" -FgtHost 192.0.2.10 -Port 443 -Days 7 -Apply
```

Omit `-Apply` for a dry-run. Use a full path to `run.ps1` if the project lives
elsewhere, and run the setup command again if the task account changes.
