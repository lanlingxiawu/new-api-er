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
# 前端已拍平到 web/（与上游结构一致），不再有 default / classic 子目录。
$webDir = Join-Path $rootDir "web"
$versionFile = Join-Path $rootDir "VERSION"
$version = ""
if (Test-Path $versionFile) {
    $versionContent = Get-Content -Raw -Encoding UTF8 $versionFile
    if ($null -ne $versionContent) {
        $version = $versionContent.Trim()
    }
}
# VERSION 文件为空时回退到 git describe，使版本号始终反映真实提交状态，
# 无需手动维护 VERSION 文件（对齐 .github/workflows/electron-build.yml 的做法）。
if ([string]::IsNullOrWhiteSpace($version) -and (Get-Command git -ErrorAction SilentlyContinue)) {
    $describe = (& git -C $rootDir describe --tags 2>$null)
    if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($describe)) {
        $version = $describe.Trim()
    }
}
if ([string]::IsNullOrWhiteSpace($version)) {
    $version = "v0.0.0"
}
Write-Host "Building version: $version"

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

    Write-Host "Building frontend..."
    $env:DISABLE_ESLINT_PLUGIN = "true"
    Invoke-Native -FilePath "bun" -Arguments @("run", "build") -WorkingDirectory $webDir
    $env:DISABLE_ESLINT_PLUGIN = $oldDisableEslintPlugin

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
    # 通过 -X 把版本号注入 common.Version，否则后端会一直报告默认的 v0.0.0
    # （前端展示的"当前版本"来自后端 /api/status 的 common.Version）。
    $ldflags = "-s -w -X 'github.com/QuantumNous/new-api/common.Version=$version'"
    # 必须用 "." 编译整个 package main：包里除了 main.go 还有 trusted_proxies.go，
    # 写成 main.go 会报 undefined: configureTrustedProxies。
    go build -ldflags="$ldflags" -o $output .
    Write-Host "Build completed: $output"
}
finally {
    $env:CGO_ENABLED = $oldCgoEnabled
    $env:GOOS = $oldGoos
    $env:GOARCH = $oldGoarch
    $env:DISABLE_ESLINT_PLUGIN = $oldDisableEslintPlugin
    $env:VITE_REACT_APP_VERSION = $oldViteReactAppVersion
}
