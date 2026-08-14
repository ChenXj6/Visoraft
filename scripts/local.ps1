[CmdletBinding()]
param(
    [ValidateSet('up', 'down', 'status', 'logs', 'reset')]
    [string]$Action = 'up',
    [switch]$Force
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

Push-Location $projectRoot
try {
    switch ($Action) {
        'up' {
            docker compose -f $composeFile up --detach --build --remove-orphans
            if ($LASTEXITCODE -ne 0) { throw 'docker compose up failed' }

            Write-Host ''
            Write-Host 'Waiting for Visoraft:' -ForegroundColor Cyan
            Wait-HttpEndpoint -Uri 'http://localhost:8080/health/ready' -Name 'Go Control API'
            Wait-HttpEndpoint -Uri 'http://localhost:4173/healthz' -Name 'React/Nginx Web'

            Write-Host ''
            Write-Host 'Visoraft is ready:' -ForegroundColor Cyan
            Write-Host '  Web:       http://localhost:4173'
            Write-Host '  API:       http://localhost:8080/health/ready'
            Write-Host '  RabbitMQ:  http://localhost:15673'
            Write-Host '  S3:        http://localhost:8333'
            Write-Host ''
            docker compose -f $composeFile ps
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
    }
}
finally {
    Pop-Location
}
