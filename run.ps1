param(
    [string]$Token  = $env:FGT_TOKEN,
    [string]$FgtHost = "10.0.40.1",
    [int]$Port      = 52920,
    [int]$Days      = 20,
    [switch]$Apply
)

if (-not $Token) { throw "No token. Pass -Token <t> or set `$env:FGT_TOKEN" }

$exe  = Join-Path $PSScriptRoot "expire_autoban.exe"
$args = @("-host", $FgtHost, "-port", $Port, "-token", $Token, "-days", $Days)
if (-not $Apply) { $args += "-dry-run" }

& $exe @args
exit $LASTEXITCODE
