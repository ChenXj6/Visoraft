[CmdletBinding()]
param(
    [string]$NodeBin = $env:VISORAFT_NODE_BIN
)

$ErrorActionPreference = 'Stop'
$projectRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$workerRoot = Join-Path $projectRoot 'workers\media'

Push-Location $projectRoot
try {
    if ($NodeBin) {
        if (-not (Test-Path -LiteralPath $NodeBin -PathType Container)) {
            throw "NodeBin does not exist: $NodeBin"
        }
        $env:PATH = $NodeBin + [IO.Path]::PathSeparator + $env:PATH
    }

    $nodeVersionText = (& node --version 2>$null)
    if ($LASTEXITCODE -ne 0 -or -not $nodeVersionText) {
        throw 'Node.js was not found. Install Node.js 24 or pass -NodeBin <directory>.'
    }
    $nodeVersion = [Version]($nodeVersionText.TrimStart('v'))
    if ($nodeVersion.Major -lt 24) {
        throw "Node.js 24+ is required for the checked-in frontend toolchain; found $nodeVersionText."
    }
    if (-not (Get-Command pnpm -ErrorAction SilentlyContinue)) {
        throw 'pnpm 11.9.0 was not found. Install it with: corepack prepare pnpm@11.9.0 --activate'
    }

    $unformatted = @(gofmt -l .)
    if ($LASTEXITCODE -ne 0) { throw 'gofmt failed' }
    if ($unformatted.Count -gt 0) {
        throw "Go files require gofmt: $($unformatted -join ', ')"
    }
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'go test failed' }
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }

    pnpm install --frozen-lockfile
    if ($LASTEXITCODE -ne 0) { throw 'pnpm install failed' }
    pnpm typecheck
    if ($LASTEXITCODE -ne 0) { throw 'React typecheck failed' }
    pnpm build
    if ($LASTEXITCODE -ne 0) { throw 'React build failed' }

    python -m compileall -q workers/media/src workers/media/tests
    if ($LASTEXITCODE -ne 0) { throw 'Python compileall failed' }

    docker build --tag visoraft-media-worker:check $workerRoot
    if ($LASTEXITCODE -ne 0) { throw 'Python Worker image build failed' }
    # Windows PowerShell 5 wraps native stderr lines in ErrorRecord objects.
    # FFmpeg writes normal version/build information to stderr, so this block
    # must use explicit process exit codes instead of ErrorActionPreference.
    $strictErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $ffmpegBuildConf = @(
            docker run --rm --entrypoint ffmpeg visoraft-media-worker:check `
                -hide_banner -buildconf 2>&1
        )
        if ($LASTEXITCODE -ne 0) { throw 'FFmpeg build configuration check failed' }
        $ffmpegBuildText = $ffmpegBuildConf -join "`n"
        foreach ($forbiddenFlag in @('--enable-gpl', '--enable-nonfree', '--enable-version3')) {
            if ($ffmpegBuildText.Contains($forbiddenFlag)) {
                throw "FFmpeg contains forbidden license flag: $forbiddenFlag"
            }
        }
        foreach ($requiredFlag in @('--disable-autodetect', '--disable-network', '--enable-shared')) {
            if (-not $ffmpegBuildText.Contains($requiredFlag)) {
                throw "FFmpeg is missing required build flag: $requiredFlag"
            }
        }
        $ffmpegLicense = @(
            docker run --rm --entrypoint ffmpeg visoraft-media-worker:check -L 2>&1
        ) -join "`n"
        if (
            $LASTEXITCODE -ne 0 `
            -or -not $ffmpegLicense.Contains('GNU Lesser General Public') `
            -or -not $ffmpegLicense.Contains('version 2.1 of the License')
        ) {
            throw 'FFmpeg LGPL license check failed'
        }
        docker run --rm --entrypoint ffprobe visoraft-media-worker:check `
            -hide_banner -version
        if ($LASTEXITCODE -ne 0) { throw 'ffprobe runtime check failed' }
        docker run --rm --entrypoint deno visoraft-media-worker:check --version
        if ($LASTEXITCODE -ne 0) { throw 'Deno runtime check failed' }
        docker run --rm --entrypoint python visoraft-media-worker:check `
            -c "import importlib.metadata as m; assert m.version('yt-dlp-ejs') == '0.8.0'"
        if ($LASTEXITCODE -ne 0) { throw 'yt-dlp-ejs runtime check failed' }
        docker run --rm --entrypoint sh visoraft-media-worker:check `
            -c 'test -r /usr/share/ffmpeg-compliance/source/ffmpeg-8.1.2.tar.xz && test -r /usr/share/ffmpeg-compliance/build/provenance.txt'
        if ($LASTEXITCODE -ne 0) { throw 'FFmpeg source/compliance bundle is missing' }
        docker run --rm --entrypoint python `
            --volume "${workerRoot}:/workspace:ro" `
            --env PYTHONPATH=/workspace/src `
            visoraft-media-worker:check `
            -m unittest discover -s /workspace/tests -v
        if ($LASTEXITCODE -ne 0) { throw 'Python Worker unit tests failed' }
    }
    finally {
        $ErrorActionPreference = $strictErrorActionPreference
    }

    docker compose -f (Join-Path $projectRoot 'compose.yaml') config --quiet
    if ($LASTEXITCODE -ne 0) { throw 'docker compose config failed' }

    Write-Host 'All local checks passed.' -ForegroundColor Green
}
finally {
    Pop-Location
}
