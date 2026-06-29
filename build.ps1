param(
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet("win", "linux")]
    [string]$Target
)

$ErrorActionPreference = "Stop"

function Invoke-Native {
    param(
        [Parameter(Mandatory = $true)]
        [string]$FilePath,

        [Parameter(Mandatory = $true)]
        [string[]]$Arguments,

        [Parameter(Mandatory = $true)]
        [string]$WorkingDirectory
    )

    Push-Location $WorkingDirectory
    try {
        & $FilePath @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "Command failed with exit code ${LASTEXITCODE}: $FilePath $($Arguments -join ' ')"
        }
    }
    finally {
        Pop-Location
    }
}

$rootDir = $PSScriptRoot
$webDir = Join-Path $rootDir "web"
$defaultFrontendDir = Join-Path $webDir "default"
$classicFrontendDir = Join-Path $webDir "classic"
$versionContent = Get-Content -Raw -Encoding UTF8 (Join-Path $rootDir "VERSION")
if ($null -eq $versionContent) {
    $version = ""
}
else {
    $version = $versionContent.Trim()
}

$oldCgoEnabled = $env:CGO_ENABLED
$oldGoos = $env:GOOS
$oldGoarch = $env:GOARCH
$oldDisableEslintPlugin = $env:DISABLE_ESLINT_PLUGIN
$oldViteReactAppVersion = $env:VITE_REACT_APP_VERSION

try {
    if (-not (Get-Command bun -ErrorAction SilentlyContinue)) {
        throw "bun was not found in PATH. Please install Bun or add it to PATH before building."
    }

    Write-Host "Installing frontend dependencies..."
    Invoke-Native -FilePath "bun" -Arguments @("install", "--frozen-lockfile") -WorkingDirectory $webDir

    $env:VITE_REACT_APP_VERSION = $version

    Write-Host "Building default frontend..."
    $env:DISABLE_ESLINT_PLUGIN = "true"
    Invoke-Native -FilePath "bun" -Arguments @("run", "build") -WorkingDirectory $defaultFrontendDir

    Write-Host "Building classic frontend..."
    $env:DISABLE_ESLINT_PLUGIN = $oldDisableEslintPlugin
    Invoke-Native -FilePath "bun" -Arguments @("run", "build") -WorkingDirectory $classicFrontendDir

    $env:CGO_ENABLED = "0"
    $env:GOARCH = "amd64"

    switch ($Target) {
        "win" {
            $env:GOOS = "windows"
            $output = "new-api.exe"
        }
        "linux" {
            $env:GOOS = "linux"
            $output = "new-api"
        }
    }

    Write-Host "Building $output for $($env:GOOS)/$($env:GOARCH)..."
    go build -ldflags="-s -w" -o $output main.go
    Write-Host "Build completed: $output"
}
finally {
    $env:CGO_ENABLED = $oldCgoEnabled
    $env:GOOS = $oldGoos
    $env:GOARCH = $oldGoarch
    $env:DISABLE_ESLINT_PLUGIN = $oldDisableEslintPlugin
    $env:VITE_REACT_APP_VERSION = $oldViteReactAppVersion
}
