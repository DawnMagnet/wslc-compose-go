# Adds `wslc compose ...` to PowerShell by wrapping the real wslc.exe.
# Usage: dot-source this file from your $PROFILE:
#   . "$HOME\wslc-compose\scripts\wslc-compose.ps1"
# Every other wslc subcommand is forwarded unchanged.

$script:RealWslc = (Get-Command wslc.exe -CommandType Application -ErrorAction SilentlyContinue |
    Select-Object -First 1).Source
if (-not $script:RealWslc) { $script:RealWslc = Join-Path $env:ProgramFiles 'WSL\wslc.exe' }

function wslc {
    if ($args.Count -gt 0 -and $args[0] -eq 'compose') {
        $rest = @($args | Select-Object -Skip 1)
        & wslc-compose.exe --wslc $script:RealWslc @rest
    } else {
        & $script:RealWslc @args
    }
}
