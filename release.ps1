[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^v\d+\.\d+\.\d+([.-][0-9A-Za-z.-]+)?$')]
    [string]$Version,

    [switch]$SkipTests
)

$ErrorActionPreference = 'Stop'

if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
    throw "Git is required but was not found in PATH."
}

$repoRoot = $PSScriptRoot
Set-Location $repoRoot

$statusLines = @(git status --porcelain)
if ($LASTEXITCODE -ne 0) {
    throw "Failed to read git status."
}
if ($statusLines.Count -gt 0) {
    throw "Working tree is not clean. Commit or stash changes before creating a release tag."
}

git fetch --tags origin
if ($LASTEXITCODE -ne 0) {
    throw "Failed to fetch tags from origin."
}

git rev-parse --verify $Version | Out-Null 2>$null
if ($LASTEXITCODE -eq 0) {
    throw "Tag $Version already exists locally."
}

git ls-remote --tags origin "refs/tags/$Version" | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw "Failed to query remote tags from origin."
}
if (git ls-remote --tags origin "refs/tags/$Version") {
    throw "Tag $Version already exists on origin."
}

if (-not $SkipTests) {
    Write-Host "Running tests"
    $originalGoos = $env:GOOS
    $originalGoarch = $env:GOARCH
    $originalCgoEnabled = $env:CGO_ENABLED

    try {
        Remove-Item Env:GOOS -ErrorAction SilentlyContinue
        Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
        Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue

        go test ./...
        if ($LASTEXITCODE -ne 0) {
            throw "Tests failed."
        }
    }
    finally {
        if ($null -ne $originalGoos) {
            $env:GOOS = $originalGoos
        } else {
            Remove-Item Env:GOOS -ErrorAction SilentlyContinue
        }

        if ($null -ne $originalGoarch) {
            $env:GOARCH = $originalGoarch
        } else {
            Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
        }

        if ($null -ne $originalCgoEnabled) {
            $env:CGO_ENABLED = $originalCgoEnabled
        } else {
            Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
        }
    }
}

$headCommit = (git rev-parse HEAD).Trim()
if (-not $headCommit) {
    throw "Failed to resolve HEAD."
}

Write-Host "Creating annotated tag $Version at $headCommit"
git tag -a $Version -m "Release $Version"
if ($LASTEXITCODE -ne 0) {
    throw "Failed to create tag $Version."
}

Write-Host "Pushing tag $Version to origin"
git push origin "refs/tags/$Version"
if ($LASTEXITCODE -ne 0) {
    throw "Failed to push tag $Version to origin."
}

Write-Host "Release workflow triggered by tag push."
