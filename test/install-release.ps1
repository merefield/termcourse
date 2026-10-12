$ErrorActionPreference = "Stop"
Set-StrictMode -Version 2.0

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$installer = Join-Path $repositoryRoot "install-release.ps1"
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ("termcourse-release-installer-test-" + [Guid]::NewGuid().ToString("N"))
$serverRoot = Join-Path $testRoot "server"
$packageDirectory = Join-Path $testRoot "package"
$installDirectory = Join-Path $testRoot "installed"
$releaseTag = "v1.2.3+build.5"
$releaseVersion = "1.2.3+build.5"
$assetName = "termcourse_1.2.3+build.5_windows_amd64.zip"
$releaseDirectory = Join-Path $serverRoot "merefield\termcourse\releases\download\$releaseTag"
$latestDirectory = Join-Path $serverRoot "repos\merefield\termcourse\releases"
$fixtureBinary = Join-Path $packageDirectory "termcourse.exe"
$archivePath = Join-Path $releaseDirectory $assetName
$serverProcess = $null
$savedEnvironment = @{}

function Write-Utf8File { param([string]$Path, [string]$Content) [IO.File]::WriteAllText($Path, $Content, (New-Object Text.UTF8Encoding($false))) }
function Invoke-Installer {
    param([string]$Destination, [string]$RequestedVersion)
    $arguments = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $installer, "-BinDir", $Destination)
    if ($RequestedVersion) { $arguments += @("-Version", $RequestedVersion) }
    $output = & (Get-Process -Id $PID).Path @arguments 2>&1
    return @{ Status = $LASTEXITCODE; Output = ($output | Out-String).Trim() }
}

try {
    New-Item -ItemType Directory -Path $packageDirectory, $releaseDirectory, $latestDirectory -Force | Out-Null
    Push-Location $repositoryRoot
    try { & go build -trimpath -ldflags "-X github.com/merefield/termcourse.buildVersion=$releaseTag" -o $fixtureBinary ./cmd/termcourse }
    finally { Pop-Location }
    if ($LASTEXITCODE -ne 0) { throw "failed to build the Windows installer fixture" }
    Compress-Archive -LiteralPath $fixtureBinary -DestinationPath $archivePath
    $archiveHash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-Utf8File (Join-Path $releaseDirectory "checksums.txt") "$archiveHash  $assetName`n"
    Write-Utf8File (Join-Path $latestDirectory "latest") '{"tag_name":"v1.2.3+build.5"}'

    $python = Get-Command python -ErrorAction SilentlyContinue
    if ($null -eq $python) { $python = Get-Command python3 -ErrorAction Stop }
    $serverScript = Join-Path $testRoot "fixture-server.py"
    $portFile = Join-Path $testRoot "fixture-server.port"
    Write-Utf8File $serverScript @'
import http.server
import os
import sys

os.chdir(sys.argv[1])
server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), http.server.SimpleHTTPRequestHandler)
port_path = sys.argv[2]
with open(port_path + ".tmp", "w", encoding="ascii") as port_file:
    port_file.write(str(server.server_port))
os.replace(port_path + ".tmp", port_path)
server.serve_forever()
'@
    $serverStdout = Join-Path $testRoot "fixture-server.stdout"
    $serverStderr = Join-Path $testRoot "fixture-server.stderr"
    # Start-Process joins ArgumentList with spaces, so quote each path for
    # temporary directories whose names contain spaces.
    $serverArguments = @("-u", "`"$serverScript`"", "`"$serverRoot`"", "`"$portFile`"")
    $serverProcess = Start-Process -FilePath $python.Source -ArgumentList $serverArguments -PassThru -NoNewWindow -RedirectStandardOutput $serverStdout -RedirectStandardError $serverStderr

    function Get-FixtureServerDiagnostics {
        $output = @()
        foreach ($logPath in @($serverStdout, $serverStderr)) {
            if (Test-Path -LiteralPath $logPath) {
                $log = ([string](Get-Content -LiteralPath $logPath -Raw -ErrorAction SilentlyContinue)).Trim()
                if ($log) { $output += $log }
            }
        }
        if ($output.Count -eq 0) { return "No server output was captured." }
        return ($output -join "`n")
    }

    $port = ""
    $baseUrl = ""
    $webRequestArgs = @{}
    if ($PSVersionTable.PSVersion.Major -lt 6) { $webRequestArgs.UseBasicParsing = $true }
    $ready = $false
    $lastReadinessError = "The server has not reported a port."
    # A cold Python process can take longer than five seconds on Windows CI.
    # Wait for a successful HTTP response, with one deadline for all startup.
    $startupTimer = [Diagnostics.Stopwatch]::StartNew()
    while ($startupTimer.Elapsed.TotalSeconds -lt 30) {
        $serverProcess.Refresh()
        if ($serverProcess.HasExited) {
            throw "fixture HTTP server exited with code $($serverProcess.ExitCode) before becoming ready:`n$(Get-FixtureServerDiagnostics)"
        }
        if (Test-Path -LiteralPath $portFile) {
            try {
                $port = ([string](Get-Content -LiteralPath $portFile -Raw)).Trim()
                if ($port -match '^\d+$') {
                    $baseUrl = "http://127.0.0.1:$port"
                    Invoke-WebRequest -Uri "$baseUrl/repos/merefield/termcourse/releases/latest" -TimeoutSec 2 @webRequestArgs | Out-Null
                    $ready = $true
                    break
                }
                $lastReadinessError = "The server reported an invalid port: $port"
            } catch { $lastReadinessError = $_.Exception.Message }
        }
        Start-Sleep -Milliseconds 100
    }
    if (-not $ready) {
        throw "fixture HTTP server did not become ready within 30 seconds: $lastReadinessError`n$(Get-FixtureServerDiagnostics)"
    }

    foreach ($name in @("TERMCOURSE_VERSION", "TERMCOURSE_REPOSITORY", "TERMCOURSE_GITHUB_URL", "TERMCOURSE_GITHUB_API_URL")) { $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name) }
    $env:TERMCOURSE_VERSION = "latest"; $env:TERMCOURSE_REPOSITORY = "merefield/termcourse"; $env:TERMCOURSE_GITHUB_URL = $baseUrl; $env:TERMCOURSE_GITHUB_API_URL = $baseUrl

    $result = Invoke-Installer $installDirectory ""
    if ($result.Status -ne 0) { throw "release installer failed:`n$($result.Output)" }
    if ($result.Output -notmatch 'Verified the release checksum\.') { throw "release installer did not verify the checksum" }
    $installedBinary = Join-Path $installDirectory "termcourse.exe"
    if ((& $installedBinary --version 2>&1 | Out-String).Trim() -ne "termcourse $releaseVersion") { throw "installed binary reported an unexpected version" }

    $explicitResult = Invoke-Installer (Join-Path $testRoot "explicit") $releaseTag
    if ($explicitResult.Status -ne 0) { throw "installer rejected valid build metadata: $($explicitResult.Output)" }
    $invalidResult = Invoke-Installer (Join-Path $testRoot "invalid") "v1.2.3-01"
    if ($invalidResult.Status -eq 0 -or $invalidResult.Output -notmatch 'invalid semantic release tag') { throw "installer accepted an invalid semantic version" }

    $directoryTarget = Join-Path $testRoot "directory-target"
    New-Item -ItemType Directory -Path (Join-Path $directoryTarget "termcourse.exe") -Force | Out-Null
    $directoryResult = Invoke-Installer $directoryTarget $releaseTag
    if ($directoryResult.Status -eq 0 -or $directoryResult.Output -notmatch 'installation target exists and is a directory') { throw "installer accepted a directory target" }
    Write-Host "Windows release installer tests passed."
} finally {
    foreach ($name in $savedEnvironment.Keys) { [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name]) }
    if ($null -ne $serverProcess -and -not $serverProcess.HasExited) { Stop-Process -Id $serverProcess.Id -Force }
    if (Test-Path -LiteralPath $testRoot) { Remove-Item -LiteralPath $testRoot -Recurse -Force }
}
exit 0
