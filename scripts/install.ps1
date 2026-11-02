# Install the openheron binary that GitHub published as a release (Windows).
#
#   irm https://github.com/ouranoshong/openheron/releases/latest/download/install.ps1 | iex
#
# ★ The verification is not optional: a download that does not match the
#   release's checksums.txt is not installed.
#
# Environment:
#   OPENHERON_VERSION   the tag to install (default: the latest release)
#   OPENHERON_REPO      owner/repo to install from (default: ouranoshong/openheron)
#   OPENHERON_BASE_URL  the GitHub base URL (default: https://github.com)
#   OPENHERON_TOKEN     a token for private repos or draft releases
#   OPENHERON_ASSET     an archive already on disk: skip the download, verify it
#   OPENHERON_CHECKSUMS the checksums file to verify against (default: next to the asset)
#   INSTALL_DIR          where to install (default: %LOCALAPPDATA%\Programs\openheron)

$ErrorActionPreference = "Stop"

$repo = if ($env:OPENHERON_REPO) { $env:OPENHERON_REPO } else { "ouranoshong/openheron" }
$baseUrl = if ($env:OPENHERON_BASE_URL) { $env:OPENHERON_BASE_URL } else { "https://github.com" }
$token = $env:OPENHERON_TOKEN
$asset = $env:OPENHERON_ASSET
$installDir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\openheron" }

function Note($message) { Write-Host "openheron: $message" }

function Fail($message) {
	Write-Host "openheron: $message" -ForegroundColor Red
	exit 1
}

function Get-Headers {
	$headers = @{}
	if ($token) { $headers["Authorization"] = "Bearer $token" }
	return $headers
}

# ── 1. the platform ──────────────────────────────────────────────────────────
switch ($env:PROCESSOR_ARCHITECTURE) {
	"AMD64" { $arch = "amd64" }
	"ARM64" { $arch = "arm64" }
	default { Fail "unsupported architecture: $($env:PROCESSOR_ARCHITECTURE)" }
}
$os = "windows"

# ── 2. the version ───────────────────────────────────────────────────────────
$version = $env:OPENHERON_VERSION
if (-not $version) {
	Note "resolving the latest release of $repo"
	try {
		$release = Invoke-RestMethod -Uri "$baseUrl/api/repos/$repo/releases/latest" -Headers (Get-Headers)
	} catch {
		Fail "could not read the latest release of ${repo}: $($_.Exception.Message)"
	}
	$version = $release.tag_name
	if (-not $version) { Fail "could not resolve the latest release of $repo — pass OPENHERON_VERSION=<tag> instead" }
}

$name = "openheron_${version}_${os}_${arch}"
$archive = "$name.zip"

# ── 3. the files ─────────────────────────────────────────────────────────────
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("openheron-" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
	$archivePath = Join-Path $tmp $archive
	if ($asset) {
		if (-not (Test-Path $asset)) { Fail "OPENHERON_ASSET points at a file that does not exist: $asset" }
		Copy-Item $asset $archivePath
		$checksums = if ($env:OPENHERON_CHECKSUMS) { $env:OPENHERON_CHECKSUMS } else { Join-Path (Split-Path $asset) "checksums.txt" }
		if (-not (Test-Path $checksums)) { Fail "no checksums file found next to the asset: $checksums" }
		Copy-Item $checksums (Join-Path $tmp "checksums.txt")
	} else {
		Note "downloading $archive (version $version)"
		try {
			Invoke-WebRequest -Uri "$baseUrl/$repo/releases/download/$version/$archive" -Headers (Get-Headers) -OutFile $archivePath
			Invoke-WebRequest -Uri "$baseUrl/$repo/releases/download/$version/checksums.txt" -Headers (Get-Headers) -OutFile (Join-Path $tmp "checksums.txt")
		} catch {
			Fail "could not download $archive — the release page is $baseUrl/$repo/releases/tag/$version"
		}
	}

	# ── 4. verify ────────────────────────────────────────────────────────────
	$expected = $null
	foreach ($line in Get-Content (Join-Path $tmp "checksums.txt")) {
		$parts = $line -split "\s+", 2
		if ($parts.Count -eq 2 -and $parts[1].Trim() -eq $archive) { $expected = $parts[0].Trim() }
	}
	if (-not $expected) { Fail "checksums.txt has no entry for $archive" }
	$actual = (Get-FileHash -Path $archivePath -Algorithm SHA256).Hash.ToLower()
	if ($actual -ne $expected.ToLower()) {
		Fail "checksum mismatch for $archive — expected $expected, got $actual. Nothing was installed."
	}
	Note "checksum ok"

	# ── 5. unpack ─────────────────────────────────────────────────────────────
	Expand-Archive -Path $archivePath -DestinationPath $tmp -Force
	$binary = Join-Path $tmp "$name\openheron.exe"
	if (-not (Test-Path $binary)) { Fail "the archive does not contain $name\openheron.exe" }

	# ── 6. install ────────────────────────────────────────────────────────────
	New-Item -ItemType Directory -Path $installDir -Force | Out-Null
	$installed = Join-Path $installDir "openheron.exe"
	Copy-Item $binary $installed -Force

	# ── 7. check ──────────────────────────────────────────────────────────────
	$got = (& $installed version 2>$null | Out-String).Trim()
	if ($got -ne $version) {
		Fail "the installed binary reports '$got' but $version was expected — something is wrong with the archive"
	}

	$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
	if ($userPath -notlike "*$installDir*") {
		Note "hint: $installDir is not on PATH — add it to the user PATH"
	}

	Note "installed $installed (version $version)"
} finally {
	Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
