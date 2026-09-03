<#
.SYNOPSIS
Install the Alpacon CLI on Windows.

.DESCRIPTION
Downloads the release archive that matches this machine's architecture,
verifies its SHA256 checksum, unpacks alpacon.exe into the user's local
application data directory, and puts that directory on the user PATH.
Administrator rights are not needed. Run it again to update.

.PARAMETER Version
Install this exact version instead of the latest release, e.g. 1.10.0.

.PARAMETER Force
Install even when the requested version is already the installed one.

.EXAMPLE
irm https://github.com/alpacax/alpacon-cli/releases/latest/download/install.ps1 | iex

.EXAMPLE
&([scriptblock]::Create((irm https://github.com/alpacax/alpacon-cli/releases/latest/download/install.ps1))) -Version 1.10.0
#>
param(
    [string]$Version,
    [switch]$Force
)

$script:VersionMarkerName = 'installed-version.txt'
# The check behind both refusals answers "held by something", not "held by alpacon".
$script:LockedMessage = 'alpacon.exe cannot be replaced. Close alpacon if it is running. A virus scanner reading the file, or an account not allowed to open it for writing, looks the same from here.'
# A release version, with an optional pre-release or build suffix. The set is
# listed rather than excluded so a version can never steer a URL or a file name
# somewhere else, and \z rather than $ because $ also matches before a trailing
# newline, which a CI wrapper reading a version out of a file would carry in.
$script:VersionPattern = '[0-9]+\.[0-9]+\.[0-9]+[-.0-9A-Za-z+]*'
# A drive letter with a separator, or a UNC prefix. Path.IsPathRooted is not
# this test: it also accepts the drive-relative 'C:foo' and the root-relative
# '\foo', neither of which names one directory on its own. Written as a regex
# rather than a method call so that file scope stays inside what constrained
# language mode allows.
$script:AbsolutePathPattern = '^([A-Za-z]:[\\/]|\\\\)'
# A drive-qualified path. The install directory goes into the permanent user
# PATH, so a UNC or relative entry, which names something else on every other
# machine, is refused. A mapped network drive still gets through.
$script:DriveRootedPattern = '^[A-Za-z]:[\\/]'
$script:ReleaseBaseUrl = 'https://github.com/alpacax/alpacon-cli/releases/download'
$script:LatestReleaseUrl = 'https://github.com/alpacax/alpacon-cli/releases/latest'
function Get-DefaultInstallDir {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$LocalAppData)

    if ($LocalAppData -cnotmatch "$script:DriveRootedPattern") { return $null }

    # Joined as a string, not with Join-Path: a drive-qualified path is a
    # PowerShell drive reference off Windows, and Join-Path fails on it.
    return $LocalAppData.TrimEnd('\') + '\Alpacon\bin'
}

function Get-AlpaconArch {
    param(
        [string]$ProcessorArchitecture = $(
            if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 }
            else { $env:PROCESSOR_ARCHITECTURE }
        )
    )

    # A 32-bit PowerShell on 64-bit Windows reports X86 here and puts the real
    # architecture in PROCESSOR_ARCHITEW6432, which the default above prefers.
    switch ("$ProcessorArchitecture".ToUpperInvariant()) {
        'AMD64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        'ARM'   { return 'arm' }
    }

    throw "Alpacon does not ship a Windows build for processor architecture '$ProcessorArchitecture'."
}

function ConvertFrom-ReleaseLocation {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Location)

    if ($Location -cnotmatch "/releases/tag/[vV]?(?<version>$script:VersionPattern)\z") {
        throw "Could not read a version from the release redirect '$Location'."
    }

    return $Matches['version']
}

function Assert-FullLanguageMode {
    param([string]$LanguageMode = $ExecutionContext.SessionState.LanguageMode)

    if ($LanguageMode -ne 'FullLanguage') {
        throw "PowerShell is in $LanguageMode mode, which blocks the .NET calls this installer needs. Unpack the release zip yourself and put alpacon.exe on your PATH."
    }
}

function Get-LocationHeader {
    param($Headers)

    if ($null -eq $Headers) { return $null }

    # The property branch is for HttpResponseHeaders, which is what PowerShell 7
    # hands back on the exception path and which exposes a typed Uri Location.
    # The indexer branch covers everything else: Windows PowerShell 5.1's header
    # collection, and the plain dictionary both editions return on success.
    $typed = $Headers.PSObject.Properties['Location']
    if ($typed -and $typed.Value) {
        return [string]$typed.Value
    }

    try {
        return [string]($Headers['Location'] | Select-Object -First 1)
    } catch {
        return $null
    }
}

function ConvertFrom-VersionArgument {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Version)

    # The version goes straight into a download URL and an archive file name,
    # and Join-Path does not normalize '..'. A caller that passes an unchecked
    # value through (a CI wrapper, an agent bootstrapping the CLI) must not be
    # able to move the write out of the temporary directory.
    # -cmatch rather than -match: under case-insensitive matching [A-Za-z] also
    # accepts characters that case-fold onto ASCII, such as U+212A KELVIN SIGN,
    # which would then travel into the URL and the archive name.
    if ($Version -cnotmatch "^[vV]?$script:VersionPattern\z") {
        throw "'$Version' is not a version number like 1.10.0."
    }

    return $Version.TrimStart('v', 'V')
}

function Get-LatestAlpaconVersion {
    param([string]$LatestUrl = $script:LatestReleaseUrl)

    # The GitHub API is rate limited to 60 calls an hour per IP, which a shared
    # office network or a CI runner can exhaust. The releases/latest redirect
    # carries the same tag and is not rate limited.
    try {
        $response = Invoke-WebRequest -Uri $LatestUrl -MaximumRedirection 0 -UseBasicParsing
        $location = Get-LocationHeader -Headers $response.Headers
    } catch {
        # Refusing to follow the redirect is an error in both PowerShell
        # editions, but they throw unrelated types: WebException on 5.1 and
        # HttpResponseException on 7. And 5.1 cannot even resolve the name of
        # the other one in a typed catch. So catch everything, and rethrow
        # whatever carries no redirect for us.
        $failure = $_.Exception.PSObject.Properties['Response']
        if (-not $failure -or -not $failure.Value) {
            throw "Could not reach $LatestUrl to find the latest alpacon release. Check this machine can reach github.com, or pass -Version to skip the lookup. ($($_.Exception.Message))"
        }
        $location = Get-LocationHeader -Headers $failure.Value.Headers
        if ([string]::IsNullOrWhiteSpace($location)) { throw }
    }

    if ([string]::IsNullOrWhiteSpace($location)) {
        throw "The latest release URL '$LatestUrl' did not redirect to a tag."
    }

    return ConvertFrom-ReleaseLocation -Location $location
}

function ConvertTo-ResponseText {
    param([Parameter(Mandatory)][AllowNull()]$Content)

    # GitHub serves release assets as application/octet-stream, so
    # Invoke-WebRequest decides the body is not text and hands back a byte[].
    # Casting that to a string spells out the byte values separated by spaces
    # rather than decoding them.
    if ($Content -is [byte[]]) {
        return [System.Text.Encoding]::UTF8.GetString($Content).TrimStart([char]0xFEFF)
    }

    return ([string]$Content).TrimStart([char]0xFEFF)
}

function Get-ExpectedChecksum {
    param(
        [Parameter(Mandatory)][AllowEmptyString()][string]$ChecksumText,
        [Parameter(Mandatory)][string]$FileName
    )

    # GoReleaser writes the two-column GNU form, so that is all this reads. A
    # BSD-style 'SHA256 (file) = hash' line would not be found here.
    foreach ($line in ($ChecksumText -split '\r?\n')) {
        $parts = $line.Trim() -split '\s+', 2
        if (@($parts).Count -eq 2 -and $parts[1] -ceq $FileName) {
            return $parts[0].ToLowerInvariant()
        }
    }

    throw "No checksum for '$FileName' in the checksum file."
}

function Assert-Checksum {
    param(
        [Parameter(Mandatory)][string]$ActualHash,
        [Parameter(Mandatory)][string]$ExpectedHash,
        [Parameter(Mandatory)][string]$FileName
    )

    if ($ActualHash -ne $ExpectedHash) {
        throw "Checksum mismatch for $FileName. Expected $ExpectedHash but got $ActualHash."
    }
}

function Get-InstalledAlpaconVersion {
    param([Parameter(Mandatory)][string]$InstallDir)

    # The version is read from a marker file rather than from `alpacon version`,
    # which would make a network call to check for updates on every install.
    # A marker without the binary beside it means nothing is installed. An
    # antivirus quarantine or a manual delete leaves exactly that behind, and
    # trusting the marker alone would make the next run skip the repair.
    if (-not (Test-Path -LiteralPath (Join-Path $InstallDir 'alpacon.exe'))) { return $null }

    $marker = Join-Path $InstallDir $script:VersionMarkerName
    if (-not (Test-Path -LiteralPath $marker)) { return $null }

    $version = Get-Content -LiteralPath $marker -Raw -ErrorAction SilentlyContinue
    if ([string]::IsNullOrWhiteSpace($version)) { return $null }

    return $version.Trim()
}

function Set-InstalledAlpaconVersion {
    param(
        [Parameter(Mandatory)][string]$InstallDir,
        [Parameter(Mandatory)][string]$Version
    )

    Set-Content -LiteralPath (Join-Path $InstallDir $script:VersionMarkerName) -Value $Version -NoNewline
}

function Test-AlpaconFileLocked {
    param([Parameter(Mandatory)][string]$Path)

    if (-not (Test-Path -LiteralPath $Path)) { return $false }

    # A read-only file cannot be opened for writing either, but Move-Item -Force
    # replaces one, so it is not what this asks about. Ask for read access there
    # and let the share mode do the work: a running exe is held with a share
    # mode that refuses this open, a merely read-only file is not.
    try {
        $access = if ((Get-Item -LiteralPath $Path -Force).IsReadOnly) { 'Read' } else { 'ReadWrite' }
        $stream = [System.IO.File]::Open($Path, 'Open', $access, 'None')
        $stream.Close()
        return $false
    } catch {
        # Deliberately strict: FileShare.None conflicts with any handle at all,
        # so an antivirus scan in flight also reads as held. Refusing with a
        # clear message beats a half-finished install, and the caller says to
        # try again.
        return $true
    }
}

function Resolve-InstallAction {
    param(
        [Parameter()][AllowNull()][AllowEmptyString()][string]$InstalledVersion,
        [Parameter(Mandatory)][string]$TargetVersion,
        [switch]$Force
    )

    if ($Force) { return 'install' }
    if ([string]::IsNullOrWhiteSpace($InstalledVersion)) { return 'install' }
    if ($InstalledVersion -ceq $TargetVersion) { return 'skip' }
    return 'upgrade'
}

function ConvertTo-ComparablePath {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Path)

    # PATH entries are written by hand as often as by installers: quoted, with a
    # trailing separator, or still holding %LOCALAPPDATA%. Each of those names
    # the same directory as the plain absolute path this script appends, and
    # the registry is read unexpanded, so the tokens do reach here.
    $trimmed = [Environment]::ExpandEnvironmentVariables($Path.Trim().Trim('"')).TrimEnd('\')
    if ($trimmed -cmatch "$script:AbsolutePathPattern") {
        # Folds '..' segments. Only for absolute paths: GetFullPath would resolve
        # anything else against this process's working directory, which has
        # nothing to do with where that entry points for anyone else.
        try { return [System.IO.Path]::GetFullPath($trimmed).TrimEnd('\') } catch { return $trimmed }
    }

    return $trimmed
}

function Add-PathEntry {
    param(
        [Parameter(Mandatory)][AllowEmptyString()][string]$CurrentPath,
        [Parameter(Mandatory)][string]$Directory
    )

    $target = ConvertTo-ComparablePath -Path $Directory
    foreach ($entry in ($CurrentPath -split ';')) {
        if ((ConvertTo-ComparablePath -Path $entry) -eq $target) { return $null }
    }

    # Append rather than rebuild, so the entries a user has keep their spelling.
    # Trailing separators go, since an empty PATH entry means the current
    # directory on Windows and nothing should inherit that from an installer.
    if ($CurrentPath.Trim() -eq '') { return $Directory }
    return ($CurrentPath.TrimEnd(';') + ';' + $Directory)
}

function Add-ToUserPath {
    param(
        [Parameter(Mandatory)][string]$Directory,
        [string]$SubKey = 'Environment'
    )

    # [Environment]::SetEnvironmentVariable rewrites the user PATH as REG_SZ,
    # freezing entries such as %USERPROFILE%\tools into expanded strings and
    # breaking them when the profile moves. Go through the registry directly so
    # the value kind survives.
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey($SubKey)
    if ($null -eq $key) {
        # CreateSubKey answers null rather than throwing when it cannot open the
        # key. Say that here; the finally below would otherwise fail on null and
        # report a method call instead of the cause.
        throw "Could not open HKCU\$SubKey for writing."
    }

    try {
        $current = $key.GetValue('Path', $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $kind = if ($null -eq $current) {
            [Microsoft.Win32.RegistryValueKind]::ExpandString
        } else {
            $key.GetValueKind('Path')
        }
        if ($kind -ne [Microsoft.Win32.RegistryValueKind]::String -and
            $kind -ne [Microsoft.Win32.RegistryValueKind]::ExpandString) {
            # Casting a REG_MULTI_SZ or REG_BINARY Path to a string and writing
            # it back would destroy the user's PATH.
            throw "Your user PATH is stored as $kind, which this installer will not rewrite."
        }

        $updated = Add-PathEntry -CurrentPath ([string]$current) -Directory $Directory
        if ($null -eq $updated) { return $false }

        $key.SetValue('Path', $updated, $kind)
        return $true
    } finally {
        $key.Close()
    }
}

function Send-SettingChange {
    if (-not ('AlpaconNative.Win32' -as [type])) {
        Add-Type -Namespace 'AlpaconNative' -Name 'Win32' -MemberDefinition @'
[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)]
public static extern IntPtr SendMessageTimeout(
    IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam,
    uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);
'@
    }

    $HWND_BROADCAST = [IntPtr]0xffff
    $WM_SETTINGCHANGE = 0x1A
    $SMTO_ABORTIFHUNG = 0x0002
    $result = [UIntPtr]::Zero

    [void][AlpaconNative.Win32]::SendMessageTimeout(
        $HWND_BROADCAST, $WM_SETTINGCHANGE, [UIntPtr]::Zero, 'Environment',
        # Per window, not in total, and the PATH is already written either way.
        $SMTO_ABORTIFHUNG, 1000, [ref]$result)
}

function Invoke-AlpaconInstall {
    param(
        [string]$Version,
        [switch]$Force
    )

    # irm | iex runs this in the caller's own shell. Set inside the function
    # these stay in this scope and its children; at file scope they would
    # outlive the install and leave the user's shell in strict mode.
    # Constrained language mode blocks the .NET calls below long before the
    # first one fails, so name the cause once instead of surfacing it as an
    # unrecognisable method error.
    Assert-FullLanguageMode

    Set-StrictMode -Version 3.0
    $ErrorActionPreference = 'Stop'
    # Windows PowerShell 5.1 draws a progress bar per chunk, which on its own
    # turns a few megabytes into a long wait.
    $ProgressPreference = 'SilentlyContinue'

    if (-not $env:LOCALAPPDATA) {
        throw 'This installer runs on Windows only.'
    }
    $InstallDir = Get-DefaultInstallDir -LocalAppData "$env:LOCALAPPDATA"
    if (-not $InstallDir) {
        throw "LOCALAPPDATA is '$env:LOCALAPPDATA', which does not start with a drive letter. Unpack the release zip yourself and put alpacon.exe on your PATH."
    }

    # SecurityProtocol is process wide, so it is the one setting that has to be
    # put back by hand. Windows PowerShell 5.1 does not always negotiate TLS 1.2
    # by default, but the SystemDefault member is the zero value: or-ing Tls12
    # onto it does not add 1.2 to the OS defaults, it replaces them and drops
    # TLS 1.3. Compared as a number because that member only exists from .NET
    # Framework 4.7, and StrictMode turns a missing member into an error.
    $priorProtocol = [Net.ServicePointManager]::SecurityProtocol
    if ([int]$priorProtocol -ne 0) {
        [Net.ServicePointManager]::SecurityProtocol = $priorProtocol -bor [Net.SecurityProtocolType]::Tls12
    }

    try {
        $arch = Get-AlpaconArch
        $target = if ($Version) { ConvertFrom-VersionArgument -Version $Version } else { Get-LatestAlpaconVersion }

        $installed = Get-InstalledAlpaconVersion -InstallDir $InstallDir
        $action = Resolve-InstallAction -InstalledVersion $installed -TargetVersion $target -Force:$Force
        if ($action -eq 'skip') {
            Write-Host "alpacon is already at $target. Pass -Force to install it again."
            return
        }

        $exePath = Join-Path $InstallDir 'alpacon.exe'
        if (Test-AlpaconFileLocked -Path $exePath) {
            throw $script:LockedMessage
        }

        $zipName = "alpacon-$target-windows-$arch.zip"
        $sumName = "alpacon-$target-checksums.sha256"
        $work = Join-Path ([System.IO.Path]::GetTempPath()) ('alpacon-install-' + [guid]::NewGuid())
        New-Item -ItemType Directory -Path $work | Out-Null

        try {
            $zipPath = Join-Path $work $zipName
            $zipUrl = "$script:ReleaseBaseUrl/v$target/$zipName"
            $sumUrl = "$script:ReleaseBaseUrl/v$target/$sumName"
            try {
                Invoke-WebRequest -Uri $zipUrl -OutFile $zipPath -UseBasicParsing
            } catch {
                throw "Could not download alpacon $target for $arch from $zipUrl. Check the version exists on the releases page, and that this machine can reach github.com. ($($_.Exception.Message))"
            }
            try {
                $sumResponse = Invoke-WebRequest -Uri $sumUrl -UseBasicParsing
            } catch {
                throw "Could not download the checksums for alpacon $target from $sumUrl. ($($_.Exception.Message))"
            }
            $sumText = ConvertTo-ResponseText -Content $sumResponse.Content

            $expected = Get-ExpectedChecksum -ChecksumText $sumText -FileName $zipName
            $actual = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash
            Assert-Checksum -ActualHash $actual -ExpectedHash $expected -FileName $zipName

            Expand-Archive -LiteralPath $zipPath -DestinationPath $work -Force
            if (-not (Test-Path -LiteralPath $InstallDir)) {
                New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
            }
            try {
                Move-Item -LiteralPath (Join-Path $work 'alpacon.exe') -Destination $exePath -Force
            } catch {
                # The lock check above ran before the download, so alpacon can
                # have been started in the seconds since. Only say so when the
                # file really is held; a full disk, a missing source and a
                # denied write all arrive here too, under unrelated types.
                if (-not (Test-AlpaconFileLocked -Path $exePath)) { throw }
                throw $script:LockedMessage
            }

            # Belt and braces: neither Invoke-WebRequest -OutFile nor
            # Expand-Archive is known to tag what it writes, but a mark of the
            # web here would make Windows warn on every run. The checksum above
            # proves the download arrived intact; it says nothing about the
            # release itself, since the hash and the archive come from the same
            # host.
            Unblock-File -LiteralPath $exePath

            Set-InstalledAlpaconVersion -InstallDir $InstallDir -Version $target
        } finally {
            Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
        }

        try {
            if (Add-ToUserPath -Directory $InstallDir) {
                # Only the registry write decides whether the PATH is set. The
                # broadcast just refreshes windows that are already open, so its
                # failure must not be reported as a PATH that was never written.
                try {
                    Send-SettingChange
                } catch {
                    # Deliberately silent: there is no way to pass -Verbose to a
                    # script run through irm | iex, and the line below already
                    # tells the reader what a missed broadcast costs them.
                    $null = $_
                }
                Write-Host "Added $InstallDir to your PATH. Terminals that are already open need a restart."
            }
        } catch {
            # alpacon.exe is already in place, so a PATH this process was not
            # allowed to write is a warning rather than a failed install. A
            # group policy that locks HKCU\Environment lands here.
            Write-Warning "Could not put $InstallDir on your PATH: $($_.Exception.Message)"
            Write-Warning "Add it yourself, or call alpacon by its full path."
        }

        # irm | iex runs inside the caller's shell, so this makes alpacon usable
        # right away instead of only in the next terminal.
        $sessionPath = Add-PathEntry -CurrentPath "$env:Path" -Directory $InstallDir
        if ($sessionPath) { $env:Path = $sessionPath }

        $shadow = Get-Command alpacon -CommandType Application -ErrorAction SilentlyContinue |
            Select-Object -First 1
        if ($shadow -and (ConvertTo-ComparablePath -Path $shadow.Source) -ne (ConvertTo-ComparablePath -Path $exePath)) {
            Write-Warning "'alpacon' still resolves to $($shadow.Source), which comes earlier on your PATH."
            Write-Warning "Remove that copy, or put $InstallDir ahead of it."
        }

        if ($action -eq 'upgrade') {
            # Not "updated": Resolve-InstallAction only knows the versions
            # differ, so a pinned -Version can be going backwards.
            Write-Host "Replaced alpacon $installed with $target."
        } else {
            Write-Host "Installed alpacon $target to $InstallDir."
        }
    } finally {
        [Net.ServicePointManager]::SecurityProtocol = $priorProtocol
    }
}

# Dot-sourcing loads the functions for the test suite without installing
# anything. Every other invocation runs the install.
if ($MyInvocation.InvocationName -ne '.') {
    Invoke-AlpaconInstall @PSBoundParameters
}
