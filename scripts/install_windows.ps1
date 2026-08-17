[CmdletBinding()]
param(
    [string]$Source,
    [switch]$AllowUntestedSource
)

# This is the user-facing, double-click installer. It deliberately delegates
# all replacement logic to patch_windows.py, which accepts only the stable
# per-user Router destination and stops only executables below that directory.
$ErrorActionPreference = 'Stop'

try {
    $repositoryRoot = Split-Path -Parent $PSScriptRoot
    $patcher = Join-Path $PSScriptRoot 'patch_windows.py'
    $asarTool = Join-Path $repositoryRoot 'node_modules\@electron\asar\bin\asar.mjs'

    if (!(Test-Path -LiteralPath $patcher -PathType Leaf)) {
        throw "The Router patcher is missing: $patcher"
    }

    foreach ($tool in @('go.exe', 'node.exe')) {
        if ($null -eq (Get-Command $tool -ErrorAction SilentlyContinue)) {
            throw "Required build tool not found: $tool. Install the documented prerequisite, then double-click this installer again."
        }
    }

    # A source checkout may omit node_modules. Fetch only the checked-in,
    # lockfile-resolved renderer packer; no lifecycle scripts are run.
    if (!(Test-Path -LiteralPath $asarTool -PathType Leaf)) {
        $npm = Get-Command npm.cmd -ErrorAction SilentlyContinue
        if ($null -eq $npm) {
            throw 'npm.cmd is required to prepare the local installer dependencies.'
        }
        Write-Host 'Preparing the local Router installer dependency...'
        Push-Location $repositoryRoot
        try {
            & $npm.Source ci --ignore-scripts
            if ($LASTEXITCODE -ne 0) {
                throw "npm ci exited with code $LASTEXITCODE"
            }
        }
        finally {
            Pop-Location
        }
    }

    $pythonLauncher = Get-Command py.exe -ErrorAction SilentlyContinue
    if ($null -ne $pythonLauncher) {
        $pythonCommand = $pythonLauncher.Source
        $pythonArguments = @('-3', $patcher)
    }
    else {
        $python = Get-Command python.exe -ErrorAction SilentlyContinue
        if ($null -eq $python) {
            throw 'Python 3 is required to run the local Router installer.'
        }
        $pythonCommand = $python.Source
        $pythonArguments = @($patcher)
    }

    # --force is safe here: patch_windows.py builds and validates the staged
    # copy first, then backs up the prior managed Router copy. It never targets
    # the Microsoft Store package and preserves .codex-mux/account state.
    $pythonArguments += @('--force', '--launch')
    if (![string]::IsNullOrWhiteSpace($Source)) {
        $pythonArguments += @('--source', $Source)
    }
    if ($AllowUntestedSource) {
        $pythonArguments += '--allow-untested-source'
    }

    Write-Host 'Installing the independent Codex Subscription Router...'
    & $pythonCommand @pythonArguments
    if ($LASTEXITCODE -ne 0) {
        throw "patch_windows.py exited with code $LASTEXITCODE"
    }

    Write-Host ''
    Write-Host 'Done. A Desktop shortcut named "Codex Subscription Router" has been created or repaired.'
    exit 0
}
catch {
    Write-Error $_
    exit 1
}
