# Adds `wslc compose ...` to PowerShell by wrapping the real wslc.exe.
# install.ps1 dot-sources this file from $PROFILE automatically; to do it by hand:
#   . "$env:LOCALAPPDATA\Programs\wslc-compose\wslc-compose.profile.ps1"
# The file name must not be plain wslc-compose.ps1: PowerShell prefers .ps1 over
# .exe for the same command name, which would shadow wslc-compose.exe.
# Every other wslc subcommand is forwarded unchanged.

$script:RealWslc = (Get-Command wslc.exe -CommandType Application -ErrorAction SilentlyContinue |
    Select-Object -First 1).Source
if (-not $script:RealWslc) { $script:RealWslc = Join-Path $env:ProgramFiles 'WSL\wslc.exe' }

# Prefer the wslc-compose.exe next to this script, then whatever is on PATH.
$script:WslcCompose = Join-Path $PSScriptRoot 'wslc-compose.exe'
if (-not (Test-Path -LiteralPath $script:WslcCompose)) { $script:WslcCompose = 'wslc-compose.exe' }

function wslc {
    if ($args.Count -gt 0 -and $args[0] -eq 'compose') {
        $rest = @($args | Select-Object -Skip 1)
        & $script:WslcCompose --wslc $script:RealWslc @rest
    } else {
        & $script:RealWslc @args
    }
}
