param(
    [string]$FgtHost = "10.0.40.1",
    [int]$Port      = 52920,
    [int]$Days      = 20,
    [string]$WriteTimeout = "2m",
    [string]$TokenFile = (Join-Path $PSScriptRoot "fgt-token.dpapi"),
    [switch]$Apply,
    [Alias("Save")]
    [switch]$SaveIfNeeded
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$exitCode = 1
$bstr = [IntPtr]::Zero
$secureToken = $null
$plainToken = $null

try {
    $exe = Join-Path $PSScriptRoot "expire_autoban.exe"
    if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) {
        throw "Executable not found: $exe"
    }
    if (-not (Test-Path -LiteralPath $TokenFile -PathType Leaf)) {
        throw "DPAPI token file not found: $TokenFile"
    }

    $secureToken = Get-Content -LiteralPath $TokenFile -Raw | ConvertTo-SecureString
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureToken)
    $plainToken = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr)
    if ([string]::IsNullOrWhiteSpace($plainToken)) {
        throw "DPAPI token file decrypted to an empty value."
    }

    $cliArgs = @("-host", $FgtHost, "-port", $Port, "-days", $Days, "-token-stdin", "-write-timeout", $WriteTimeout)
    if (-not $Apply) { $cliArgs += "-dry-run" }
    if ($SaveIfNeeded) { $cliArgs += "-save" }

    $plainToken | & $exe @cliArgs
    $exitCode = $LASTEXITCODE
    if ($null -eq $exitCode) {
        $exitCode = 1
    }
}
catch {
    [Console]::Error.WriteLine("ERROR: " + $_.Exception.Message)
    $exitCode = 1
}
finally {
    if ($bstr -ne [IntPtr]::Zero) {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
    }
    if ($null -ne $secureToken) {
        $secureToken.Dispose()
    }
    $plainToken = $null
}

exit $exitCode
