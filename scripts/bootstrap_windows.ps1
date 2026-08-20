<#
Installs the latest published Codex Relay source release from GitHub.

This bootstrap is intentionally small and source-only: it downloads the
release manifest, validates its exact expected asset URL and SHA-256, expands
the reviewed source into a per-user staging directory, then invokes the same
Windows installer shipped in that release. It never targets the Microsoft
Store ChatGPT package directly.

Run after reviewing this file with:
  irm https://github.com/LightHaru/codex-relay/releases/latest/download/install-codex-relay.ps1 | iex
#>

[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$Repository = 'LightHaru/codex-relay'
$ManifestUrl = "https://github.com/$Repository/releases/latest/download/windows-update.json"
$BootstrapRoot = Join-Path $env:LOCALAPPDATA 'Codex Relay Bootstrap'
$MaximumArchiveBytes = 512MB
$MaximumExtractedBytes = 1GB
$MaximumExtractedFiles = 50000

function Fail([string]$Message) {
    throw "Codex Relay bootstrap: $Message"
}

function Assert-Command([string]$Name, [string]$InstallHint) {
    if ($null -eq (Get-Command $Name -ErrorAction SilentlyContinue)) {
        Fail "Required tool '$Name' was not found. $InstallHint"
    }
}

function Assert-Prerequisites {
    Assert-Command 'go.exe' 'Install Go 1.26+ and run this one-line installer again.'
    Assert-Command 'node.exe' 'Install Node.js 22.12+ and run this one-line installer again.'
    Assert-Command 'npm.cmd' 'Install the Node.js LTS package and run this one-line installer again.'
    if (
        $null -eq (Get-Command 'py.exe' -ErrorAction SilentlyContinue) -and
        $null -eq (Get-Command 'python.exe' -ErrorAction SilentlyContinue)
    ) {
        Fail 'Python 3 was not found. Install Python 3 and run this one-line installer again.'
    }
}

function Assert-Manifest([object]$Manifest) {
    if ($null -eq $Manifest) { Fail 'release manifest is empty.' }
    if ($Manifest.schema -ne 1) { Fail 'release manifest has an unsupported schema.' }
    if ([string]$Manifest.product -ne 'codex-subscription-router') {
        Fail 'release manifest belongs to a different product.'
    }
    $version = [string]$Manifest.version
    if ($version -notmatch '^\d+\.\d+\.\d+$') {
        Fail 'release manifest has an invalid version.'
    }
    $expectedSourceUrl = "https://github.com/$Repository/releases/download/v$version/codex-relay-source-$version.zip"
    if ([string]$Manifest.sourceUrl -cne $expectedSourceUrl) {
        Fail 'release manifest does not reference the expected Codex Relay source archive.'
    }
    if ([string]$Manifest.releaseUrl -cne "https://github.com/$Repository/releases/tag/v$version") {
        Fail 'release manifest does not reference the expected Codex Relay release.'
    }
    $hash = ([string]$Manifest.sourceSha256).ToLowerInvariant()
    if ($hash -notmatch '^[0-9a-f]{64}$') {
        Fail 'release manifest has an invalid source SHA-256.'
    }
    return [pscustomobject]@{
        Version = $version
        SourceUrl = $expectedSourceUrl
        SourceSha256 = $hash
    }
}

function Assert-SafeArchive([string]$ArchivePath) {
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [System.IO.Compression.ZipFile]::OpenRead($ArchivePath)
    try {
        $total = [Int64]0
        $count = 0
        foreach ($entry in $archive.Entries) {
            $count += 1
            if ($count -gt $MaximumExtractedFiles) {
                Fail "source archive contains more than $MaximumExtractedFiles files."
            }
            $name = $entry.FullName.Replace('/', '\')
            if (
                [IO.Path]::IsPathRooted($name) -or
                $name -eq '..' -or
                $name.StartsWith('..\', [StringComparison]::Ordinal) -or
                $name.Contains('\..\', [StringComparison]::Ordinal)
            ) {
                Fail "source archive contains an unsafe path: $($entry.FullName)"
            }
            if ($entry.Length -gt ($MaximumExtractedBytes - $total)) {
                Fail 'source archive expands beyond the safety limit.'
            }
            $total += $entry.Length
        }
    }
    finally {
        $archive.Dispose()
    }
}

try {
    if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        Fail 'LOCALAPPDATA is not available for this Windows user.'
    }
    Assert-Prerequisites

    Write-Host 'Checking the latest verified Codex Relay release...'
    $manifestResponse = Invoke-WebRequest -Uri $ManifestUrl -UseBasicParsing -MaximumRedirection 8
    if ([int]$manifestResponse.StatusCode -lt 200 -or [int]$manifestResponse.StatusCode -ge 300) {
        Fail "release manifest returned HTTP $($manifestResponse.StatusCode)."
    }
    if ($manifestResponse.Content.Length -gt 1MB) {
        Fail 'release manifest is too large.'
    }
    $release = Assert-Manifest ($manifestResponse.Content | ConvertFrom-Json -ErrorAction Stop)

    $runId = '{0}-{1}' -f $release.Version, [Guid]::NewGuid().ToString('N')
    $working = Join-Path $BootstrapRoot $runId
    $archivePath = Join-Path $working 'codex-relay-source.zip'
    $sourceDirectory = Join-Path $working 'source'
    New-Item -ItemType Directory -LiteralPath $working -Force | Out-Null

    Write-Host "Downloading Codex Relay v$($release.Version)..."
    Invoke-WebRequest -Uri $release.SourceUrl -OutFile $archivePath -UseBasicParsing -MaximumRedirection 8
    $size = (Get-Item -LiteralPath $archivePath -ErrorAction Stop).Length
    if ($size -le 0 -or $size -gt $MaximumArchiveBytes) {
        Fail 'source archive has an invalid size.'
    }
    $actualHash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actualHash -cne $release.SourceSha256) {
        Fail 'source archive SHA-256 did not match the published manifest.'
    }
    Assert-SafeArchive $archivePath
    Expand-Archive -LiteralPath $archivePath -DestinationPath $sourceDirectory -Force

    $roots = @(
        Get-ChildItem -LiteralPath $sourceDirectory -Directory -ErrorAction Stop |
            Where-Object { Test-Path -LiteralPath (Join-Path $_.FullName 'scripts\install_windows.ps1') -PathType Leaf }
    )
    if ($roots.Count -ne 1) {
        Fail 'source archive does not contain exactly one Windows installer root.'
    }
    $installer = Join-Path $roots[0].FullName 'scripts\install_windows.ps1'
    Write-Host 'Building an independent Codex Relay copy from the verified source...'
    # The installer derives its repository root from this script's location.
    # Do not pass the extracted checkout as `-Source`: that parameter is
    # reserved for an explicit official Store app directory and would make
    # the patcher treat the source checkout as ChatGPT itself.
    & powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $installer
    if ($LASTEXITCODE -ne 0) {
        Fail "the verified Windows installer exited with code $LASTEXITCODE."
    }
    Write-Host ''
    Write-Host 'Codex Relay is ready. Use the Codex Relay shortcut on the Desktop.'
    Write-Host "Verified source retained at: $($roots[0].FullName)"
}
catch {
    Write-Error $_
    exit 1
}
