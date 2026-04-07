# v100 Windows Installation Script
# Run in PowerShell:irm https://raw.githubusercontent.com/tripledoublev/v100/main/scripts/install.ps1 | iex

$ErrorActionPreference = "Stop"

$INSTALL_DIR = "$env:LOCALAPPDATA\v100"
$BIN_DIR = "$INSTALL_DIR\bin"
$GITHUB_REPO = "tripledoublev/v100"

function Get-LatestVersion {
    $release = Invoke-RestMethod "https://api.github.com/repos/$GITHUB_REPO/releases/latest" -UseBasicParsing
    return $release.tag_name
}

function Get-ReleaseAsset {
    param(
        [string]$Version,
        [string]$Name,
        [string]$Destination
    )

    $url = "https://github.com/$GITHUB_REPO/releases/download/$Version/$Name"
    Invoke-WebRequest -Uri $url -OutFile $Destination -UseBasicParsing
}

function Verify-Checksum {
    param(
        [string]$ChecksumFile,
        [string]$Filename,
        [string]$FilePath
    )

    $line = Get-Content $ChecksumFile | Where-Object { $_ -match [regex]::Escape($Filename) + '$' }
    if (-not $line) {
        throw "checksum entry not found for $Filename"
    }

    $expected = ($line -split '\s+')[0]
    $actual = (Get-FileHash -Algorithm SHA256 $FilePath).Hash.ToLowerInvariant()
    if ($actual -ne $expected.ToLowerInvariant()) {
        throw "checksum mismatch for $Filename"
    }
}

function Install-V100 {
    param([string]$Version)

    Write-Host "Installing v100 $Version..." -ForegroundColor Cyan

    # Create install directory
    if (-not (Test-Path $BIN_DIR)) {
        New-Item -ItemType Directory -Path $BIN_DIR -Force | Out-Null
    }

    # Determine architecture
    $arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
    $filename = "v100-windows-$arch.exe"

    Write-Host "Downloading $filename..." -ForegroundColor Yellow
    $outfile = Join-Path $BIN_DIR "v100.exe"
    $tmpdir = Join-Path $env:TEMP ("v100-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Path $tmpdir -Force | Out-Null
    $assetPath = Join-Path $tmpdir $filename
    $checksumsPath = Join-Path $tmpdir "checksums.txt"

    try {
        Get-ReleaseAsset -Version $Version -Name $filename -Destination $assetPath
        Get-ReleaseAsset -Version $Version -Name "checksums.txt" -Destination $checksumsPath
        Verify-Checksum -ChecksumFile $checksumsPath -Filename $filename -FilePath $assetPath
        Copy-Item $assetPath $outfile -Force
    } catch {
        Write-Host "Download failed: $_" -ForegroundColor Red
        exit 1
    } finally {
        if (Test-Path $tmpdir) {
            Remove-Item $tmpdir -Recurse -Force
        }
    }

    # Add to PATH
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($userPath -notlike "*$BIN_DIR*") {
        [Environment]::SetEnvironmentVariable("Path", "$userPath;$BIN_DIR", "User")
        $env:Path = "$env:Path;$BIN_DIR"
        Write-Host "Added $BIN_DIR to PATH" -ForegroundColor Green
    }

    Write-Host ""
    Write-Host "✅ v100 installed to $outfile" -ForegroundColor Green
    Write-Host ""
    Write-Host "Run 'v100 --help' to get started!" -ForegroundColor Cyan
}

# Main
$version = if ($args[0]) { $args[0] } else { Get-LatestVersion }
Install-V100 -Version $version
