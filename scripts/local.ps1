[CmdletBinding()]
param(
    [ValidateSet('up', 'refresh', 'refresh-all', 'down', 'status', 'logs', 'reset', 'storage')]
    [string]$Action = 'up',
    [switch]$Force,
    [string]$Path
)

$ErrorActionPreference = 'Stop'
$projectRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$composeFile = Join-Path $projectRoot 'compose.yaml'

if (-not (Test-Path -LiteralPath $composeFile)) {
    throw "compose.yaml not found under $projectRoot"
}
if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    throw 'Docker CLI was not found. Install and start Docker Desktop first.'
}

function Wait-HttpEndpoint {
    param(
        [Parameter(Mandatory)]
        [string]$Uri,
        [Parameter(Mandatory)]
        [string]$Name,
        [int]$TimeoutSeconds = 90
    )

    $deadline = [DateTimeOffset]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        try {
            $response = Invoke-WebRequest -Uri $Uri -UseBasicParsing -TimeoutSec 3
            if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 300) {
                Write-Host "  ready: $Name" -ForegroundColor Green
                return
            }
        }
        catch {
            # Services can legitimately refuse connections while containers initialize.
        }
        Start-Sleep -Seconds 2
    } while ([DateTimeOffset]::UtcNow -lt $deadline)

    throw "$Name did not become ready within $TimeoutSeconds seconds: $Uri"
}

function Test-DockerEngineReady {
    & docker info --format '{{.ServerVersion}}' 1>$null 2>$null
    return $LASTEXITCODE -eq 0
}

function Ensure-DockerEngine {
    if (Test-DockerEngineReady) {
        Write-Host 'Docker Linux Engine is ready.' -ForegroundColor Green
        return
    }

    $desktopPath = 'C:\Program Files\Docker\Docker\Docker Desktop.exe'
    if (-not (Test-Path -LiteralPath $desktopPath)) {
        throw 'Docker Desktop was not found. Install or start Docker Desktop before continuing.'
    }

    $desktopProcess = Get-Process -Name 'Docker Desktop' -ErrorAction SilentlyContinue
    if (-not $desktopProcess) {
        Write-Host 'Starting Docker Desktop...' -ForegroundColor Cyan
        Start-Process -FilePath $desktopPath | Out-Null
    }
    else {
        Write-Host 'Docker Desktop is open; waiting for the Linux Engine...' -ForegroundColor Cyan
    }

    $deadline = [DateTimeOffset]::UtcNow.AddMinutes(4)
    do {
        Start-Sleep -Seconds 3
        if (Test-DockerEngineReady) {
            Write-Host 'Docker Linux Engine is ready.' -ForegroundColor Green
            return
        }
    } while ([DateTimeOffset]::UtcNow -lt $deadline)

    throw 'Docker Desktop did not start its Linux Engine within 4 minutes. Open Docker Desktop, switch to Linux containers if needed, wait until it reports Running, and retry.'
}

function Set-StorageEnvironment {
    param([Parameter(Mandatory)][string]$StoragePath)

    $isDrivePath = $StoragePath -match '^[A-Za-z]:[\\/]'
    $isUncPath = $StoragePath -match '^\\\\[^\\]+\\[^\\]+'
    if (-not $isDrivePath -and -not $isUncPath) {
        throw "Storage path must be an absolute local path: $StoragePath"
    }
    $fullPath = [IO.Path]::GetFullPath($StoragePath)
    New-Item -ItemType Directory -Path $fullPath -Force | Out-Null
    $envFile = Join-Path $projectRoot '.env'
    $lines = [Collections.Generic.List[string]]::new()
    if (Test-Path -LiteralPath $envFile) {
        foreach ($line in Get-Content -LiteralPath $envFile) {
            $lines.Add([string]$line)
        }
    }
    $replacement = 'VISORAFT_LIBRARY_HOST_PATH=' + $fullPath
    $found = $false
    for ($index = 0; $index -lt $lines.Count; $index++) {
        if ($lines[$index] -match '^VISORAFT_LIBRARY_HOST_PATH=') {
            $lines[$index] = $replacement
            $found = $true
        }
    }
    if (-not $found) { $lines.Add($replacement) }
    [IO.File]::WriteAllLines($envFile, $lines, [Text.UTF8Encoding]::new($false))
    return $fullPath
}

function Ensure-StorageEnvironment {
    $envFile = Join-Path $projectRoot '.env'
    if (Test-Path -LiteralPath $envFile) {
        $existing = Get-Content -LiteralPath $envFile | Where-Object { $_ -match '^VISORAFT_LIBRARY_HOST_PATH=' }
        if ($existing) { return }
    }
    $defaultPath = Join-Path $projectRoot 'storage\library'
    $null = Set-StorageEnvironment -StoragePath $defaultPath
}

function Assert-LatestLocalContract {
    try {
        $library = Invoke-RestMethod -Uri 'http://localhost:8080/api/v1/files' -TimeoutSec 10
    }
    catch {
        throw 'Visoraft started, but the current files API could not be verified.'
    }
    if ($null -eq $library.settings -or $null -eq $library.collections) {
        throw 'Docker is still serving an outdated Visoraft image. Run .\scripts\local.ps1 refresh.'
    }
    Write-Host '  verified: current local media library contract' -ForegroundColor Green
}

function Show-ReadySummary {
    Write-Host ''
    Write-Host 'Waiting for Visoraft:' -ForegroundColor Cyan
    Wait-HttpEndpoint -Uri 'http://localhost:8080/health/ready' -Name 'Go Control API'
    Wait-HttpEndpoint -Uri 'http://localhost:4173/healthz' -Name 'React/Nginx Web'
    Assert-LatestLocalContract

    Write-Host ''
    Write-Host 'Visoraft is ready:' -ForegroundColor Cyan
    Write-Host '  Web:       http://localhost:4173'
    Write-Host '  API:       http://localhost:8080/health/ready'
    Write-Host '  RabbitMQ:  http://localhost:15673'
    Write-Host '  S3:        http://localhost:8333'
    Write-Host ''
    docker compose -f $composeFile ps
}

Push-Location $projectRoot
try {
    switch ($Action) {
        'up' {
            Ensure-DockerEngine
            Ensure-StorageEnvironment
            docker compose -f $composeFile up --detach --build --remove-orphans
            if ($LASTEXITCODE -ne 0) { throw 'docker compose up failed' }
            Show-ReadySummary
        }
        'refresh' {
            Ensure-DockerEngine
            Ensure-StorageEnvironment
            Write-Host 'Refreshing the Web and Control API images...' -ForegroundColor Cyan
            docker compose -f $composeFile build control-api web
            if ($LASTEXITCODE -ne 0) { throw 'docker compose Web/API rebuild failed' }
            docker compose -f $composeFile up --detach --force-recreate control-api web
            if ($LASTEXITCODE -ne 0) { throw 'docker compose Web/API recreate failed' }
            Show-ReadySummary
        }
        'refresh-all' {
            Ensure-DockerEngine
            Ensure-StorageEnvironment
            Write-Host 'Refreshing all local service images with Docker cache...' -ForegroundColor Cyan
            docker compose -f $composeFile build
            if ($LASTEXITCODE -ne 0) { throw 'docker compose full rebuild failed' }
            docker compose -f $composeFile up --detach --force-recreate --remove-orphans
            if ($LASTEXITCODE -ne 0) { throw 'docker compose full recreate failed' }
            Show-ReadySummary
        }
        'down' {
            docker compose -f $composeFile down --remove-orphans
            if ($LASTEXITCODE -ne 0) { throw 'docker compose down failed' }
        }
        'status' {
            docker compose -f $composeFile ps
        }
        'logs' {
            docker compose -f $composeFile logs --follow --tail 150
        }
        'reset' {
            if (-not $Force) {
                throw 'Reset removes Visoraft local database, queue, and object volumes. Re-run with -Force.'
            }
            $resolvedRoot = (Resolve-Path -LiteralPath $projectRoot).Path
            if (-not $resolvedRoot.EndsWith([IO.Path]::DirectorySeparatorChar + 'visoraft')) {
                throw "Refusing reset outside the exact visoraft project: $resolvedRoot"
            }
            docker compose -f $composeFile down --volumes --remove-orphans
            if ($LASTEXITCODE -ne 0) { throw 'docker compose reset failed' }
            Write-Host 'Visoraft local containers and named volumes were removed.' -ForegroundColor Yellow
        }
        'storage' {
            Ensure-DockerEngine
            $requestedPath = $Path
            if (-not $requestedPath) {
                try {
                    $settings = Invoke-RestMethod -Uri 'http://localhost:8080/api/v1/library/settings' -TimeoutSec 5
                    $requestedPath = $settings.requested_host_path
                }
                catch {
                    throw 'Cannot read the requested path from Visoraft. Pass -Path explicitly.'
                }
            }
            if (-not $requestedPath) {
                throw 'No pending storage path. Set it in Settings or pass -Path D:\Media.'
            }
            $appliedPath = Set-StorageEnvironment -StoragePath $requestedPath
            docker compose -f $composeFile up --detach --build --force-recreate control-api
            if ($LASTEXITCODE -ne 0) { throw 'Recreating control-api with the new storage path failed.' }
            Wait-HttpEndpoint -Uri 'http://localhost:8080/health/ready' -Name 'Go Control API'
            Write-Host "Local media library: $appliedPath" -ForegroundColor Green
        }
    }
}
finally {
    Pop-Location
}
