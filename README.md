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

# apply cleanup and explicitly allow saving the entire running configuration
printf '%s\n' "$TOKEN" | go run . \
    -host "$FORTIGATE_HOST" \
    -port 52920 \
    -token-stdin \
    -days 7 \
    -group admin-failed-login \
    -save-if-needed
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

# apply cleanup and allow configuration saving with the DPAPI wrapper
.\run.ps1 `
    -FgtHost $FgtHost `
    -Port 52920 `
    -Days 7 `
    -Apply `
    -SaveIfNeeded
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
| `-save-if-needed` | `false` | explicitly save the running configuration after successful cleanup when `cfg-save` is `manual` or `revert` |
| `-insecure` | `true` | skip TLS verify (self-signed certs) |

Exits `0` on success, `1` on failure. Meant to run daily via cron or Task Scheduler.

## Configuration saving

Configuration saving is optional. Without `-save-if-needed`, apply runs do
not read `cfg-save` or request an explicit configuration save. The PowerShell
wrapper enables this flag only when `-SaveIfNeeded` is supplied.

Dry runs always read `cfg-save` using `GET /api/v2/cmdb/system/global` and
display the device mode immediately below `mode: DRY RUN` in the summary.
If the mode cannot be read, a warning is shown and the summary reports
`cfg-save: unknown`; the cleanup preview still completes.
This read checks neither permission to save nor configuration persistence.

With the flag enabled, the tool reads `cfg-save` through
`GET /api/v2/cmdb/system/global` before changing the group, provided this is
an apply run with expired entries. An unreadable, missing, or unsupported
mode stops the run before modifications. This preflight checks the mode
and access to its read endpoint; it does not verify permission to save.

After the group update and all address deletions succeed, the tool reads
the mode again and acts on its current value:

| Current `cfg-save` | Explicit save action |
|---|---|
| `automatic` | No save request; FortiOS saves configuration changes automatically. |
| `manual` | Save the running configuration to persist the changes. |
| `revert` | Save the running configuration to commit the changes before the revert timeout. |

For `manual` and `revert`, the tool sends `{}` to
`POST /api/v2/monitor/system/config/save?vdom=root`. The save request is
hardcoded to `root`; the tool has no VDOM selection flag. The API token
must permit the additional mode read and save action.

Saving persists the **entire current running configuration**, including
other unsaved changes made before or during cleanup by administrators or
automation. Enable the flag only when committing all such changes is intended.
In `revert` mode, cleanup and saving must finish before the timeout; the
preflight does not postpone it.

Dry runs send only GET requests and never update the group, delete addresses,
or save configuration, even when `-save-if-needed` is supplied. Apply runs
with no expired entries make no configuration-saving API calls.
A failed group update or any failed deletion prevents the save.
If the final mode read or save request fails, the tool prints the cleanup
report and exits `1`. Already applied cleanup changes are not rolled back;
their persistence must be checked on the FortiGate. Successful explicit
saves show `[SAVED]` and the mode in the report.

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
Append `-SaveIfNeeded` to the scheduled task arguments to explicitly allow
saving after successful cleanup. It has no effect without `-Apply`.
