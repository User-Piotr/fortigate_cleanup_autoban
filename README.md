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
TOKEN="<fortigate-api-token>"

# preview, changes nothing
go run . \
    -host "$FORTIGATE_HOST" \
    -port 52920 \
    -token "$TOKEN" \
    -days 7 \
    -group admin-failed-login \
    -dry-run

# read the token from standard input instead of passing it as an argument
printf '%s\n' "$TOKEN" | go run . \
    -host "$FORTIGATE_HOST" \
    -port 52920 \
    -token-stdin \
    -days 7 \
    -group admin-failed-login \
    -dry-run
```

```powershell
$FgtHost = "192.0.2.10"
$token = "<fortigate-api-token>"
$env:FGT_TOKEN = $token

# stdin example for Windows PowerShell
$env:FGT_TOKEN | & .\expire_autoban.exe `
    -host $FgtHost `
    -port 52920 `
    -token-stdin `
    -days 7 `
    -group admin-failed-login `
    -dry-run

# wrapper dry-run, using a DPAPI-encrypted token file
.\run.ps1 `
    -FgtHost $FgtHost `
    -port 52920 `
    -Days 7

# Add -Apply only after reviewing the dry-run output.
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
$secureToken = Read-Host -Prompt 'FortiGate API token' -AsSecureString
$encryptedToken = ConvertFrom-SecureString -SecureString $secureToken
Set-Content -LiteralPath '.\fgt-token.dpapi' -Value $encryptedToken -NoNewline
```

Old versions of PowerShell (< 5.1) may not support `-NoNewline`. Use `Out-File -NoNewline` instead.

```powershell
$env:FGT_TOKEN = "<fortigate-api-token>"
$secureToken = ConvertTo-SecureString -String $env:FGT_TOKEN -AsPlainText -Force
$encryptedToken = ConvertFrom-SecureString -SecureString $secureToken
Set-Content -LiteralPath '.\fgt-token.dpapi' -Value $encryptedToken -NoNewline
```

DPAPI user-scoped secrets can be decrypted only by that account on that
computer. Restrict the token-file directory to the task account and
administrators. If the task runs as `SYSTEM`, create this file from a System
PowerShell session; a file created by a normal user cannot be decrypted by
`SYSTEM`. Configure the task to run `powershell.exe` with:

```text
-NoProfile -NonInteractive -File "C:\Path\To\CleanupAutoban\run.ps1" -FgtHost 192.0.2.10 -Port 52920 -Days 7 -Apply
```

Omit `-Apply` for a dry-run. Use a full path to `run.ps1` if the project lives
elsewhere, and run the setup command again if the task account changes.
