<#
.SYNOPSIS
    Installs wslc-compose from GitHub Releases (Windows, PowerShell 5.1+ / 7+).

.DESCRIPTION
    Downloads wslc-compose-windows-<arch>.exe for the requested release, verifies its
    SHA-256 against checksums.txt, installs it as wslc-compose.exe, adds the install
    directory to the user PATH and (unless -NoProfile) dot-sources the bundled
    wslc-compose.profile.ps1 from $PROFILE so that `wslc compose ...` also works in
    PowerShell. The wrapper deliberately does not end in plain 'wslc-compose.ps1':
    PowerShell resolves .ps1 before .exe, which would shadow wslc-compose.exe.
    Re-running upgrades in place; -Uninstall reverses everything.

.EXAMPLE
    irm https://raw.githubusercontent.com/DawnMagnet/wslc-compose-go/main/scripts/install.ps1 | iex

.EXAMPLE
    # With parameters (irm | iex cannot pass them):
    & ([scriptblock]::Create((irm https://raw.githubusercontent.com/DawnMagnet/wslc-compose-go/main/scripts/install.ps1))) -Version v0.1.2 -InstallDir D:\tools\wslc-compose

.NOTES
    For `irm | iex`, the parameters can also be given as environment variables:
    WSLC_COMPOSE_VERSION, WSLC_COMPOSE_INSTALL_DIR, WSLC_COMPOSE_BASE_URL.
#>
[CmdletBinding()]
param(
    # Release tag such as v0.1.2; "latest" (default) picks the newest release.
    [string]$Version = $(if ($env:WSLC_COMPOSE_VERSION) { $env:WSLC_COMPOSE_VERSION } else { 'latest' }),
    # Installation directory (user-writable; added to the user PATH).
    [string]$InstallDir = $(if ($env:WSLC_COMPOSE_INSTALL_DIR) { $env:WSLC_COMPOSE_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\wslc-compose' }),
    # Release download root; override to use a mirror or a fork.
    [string]$BaseUrl = $(if ($env:WSLC_COMPOSE_BASE_URL) { $env:WSLC_COMPOSE_BASE_URL } else { 'https://github.com/DawnMagnet/wslc-compose-go/releases' }),
    # amd64 or arm64; detected automatically.
    [ValidateSet('', 'amd64', 'arm64')][string]$Arch = '',
    # Do not touch the user PATH.
    [switch]$NoPath,
    # Do not add the `wslc compose` wrapper to $PROFILE.
    [switch]$NoProfile,
    # Remove the installation, the PATH entry and the $PROFILE line.
    [switch]$Uninstall
)


# Run in a child scope so StrictMode/preferences do not leak into the caller's
# session when invoked through `irm | iex`.
& {
    Set-StrictMode -Version Latest
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue' # Invoke-WebRequest is very slow with its progress bar on 5.1
    # Windows PowerShell 5.1 may default to TLS 1.0; GitHub requires 1.2+.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $Exe = Join-Path $InstallDir 'wslc-compose.exe'
    $Wrapper = Join-Path $InstallDir 'wslc-compose.profile.ps1'
    # v0.1.0 installed the wrapper as wslc-compose.ps1, which shadowed the exe.
    $LegacyWrapper = Join-Path $InstallDir 'wslc-compose.ps1'
    $ProfileLine = ". `"$Wrapper`" # wslc-compose"

    function Write-Step([string]$Message) { Write-Host "==> $Message" -ForegroundColor Cyan }

    function Get-UserPath { [Environment]::GetEnvironmentVariable('Path', 'User') }

    function Set-UserPathEntry([bool]$Present) {
        $parts = @((Get-UserPath) -split ';' | Where-Object { $_ -and $_.TrimEnd('\') -ne $InstallDir.TrimEnd('\') })
        if ($Present) { $parts = @($InstallDir) + $parts }
        [Environment]::SetEnvironmentVariable('Path', ($parts -join ';'), 'User')
        $session = @($env:Path -split ';' | Where-Object { $_ -and $_.TrimEnd('\') -ne $InstallDir.TrimEnd('\') })
        if ($Present) { $session = @($InstallDir) + $session }
        $env:Path = $session -join ';'
    }

    # Windows PowerShell 5.1 and PowerShell 7 keep separate profiles
    # (Documents\WindowsPowerShell vs Documents\PowerShell); update both so that
    # `wslc compose` works whichever one the user opens.
    function Get-ProfileFiles {
        $docs = Split-Path -Parent (Split-Path -Parent $PROFILE.CurrentUserAllHosts)
        @($PROFILE.CurrentUserAllHosts,
          (Join-Path $docs 'WindowsPowerShell\profile.ps1'),
          (Join-Path $docs 'PowerShell\profile.ps1')) | Select-Object -Unique
    }

    function Set-ProfileLine([bool]$Present) {
        foreach ($file in Get-ProfileFiles) {
            $lines = @()
            if (Test-Path -LiteralPath $file) { $lines = @(Get-Content -LiteralPath $file | Where-Object { $_ -notmatch '# wslc-compose$' }) }
            elseif (-not $Present) { continue }
            if ($Present) { $lines += $ProfileLine }
            New-Item -ItemType Directory -Force -Path (Split-Path -Parent $file) | Out-Null
            Set-Content -LiteralPath $file -Value $lines -Encoding UTF8
        }
    }

    function Get-File([string]$Url, [string]$Dest) {
        for ($i = 1; ; $i++) {
            try { Invoke-WebRequest -Uri $Url -OutFile $Dest -UseBasicParsing; return }
            catch {
                $resp = $_.Exception.PSObject.Properties['Response']
                if ($resp -and $resp.Value -and [int]$resp.Value.StatusCode -eq 404) {
                    throw "not found: $Url (check -Version; releases: $BaseUrl)"
                }
                if ($i -ge 3) { throw "download failed: $Url`n$($_.Exception.Message)" }
                Write-Warning "download failed (attempt $i/3), retrying: $($_.Exception.Message)"
                Start-Sleep -Seconds (2 * $i)
            }
        }
    }

    if ($Uninstall) {
        Write-Step "Uninstalling wslc-compose from $InstallDir"
        if (Test-Path -LiteralPath $InstallDir) { Remove-Item -LiteralPath $InstallDir -Recurse -Force }
        Set-UserPathEntry $false
        Set-ProfileLine $false
        Write-Host 'wslc-compose removed. Open a new terminal to refresh PATH.' -ForegroundColor Green
        return
    }

    if (-not $Arch) {
        $Arch = if ($env:PROCESSOR_ARCHITEW6432 -eq 'ARM64' -or $env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
    }
    $asset = "wslc-compose-windows-$Arch.exe"
    $root = $BaseUrl.TrimEnd('/') + $(if ($Version -eq 'latest') { '/latest/download' } else { "/download/$Version" })

    Write-Step "Downloading $asset ($Version)"
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("wslc-compose-" + [guid]::NewGuid())
    New-Item -ItemType Directory -Force -Path $tmp | Out-Null
    try {
        Get-File "$root/$asset" (Join-Path $tmp $asset)
        Get-File "$root/checksums.txt" (Join-Path $tmp 'checksums.txt')
        Get-File "$root/wslc-compose.profile.ps1" (Join-Path $tmp 'wslc-compose.profile.ps1')

        Write-Step 'Verifying SHA-256'
        $want = (Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { $_ -match "\s\*?$([regex]::Escape($asset))$" } |
            ForEach-Object { ($_ -split '\s+')[0] } | Select-Object -First 1)
        if (-not $want) { throw "no checksum for $asset in checksums.txt" }
        $got = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $tmp $asset)).Hash
        if ($got -ne $want) { throw "checksum mismatch for ${asset}: expected $want, got $got" }

        Write-Step "Installing to $InstallDir"
        New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
        Move-Item -Force -LiteralPath (Join-Path $tmp $asset) -Destination $Exe
        Move-Item -Force -LiteralPath (Join-Path $tmp 'wslc-compose.profile.ps1') -Destination $Wrapper
        Remove-Item -LiteralPath $LegacyWrapper -Force -ErrorAction SilentlyContinue
    } catch {
        if ($_.Exception -is [System.IO.IOException] -and (Test-Path -LiteralPath $Exe)) {
            throw "cannot replace $Exe (is wslc-compose still running?): $($_.Exception.Message)"
        }
        throw
    } finally {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }

    if (-not $NoPath) { Write-Step 'Adding install dir to user PATH'; Set-UserPathEntry $true }
    if (-not $NoProfile) {
        Write-Step "Enabling 'wslc compose' in $((Get-ProfileFiles) -join ', ')"
        Set-ProfileLine $true
        if ((Get-ExecutionPolicy) -in 'Restricted', 'AllSigned') {
            Write-Warning "PowerShell execution policy is '$(Get-ExecutionPolicy)', so `$PROFILE will not load. Run: Set-ExecutionPolicy -Scope CurrentUser RemoteSigned"
        }
    }

    $installed = try { & $Exe version --short 2>$null } catch { "" }
    Write-Host ""
    Write-Host "wslc-compose $installed installed: $Exe" -ForegroundColor Green
    if (-not (Get-Command wslc.exe -ErrorAction SilentlyContinue)) {
        Write-Warning 'wslc.exe not found on PATH. WSL 2.9.3+ (WSL Containers) is required: wsl --update'
    }
    Write-Host 'Open a new terminal, then try:'
    Write-Host '  wslc-compose version        # any shell'
    Write-Host '  wslc compose version        # PowerShell sugar for wslc-compose'
}
