# Install the freshly built binary into the install directory (Windows).
#
#   powershell -NoProfile -File scripts/install-local.ps1 -Bin bin\openheron.exe -Dest <prefix>\openheron.exe
#
# ★ This is the **local** install: the binary comes from `task build`. The
#   release installer is scripts/install.ps1, and it is a different job — it
#   downloads, checksums and then verifies what it installed.
#
# ★ The copy is the whole job, and the job is not finished until the copy
#   is. A copy that failed — the destination is held by a running openheron,
#   the disk is full, the directory is not writable — must not be reported
#   as an install: the old binary keeps running while the task says
#   "installed", and that is the one lie an installer must never tell.
#
#   ★ The reason it lied: Copy-Item's failure is a **non-terminating** error,
#     so the next statement — the one that prints "installed" — ran anyway.
#
# Usage:
#   -Bin            the built binary to install
#   -Dest           where it goes (the file, not the directory)
#   -ExpectVersion  the version the installed binary must report; empty
#                   skips the check

param(
	[Parameter(Mandatory = $true)][string]$Bin,
	[Parameter(Mandatory = $true)][string]$Dest,
	[string]$ExpectVersion = ""
)

$ErrorActionPreference = "Stop"

function Note($message) { Write-Host $message }

function Fail($message) {
	Write-Host $message -ForegroundColor Red
	exit 1
}

if (-not (Test-Path $Bin)) { Fail "install failed: the binary to install does not exist: $Bin" }

$dir = Split-Path $Dest
if (-not (Test-Path $dir)) {
	New-Item -ItemType Directory -Path $dir -Force | Out-Null
}

# ★ Copy first, claim later. ★ The copy is where the failure happens, and
#   every failure has to stop here — before anything is claimed.
try {
	Copy-Item -Path $Bin -Destination $Dest -Force -ErrorAction Stop
} catch {
	Write-Host ("install failed: " + $_.Exception.Message) -ForegroundColor Red
	# ★ The common cause is the one the user can act on: the destination is
	#   the binary that is running right now.
	Write-Host "hint: if openheron is running, stop it first (Get-Process openheron | Stop-Process -Force)"
	exit 1
}

# ★ The copy landed — and the file on disk is the one that was built. A
#   destination that is still the old binary would answer with the old
#   version, and "installed" would be a lie of the second order.
if ($ExpectVersion) {
	$got = (& $Dest version 2>$null | Out-String).Trim()
	if ($got -ne $ExpectVersion) {
		Fail ("install failed: the installed binary reports '" + $got + "' but " + $ExpectVersion + " was expected")
	}
}

Note ("installed: " + $Dest)

# ★ The PATH hint is only worth printing when the directory is not already on
#   the **user** PATH: it usually is, and a hint that fires every time is a
#   hint nobody reads. ★ The check is the user PATH, not the current
#   session's — the session may lag behind the machine, and the machine is
#   what the next terminal will read.
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$dir*") {
	Note ("hint: add " + $dir + " to your user PATH if it is not there yet")
}
