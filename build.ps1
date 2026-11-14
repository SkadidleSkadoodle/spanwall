# Builds Spanwall and packs the Claude Desktop extension.
#
#   .\build.ps1             vet, test, build, pack Spanwall.mcpb and open it (Claude Desktop installs it)
#   .\build.ps1 -NoInstall  same, without opening the .mcpb
#   .\build.ps1 -NoTest     skip vet and tests
#
# The background app runs from bin\spanwall-daemon.exe and the extension from
# bin\spanwall-mcp.exe, so building bin\spanwall.exe never hits a locked file.
# After a rebuild, toggle the Spanwall extension off and on in Claude Desktop
# (Settings > Extensions) to load the new spanwall-mcp.exe.
param([switch]$NoInstall, [switch]$NoTest)
$ErrorActionPreference = "Stop"
$root = $PSScriptRoot
$bin = Join-Path $root "bin"
New-Item -ItemType Directory -Force $bin | Out-Null
Push-Location $root
try {
    if (-not $NoTest) {
        go vet ./...
        if ($LASTEXITCODE) { throw "go vet failed" }
        go test ./...
        if ($LASTEXITCODE) { throw "tests failed" }
    }
    go build -ldflags "-s -w" -o "$bin\spanwall.exe" .\cmd\spanwall
    if ($LASTEXITCODE) { throw "build failed" }
    $version = (& "$bin\spanwall.exe" version).Trim() -replace '-dev$', ''

    # Restart the background app on the new build if it was running.
    $wasRunning = $false
    try { Invoke-RestMethod http://127.0.0.1:47920/api/status -TimeoutSec 2 | Out-Null; $wasRunning = $true } catch {}
    if ($wasRunning) { & "$bin\spanwall.exe" stop }

    try {
        Copy-Item "$bin\spanwall.exe" "$bin\spanwall-mcp.exe" -Force
    } catch {
        Write-Warning "spanwall-mcp.exe is in use. Toggle the Spanwall extension off in Claude Desktop, then run this again."
    }

    # Extension bundle (manifest + the MCP binary). The manifest points at
    # bin\spanwall-mcp.exe in this repo, so a rebuild only needs a toggle.
    $mcpb = (& "$bin\spanwall.exe" pack-extension $bin "$bin\spanwall-mcp.exe").Trim()
    if ($LASTEXITCODE) { throw "packing the extension failed" }
    Write-Host "Built spanwall $version and $mcpb"

    if ($wasRunning) { & "$bin\spanwall.exe" start }
    if (-not $NoInstall) { Start-Process $mcpb }
} finally {
    Pop-Location
}
