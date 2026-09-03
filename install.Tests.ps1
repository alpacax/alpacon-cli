BeforeAll {
    # install.ps1 keeps StrictMode inside its entry point so that irm | iex
    # cannot leave a user's shell strict. The helpers are still written for it,
    # so the suite turns it on here.
    Set-StrictMode -Version 3.0
    . (Join-Path $PSScriptRoot 'install.ps1')
}

Describe 'Get-DefaultInstallDir' {
    It 'refuses <Given>, which must not reach the permanent user PATH' -TestCases @(
        @{ Given = '' }
        @{ Given = '\\fileserver\profiles\jane\AppData\Local' }
        @{ Given = 'AppData\Local' }
        @{ Given = 'C:AppData' }
        @{ Given = '\AppData' }
        @{ Given = '/home/jane/.local' }
        @{ Given = ([char]0x212A + ':\Users\jane\AppData\Local') }
    ) {
        Get-DefaultInstallDir -LocalAppData $Given | Should -BeNullOrEmpty
    }

    It 'accepts a path on a local drive' {
        Get-DefaultInstallDir -LocalAppData 'C:\Users\jane\AppData\Local' |
            Should -BeExactly 'C:\Users\jane\AppData\Local\Alpacon\bin'
    }

    It 'does not double the separator when the value ends with <Trailing>' -TestCases @(
        @{ Trailing = 'a backslash'; LocalAppData = 'C:\Users\jane\AppData\Local\' }
        @{ Trailing = 'a forward slash'; LocalAppData = 'C:/Users/jane/AppData/Local/' }
    ) {
        Get-DefaultInstallDir -LocalAppData $LocalAppData |
            Should -BeExactly ($LocalAppData.TrimEnd('\', '/') + '\Alpacon\bin')
    }
}

Describe 'Get-AlpaconArch' {
    It 'maps <Reported> to <Expected>' -TestCases @(
        @{ Reported = 'AMD64'; Expected = 'amd64' }
        @{ Reported = 'amd64'; Expected = 'amd64' }
        @{ Reported = 'ARM64'; Expected = 'arm64' }
        @{ Reported = 'ARM';   Expected = 'arm' }
    ) {
        Get-AlpaconArch -ProcessorArchitecture $Reported | Should -Be $Expected
    }

    It 'prefers PROCESSOR_ARCHITEW6432, which is where 64-bit Windows puts the real one' {
        $prior = $env:PROCESSOR_ARCHITEW6432, $env:PROCESSOR_ARCHITECTURE
        try {
            $env:PROCESSOR_ARCHITEW6432 = 'AMD64'
            $env:PROCESSOR_ARCHITECTURE = 'x86'
            Get-AlpaconArch | Should -Be 'amd64'
        } finally {
            $env:PROCESSOR_ARCHITEW6432 = $prior[0]
            $env:PROCESSOR_ARCHITECTURE = $prior[1]
        }
    }

    It 'falls back to PROCESSOR_ARCHITECTURE when there is no wow64 value' {
        $prior = $env:PROCESSOR_ARCHITEW6432, $env:PROCESSOR_ARCHITECTURE
        try {
            $env:PROCESSOR_ARCHITEW6432 = ''
            $env:PROCESSOR_ARCHITECTURE = 'ARM64'
            Get-AlpaconArch | Should -Be 'arm64'
        } finally {
            $env:PROCESSOR_ARCHITEW6432 = $prior[0]
            $env:PROCESSOR_ARCHITECTURE = $prior[1]
        }
    }

    It 'rejects 32-bit x86 because no Windows 386 build is published' {
        { Get-AlpaconArch -ProcessorArchitecture 'X86' } |
            Should -Throw -ExpectedMessage '*does not ship a Windows build*'
    }

    It 'rejects an empty architecture' {
        { Get-AlpaconArch -ProcessorArchitecture '' } |
            Should -Throw -ExpectedMessage '*does not ship a Windows build*'
    }
}

Describe 'ConvertFrom-ReleaseLocation' {
    It 'reads the version out of a tag redirect' {
        ConvertFrom-ReleaseLocation -Location 'https://github.com/alpacax/alpacon-cli/releases/tag/v1.11.2' |
            Should -Be '1.11.2'
    }

    It 'keeps a pre-release suffix' {
        ConvertFrom-ReleaseLocation -Location 'https://github.com/alpacax/alpacon-cli/releases/tag/v1.12.0-rc1' |
            Should -Be '1.12.0-rc1'
    }

    It 'accepts a tag without the v prefix' {
        ConvertFrom-ReleaseLocation -Location 'https://github.com/alpacax/alpacon-cli/releases/tag/1.11.2' |
            Should -Be '1.11.2'
    }

    It 'rejects a tag whose suffix only case-folds onto ASCII' {
        { ConvertFrom-ReleaseLocation -Location ('https://github.com/alpacax/alpacon-cli/releases/tag/v1.11.2' + [char]0x212A) } |
            Should -Throw -ExpectedMessage '*Could not read a version*'
    }

    It 'rejects a location with anything after the tag' {
        { ConvertFrom-ReleaseLocation -Location "https://github.com/alpacax/alpacon-cli/releases/tag/v1.11.2`n" } |
            Should -Throw -ExpectedMessage '*Could not read a version*'
    }

    It 'rejects a location that carries no tag' {
        { ConvertFrom-ReleaseLocation -Location 'https://github.com/alpacax/alpacon-cli/releases' } |
            Should -Throw -ExpectedMessage '*Could not read a version*'
    }

    It 'rejects an empty location' {
        { ConvertFrom-ReleaseLocation -Location '' } |
            Should -Throw -ExpectedMessage '*Could not read a version*'
    }
}

Describe 'Assert-FullLanguageMode' {
    It 'refuses <Mode>, which blocks the .NET calls the install needs' -TestCases @(
        @{ Mode = 'ConstrainedLanguage' }
        @{ Mode = 'RestrictedLanguage' }
        @{ Mode = 'NoLanguage' }
    ) {
        { Assert-FullLanguageMode -LanguageMode $Mode } |
            Should -Throw -ExpectedMessage '*blocks the .NET calls*'
    }

    It 'passes on FullLanguage' {
        { Assert-FullLanguageMode -LanguageMode 'FullLanguage' } | Should -Not -Throw
    }
}

Describe 'Get-ResponseUri' {
    It 'reads the ResponseUri Windows PowerShell 5.1 exposes' {
        $response = [pscustomobject]@{
            BaseResponse = [pscustomobject]@{
                ResponseUri = [uri]'https://github.com/alpacax/alpacon-cli/releases/tag/v1.2.3'
            }
        }
        Get-ResponseUri -Response $response |
            Should -Be 'https://github.com/alpacax/alpacon-cli/releases/tag/v1.2.3'
    }

    It 'reads the request PowerShell 7 hangs the final URL off' {
        $response = [pscustomobject]@{
            BaseResponse = [pscustomobject]@{
                RequestMessage = [pscustomobject]@{
                    RequestUri = [uri]'https://github.com/alpacax/alpacon-cli/releases/tag/v1.2.3'
                }
            }
        }
        Get-ResponseUri -Response $response |
            Should -Be 'https://github.com/alpacax/alpacon-cli/releases/tag/v1.2.3'
    }

    It 'returns nothing when the response names no URL' {
        Get-ResponseUri -Response ([pscustomobject]@{ BaseResponse = [pscustomobject]@{} }) |
            Should -BeNullOrEmpty
    }

    It 'returns nothing when there is no underlying response' {
        Get-ResponseUri -Response ([pscustomobject]@{ BaseResponse = $null }) | Should -BeNullOrEmpty
    }

    It 'returns nothing for a null response' {
        Get-ResponseUri -Response $null | Should -BeNullOrEmpty
    }
}

Describe 'Get-LatestAlpaconVersion' {
    It 'is not written against the rate limited GitHub API anywhere in the file' {
        $source = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'install.ps1') -Raw
        $source | Should -Not -Match 'api\.github\.com'
    }

    It 'reads the tag out of the URL the redirect landed on' {
        Mock -CommandName Invoke-WebRequest -MockWith {
            [pscustomobject]@{
                BaseResponse = [pscustomobject]@{
                    ResponseUri = [uri]'https://github.com/alpacax/alpacon-cli/releases/tag/v2.4.6'
                }
            }
        }

        Get-LatestAlpaconVersion -LatestUrl 'https://example.invalid/latest' | Should -Be '2.4.6'

        # HEAD is what keeps the release page off the wire, so pin it: a mock
        # that ignores its arguments would not notice its loss.
        Should -Invoke -CommandName Invoke-WebRequest -Times 1 -Exactly -ParameterFilter {
            $Uri -eq 'https://example.invalid/latest' -and
            $Method -eq 'Head' -and
            $UseBasicParsing
        }
    }

    It 'explains a response that never left the latest URL' {
        Mock -CommandName Invoke-WebRequest -MockWith {
            [pscustomobject]@{
                BaseResponse = [pscustomobject]@{
                    ResponseUri = [uri]'https://example.invalid/latest'
                }
            }
        }

        { Get-LatestAlpaconVersion -LatestUrl 'https://example.invalid/latest' } |
            Should -Throw -ExpectedMessage '*Could not read a version*'
    }

    It 'explains a response that names no URL at all' {
        Mock -CommandName Invoke-WebRequest -MockWith { [pscustomobject]@{ BaseResponse = $null } }

        { Get-LatestAlpaconVersion -LatestUrl 'https://example.invalid/latest' } |
            Should -Throw -ExpectedMessage '*did not say which release it landed on*'
    }

    It 'names the URL and the transport error when the request fails' {
        Mock -CommandName Invoke-WebRequest -MockWith {
            throw [System.Net.WebException]::new('the host is unreachable')
        }

        # Names the URL as well as carrying the transport error, so a caller
        # behind a proxy learns what could not be reached.
        { Get-LatestAlpaconVersion -LatestUrl 'https://example.invalid/latest' } |
            Should -Throw -ExpectedMessage '*https://example.invalid/latest*unreachable*'
    }
}

Describe 'ConvertTo-ResponseText' {
    It 'decodes the byte array GitHub responses actually carry' {
        $bytes = [System.Text.Encoding]::UTF8.GetBytes("abcd1234  alpacon-1.11.2-windows-amd64.zip`n")
        $text = ConvertTo-ResponseText -Content $bytes

        Get-ExpectedChecksum -ChecksumText $text -FileName 'alpacon-1.11.2-windows-amd64.zip' |
            Should -Be 'abcd1234'
    }

    It 'passes a string body through unchanged' {
        ConvertTo-ResponseText -Content 'already text' | Should -Be 'already text'
    }

    It 'turns a null body into an empty string rather than failing later' {
        # GetType, not BeNullOrEmpty: that helper accepts null too, and null is
        # what this is meant to rule out.
        (ConvertTo-ResponseText -Content $null).GetType().Name | Should -Be 'String'
    }

    It 'strips a BOM, which Trim does not treat as whitespace' {
        # Cast: the two arrays concatenate into Object[], which is not the byte[]
        # Invoke-WebRequest hands back.
        $bytes = [byte[]]([System.Text.Encoding]::UTF8.GetPreamble() +
            [System.Text.Encoding]::UTF8.GetBytes("abcd1234  alpacon-1.11.2-windows-amd64.zip`n"))

        Get-ExpectedChecksum -ChecksumText (ConvertTo-ResponseText -Content $bytes) -FileName 'alpacon-1.11.2-windows-amd64.zip' |
            Should -BeExactly 'abcd1234'
    }
}

Describe 'Get-ExpectedChecksum' {
    BeforeAll {
        $script:ChecksumFile = @(
            'aaaa1111  alpacon-1.11.2-darwin-arm64.tar.gz'
            'BBBB2222  alpacon-1.11.2-windows-amd64.zip'
            'dddd4444  ALPACON-1.11.2-WINDOWS-AMD64.ZIP'
            'eeee5555  alpacon 1.11.2 spaced.zip'
            'cccc3333  alpacon-1.11.2-windows-arm64.zip'
        ) -join "`n"
    }

    It 'finds the line for the requested file' {
        Get-ExpectedChecksum -ChecksumText $script:ChecksumFile -FileName 'alpacon-1.11.2-windows-arm64.zip' |
            Should -Be 'cccc3333'
    }

    It 'returns the hash in lower case' {
        Get-ExpectedChecksum -ChecksumText $script:ChecksumFile -FileName 'alpacon-1.11.2-windows-amd64.zip' |
            Should -BeExactly 'bbbb2222'
    }

    It 'reads a file that uses CRLF line endings' {
        $crlf = $script:ChecksumFile -replace "`n", "`r`n"
        Get-ExpectedChecksum -ChecksumText $crlf -FileName 'alpacon-1.11.2-windows-amd64.zip' |
            Should -BeExactly 'bbbb2222'
    }

    It 'tells two names apart when only their case differs' {
        Get-ExpectedChecksum -ChecksumText $script:ChecksumFile -FileName 'ALPACON-1.11.2-WINDOWS-AMD64.ZIP' |
            Should -BeExactly 'dddd4444'
    }

    It 'keeps a file name that contains spaces whole' {
        Get-ExpectedChecksum -ChecksumText $script:ChecksumFile -FileName 'alpacon 1.11.2 spaced.zip' |
            Should -BeExactly 'eeee5555'
    }

    It 'rejects a file name that is not listed' {
        { Get-ExpectedChecksum -ChecksumText $script:ChecksumFile -FileName 'alpacon-1.11.2-windows-arm.zip' } |
            Should -Throw -ExpectedMessage '*No checksum*'
    }

    It 'rejects an empty checksum file' {
        { Get-ExpectedChecksum -ChecksumText '' -FileName 'alpacon-1.11.2-windows-amd64.zip' } |
            Should -Throw -ExpectedMessage '*No checksum*'
    }
}

Describe 'Assert-Checksum' {
    It 'passes when the hashes match' {
        { Assert-Checksum -ActualHash 'abcd' -ExpectedHash 'abcd' -FileName 'x.zip' } |
            Should -Not -Throw
    }

    It 'compares hashes without regard to case, since Get-FileHash returns uppercase' {
        { Assert-Checksum -ActualHash 'ABCD' -ExpectedHash 'abcd' -FileName 'x.zip' } |
            Should -Not -Throw
    }

    It 'throws and names the file when the hashes differ' {
        { Assert-Checksum -ActualHash 'abcd' -ExpectedHash 'dcba' -FileName 'x.zip' } |
            Should -Throw -ExpectedMessage '*Checksum mismatch for x.zip*'
    }
}

Describe 'Resolve-InstallAction' {
    It 'installs when nothing is on disk' {
        Resolve-InstallAction -InstalledVersion $null -TargetVersion '1.11.2' | Should -Be 'install'
    }

    It 'installs when the version marker is empty' {
        Resolve-InstallAction -InstalledVersion '' -TargetVersion '1.11.2' | Should -Be 'install'
    }

    It 'skips when the installed version already matches' {
        Resolve-InstallAction -InstalledVersion '1.11.2' -TargetVersion '1.11.2' | Should -Be 'skip'
    }

    It 'upgrades when the versions differ only in the case of a pre-release tag' {
        Resolve-InstallAction -InstalledVersion '1.12.0-rc1' -TargetVersion '1.12.0-RC1' |
            Should -Be 'upgrade'
    }

    It 'upgrades when the installed version differs' {
        Resolve-InstallAction -InstalledVersion '1.11.1' -TargetVersion '1.11.2' | Should -Be 'upgrade'
    }

    It 'installs on -Force even when the version already matches' {
        Resolve-InstallAction -InstalledVersion '1.11.2' -TargetVersion '1.11.2' -Force | Should -Be 'install'
    }
}

Describe 'Get-InstalledAlpaconVersion and Set-InstalledAlpaconVersion' {
    BeforeEach {
        $script:Dir = Join-Path ([System.IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
        New-Item -ItemType Directory -Path $script:Dir | Out-Null
        # The reader only trusts a marker that has the binary beside it.
        Set-Content -LiteralPath (Join-Path $script:Dir 'alpacon.exe') -Value 'binary' -NoNewline
    }

    AfterEach {
        Remove-Item -LiteralPath $script:Dir -Recurse -Force -ErrorAction SilentlyContinue
    }

    It 'returns null when the binary is gone but the marker is not' {
        Set-InstalledAlpaconVersion -InstallDir $script:Dir -Version '1.11.2'
        Remove-Item -LiteralPath (Join-Path $script:Dir 'alpacon.exe')

        Get-InstalledAlpaconVersion -InstallDir $script:Dir | Should -BeNullOrEmpty
    }

    It 'returns null when no marker exists' {
        Get-InstalledAlpaconVersion -InstallDir $script:Dir | Should -BeNullOrEmpty
    }

    It 'reads back what it wrote' {
        Set-InstalledAlpaconVersion -InstallDir $script:Dir -Version '1.11.2'
        Get-InstalledAlpaconVersion -InstallDir $script:Dir | Should -BeExactly '1.11.2'
    }

    It 'trims a marker an editor left a newline on' {
        Set-Content -LiteralPath (Join-Path $script:Dir 'installed-version.txt') -Value "1.11.2`r`n" -NoNewline
        Get-InstalledAlpaconVersion -InstallDir $script:Dir | Should -BeExactly '1.11.2'
    }

    It 'returns null for an empty marker file' {
        # Get-Content -Raw answers null here, and without the guard the Trim
        # below it would be a method call on null.
        Set-Content -LiteralPath (Join-Path $script:Dir 'installed-version.txt') -Value '' -NoNewline
        Get-InstalledAlpaconVersion -InstallDir $script:Dir | Should -BeNullOrEmpty
    }

    It 'returns null for a whitespace-only marker' {
        Set-Content -LiteralPath (Join-Path $script:Dir 'installed-version.txt') -Value "   `n"
        # Neither BeNull nor BeNullOrEmpty tells null from '' in Pester, and ''
        # is what Trim would return if the guard were narrowed to IsNullOrEmpty.
        $answer = Get-InstalledAlpaconVersion -InstallDir $script:Dir
        ($null -eq $answer) | Should -BeTrue -Because "a whitespace-only marker names no version, got '$answer'"
    }
}

Describe 'ConvertFrom-VersionArgument' {
    It 'rejects <Given>, which would steer the download somewhere else' -TestCases @(
        @{ Given = '../../evil' }
        @{ Given = '1.10.0/../../evil' }
        @{ Given = '1.10.0\..\evil' }
        @{ Given = 'latest' }
        @{ Given = '' }
        @{ Given = "1.10.0`n" }
        @{ Given = '1.10.0 ' }
        @{ Given = '1.10.0?x' }
        @{ Given = ('1.10.0' + [char]0x212A) }
    ) {
        { ConvertFrom-VersionArgument -Version $Given } |
            Should -Throw -ExpectedMessage '*is not a version number*'
    }

    It 'accepts <Given> and returns <Expected>' -TestCases @(
        @{ Given = '1.10.0';     Expected = '1.10.0' }
        @{ Given = 'v1.10.0';    Expected = '1.10.0' }
        @{ Given = 'V1.10.0';    Expected = '1.10.0' }
        @{ Given = '1.12.0-rc1'; Expected = '1.12.0-rc1' }
    ) {
        ConvertFrom-VersionArgument -Version $Given | Should -Be $Expected
    }
}

Describe 'Test-AlpaconFileLocked' {
    It 'reports a missing file as not locked' {
        Test-AlpaconFileLocked -Path (Join-Path ([System.IO.Path]::GetTempPath()) 'no-such-alpacon.exe') |
            Should -BeFalse
    }

    It 'reports a free file as not locked' {
        $path = Join-Path ([System.IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
        Set-Content -LiteralPath $path -Value 'x'
        try { Test-AlpaconFileLocked -Path $path | Should -BeFalse }
        finally { Remove-Item -LiteralPath $path -Force }
    }

    It 'reports a read-only file as free, since Move-Item -Force replaces one' {
        $path = Join-Path ([System.IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
        Set-Content -LiteralPath $path -Value 'x' -NoNewline
        Set-ItemProperty -LiteralPath $path -Name IsReadOnly -Value $true
        try {
            Test-AlpaconFileLocked -Path $path | Should -BeFalse
        } finally {
            Set-ItemProperty -LiteralPath $path -Name IsReadOnly -Value $false
            Remove-Item -LiteralPath $path -Force
        }
    }

    It 'reports a file the process may not open at all as locked' -Skip:($env:OS -eq 'Windows_NT') {
        # A denying ACL on Windows and mode 000 here both raise
        # UnauthorizedAccessException. Only the second can be set up portably.
        $path = Join-Path ([System.IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
        Set-Content -LiteralPath $path -Value 'x' -NoNewline
        chmod 000 $path
        try {
            Test-AlpaconFileLocked -Path $path | Should -BeTrue
        } finally {
            chmod 600 $path
            Remove-Item -LiteralPath $path -Force
        }
    }

    It 'reports a file held open exclusively as locked' {
        $path = Join-Path ([System.IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
        Set-Content -LiteralPath $path -Value 'x'
        $stream = [System.IO.File]::Open($path, 'Open', 'ReadWrite', 'None')
        try { Test-AlpaconFileLocked -Path $path | Should -BeTrue }
        finally { $stream.Close(); Remove-Item -LiteralPath $path -Force }
    }
}

Describe 'Add-PathEntry' {
    It 'appends to an empty PATH' {
        Add-PathEntry -CurrentPath '' -Directory 'C:\bin' | Should -Be 'C:\bin'
    }

    It 'appends to the end of an existing PATH' {
        Add-PathEntry -CurrentPath 'C:\a;C:\b' -Directory 'C:\bin' | Should -Be 'C:\a;C:\b;C:\bin'
    }

    It 'returns null, not empty, when the directory is already there' {
        # Add-ToUserPath only checks for null, so an empty string here would
        # have it write an empty user PATH.
        $answer = Add-PathEntry -CurrentPath 'C:\a;C:\bin' -Directory 'C:\bin'
        ($null -eq $answer) | Should -BeTrue -Because "nothing to add must read as null, got '$answer'"
    }

    It 'returns null when the directory is already there' {
        Add-PathEntry -CurrentPath 'C:\a;C:\bin;C:\b' -Directory 'C:\bin' | Should -BeNullOrEmpty
    }

    It 'matches case insensitively, the way Windows paths compare' {
        Add-PathEntry -CurrentPath 'C:\A;C:\BIN' -Directory 'C:\bin' | Should -BeNullOrEmpty
    }

    It 'treats <Existing> as the directory it already holds' -TestCases @(
        @{ Existing = 'C:\Alpacon\bin\' }
        @{ Existing = '"C:\Alpacon\bin"' }
        @{ Existing = '  C:\Alpacon\bin  ' }
    ) {
        Add-PathEntry -CurrentPath "C:\tools;$Existing" -Directory 'C:\Alpacon\bin' |
            Should -BeNullOrEmpty
    }

    It 'folds a relative segment onto the directory it names' -Skip:($env:OS -ne 'Windows_NT') {
        # Windows-only: what counts as an absolute path, and what GetFullPath
        # does with a backslash, are both Windows semantics.
        Add-PathEntry -CurrentPath 'C:\Alpacon\tools\..\bin' -Directory 'C:\Alpacon\bin' |
            Should -BeNullOrEmpty
    }

    It 'matches an entry that is still written as an environment token' {
        $env:ALPACON_TEST_ROOT = 'C:\Alpacon'
        try {
            Add-PathEntry -CurrentPath '%ALPACON_TEST_ROOT%\bin' -Directory 'C:\Alpacon\bin' |
                Should -BeNullOrEmpty
        } finally {
            Remove-Item Env:\ALPACON_TEST_ROOT
        }
    }

    It 'leaves a relative entry alone rather than resolving it here' {
        # GetFullPath would resolve it against this process's working directory,
        # so a relative entry can never stand in for the absolute one.
        Add-PathEntry -CurrentPath 'Alpacon\bin' -Directory 'C:\Alpacon\bin' |
            Should -Be 'Alpacon\bin;C:\Alpacon\bin'
    }

    It 'appends after a trailing semicolon without rewriting what was there' {
        Add-PathEntry -CurrentPath 'C:\tools;;C:\other;' -Directory 'C:\Alpacon\bin' |
            Should -Be 'C:\tools;;C:\other;C:\Alpacon\bin'
    }
}

Describe 'Add-ToUserPath' -Skip:($env:OS -ne 'Windows_NT') {
    BeforeEach {
        $script:SubKey = "Software\AlpaconInstallerTest\$([guid]::NewGuid())"
        $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey($script:SubKey)
        $key.SetValue('Path', '%USERPROFILE%\tools', [Microsoft.Win32.RegistryValueKind]::ExpandString)
        $key.Close()
    }

    AfterEach {
        [Microsoft.Win32.Registry]::CurrentUser.DeleteSubKeyTree($script:SubKey, $false)
    }

    It 'appends the directory and reports that it changed something' {
        Add-ToUserPath -Directory 'C:\bin' -SubKey $script:SubKey | Should -BeTrue
    }

    It 'leaves %USERPROFILE% unexpanded' {
        Add-ToUserPath -Directory 'C:\bin' -SubKey $script:SubKey | Out-Null
        $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($script:SubKey)
        try {
            $raw = $key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
            $raw | Should -Be '%USERPROFILE%\tools;C:\bin'
        } finally { $key.Close() }
    }

    It 'keeps the value kind as REG_EXPAND_SZ' {
        Add-ToUserPath -Directory 'C:\bin' -SubKey $script:SubKey | Out-Null
        $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($script:SubKey)
        try {
            $key.GetValueKind('Path') | Should -Be ([Microsoft.Win32.RegistryValueKind]::ExpandString)
        } finally { $key.Close() }
    }

    It 'does not add the same directory twice' {
        Add-ToUserPath -Directory 'C:\bin' -SubKey $script:SubKey | Out-Null
        Add-ToUserPath -Directory 'C:\bin' -SubKey $script:SubKey | Should -BeFalse

        $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($script:SubKey)
        try {
            $raw = $key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
            ([regex]::Matches($raw, [regex]::Escape('C:\bin'))).Count | Should -Be 1
        } finally { $key.Close() }
    }

    It 'refuses a Path stored as a kind a string write would destroy' {
        $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey($script:SubKey)
        # Cast: SetValue wants a string[] for MultiString, and PowerShell's
        # array literal is an object[], which it refuses.
        $key.SetValue('Path', [string[]]@('C:\one', 'C:\two'), [Microsoft.Win32.RegistryValueKind]::MultiString)
        $key.Close()

        { Add-ToUserPath -Directory 'C:\Alpacon\bin' -SubKey $script:SubKey } |
            Should -Throw -ExpectedMessage '*will not rewrite*'
    }

    It 'creates the Path value when the key has none' {
        $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($script:SubKey, $true)
        $key.DeleteValue('Path')
        $key.Close()

        Add-ToUserPath -Directory 'C:\bin' -SubKey $script:SubKey | Should -BeTrue

        $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($script:SubKey)
        try {
            $key.GetValue('Path') | Should -Be 'C:\bin'
            $key.GetValueKind('Path') | Should -Be ([Microsoft.Win32.RegistryValueKind]::ExpandString)
        } finally { $key.Close() }
    }
}

Describe 'What the installer promises about itself and about the release' {
    BeforeAll {
        $script:Readme = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'README.md') -Raw
        $script:Goreleaser = Get-Content -LiteralPath (Join-Path $PSScriptRoot '.goreleaser.yaml') -Raw
    }

    It 'ships install.ps1 as a release asset' {
        $section = [regex]::Match($script:Goreleaser, '(?m)^release:\r?\n(?:[ \t].*\r?\n|\r?\n)*').Value
        $section | Should -Match 'glob:\s*\./install\.ps1'
    }

    It 'sets neither StrictMode nor the two preferences it uses at file scope' {
        # Read the syntax tree, not the lines: a preference buried in a
        # file-scope if or try block escapes into the caller's shell just as
        # surely as one written flush against the left margin.
        $parseErrors = $null
        $ast = [System.Management.Automation.Language.Parser]::ParseFile(
            (Join-Path $PSScriptRoot 'install.ps1'), [ref]$null, [ref]$parseErrors)
        $parseErrors | Should -BeNullOrEmpty

        $offenders = $ast.FindAll({
            param($node)

            $isPreference =
                ($node -is [System.Management.Automation.Language.CommandAst] -and
                 $node.GetCommandName() -eq 'Set-StrictMode') -or
                ($node -is [System.Management.Automation.Language.AssignmentStatementAst] -and
                 $node.Left.Extent.Text -match '^\$(script:|global:)?(ErrorActionPreference|ProgressPreference)$')
            if (-not $isPreference) { return $false }

            for ($parent = $node.Parent; $parent; $parent = $parent.Parent) {
                if ($parent -is [System.Management.Automation.Language.FunctionDefinitionAst]) { return $false }
            }
            return $true
        }, $true)

        @($offenders | ForEach-Object { $_.Extent.Text }) | Should -BeNullOrEmpty
    }

    It 'decodes every response body in the same expression that reads it' {
        # Only e2e exercises the download itself, so this is what stands between
        # a byte[] body and the byte values being spelled out into the parser.
        # Read the syntax tree: a line-based check misses a trailing comment, a
        # line continuation, and Select-Object -ExpandProperty Content.
        # Narrower than "decoded somewhere": the walk stops at the command
        # enclosing the read, so a two-line read-then-decode is refused too.
        $ast = [System.Management.Automation.Language.Parser]::ParseFile(
            (Join-Path $PSScriptRoot 'install.ps1'), [ref]$null, [ref]$null)

        $reads = $ast.FindAll({
            param($node)

            ($node -is [System.Management.Automation.Language.MemberExpressionAst] -and
             "$($node.Member.Extent.Text)" -eq 'Content') -or
            ($node -is [System.Management.Automation.Language.CommandAst] -and
             $node.GetCommandName() -eq 'Select-Object' -and
             $node.Extent.Text -match 'Content')
        }, $true)

        $undecoded = $reads | Where-Object {
            $call = $_.Parent
            while ($call -and $call -isnot [System.Management.Automation.Language.CommandAst]) {
                $call = $call.Parent
            }
            -not ($call -and $call.GetCommandName() -eq 'ConvertTo-ResponseText')
        }

        @($undecoded | ForEach-Object { $_.Extent.Text }) | Should -BeNullOrEmpty
    }

    It 'stays pure ASCII with no BOM, since Windows PowerShell 5.1 reads it as ANSI' {
        $bytes = [System.IO.File]::ReadAllBytes((Join-Path $PSScriptRoot 'install.ps1'))
        @($bytes | Where-Object { $_ -gt 127 }) | Should -BeNullOrEmpty
    }

    It 'hashes install.ps1 alongside the archives, since it is run as downloaded' {
        # Anchored to the stanza: a count over the whole file passes just as
        # happily when both globs sit under release.
        $section = [regex]::Match($script:Goreleaser, '(?m)^checksum:\r?\n(?:[ \t].*\r?\n|\r?\n)*').Value
        $section | Should -Match 'glob:\s*\./install\.ps1'
    }

    It 'still names archives the way install.ps1 assembles them' {
        # install.ps1 spells alpacon-<version>-windows-<arch>.zip out by hand, so
        # a template edit here 404s every install from the next tag onward.
        $section = [regex]::Match($script:Goreleaser, '(?m)^archives:\r?\n(?:[ \t-].*\r?\n|\r?\n)*').Value
        $section | Should -Match '\{\{ \.ProjectName \}\}-\{\{ trimprefix \.Version "v" \}\}-\{\{ \.Os \}\}-\{\{ \.Arch \}\}'
        # One list item, not two: a lazy dot-all match would happily pair a
        # windows override with a zip belonging to some later entry.
        $section | Should -Match '- goos: windows\r?\n\s+format: zip'
    }

    It 'keeps the archive flat, since install.ps1 moves alpacon.exe from its root' {
        $section = [regex]::Match($script:Goreleaser, '(?m)^archives:\r?\n(?:[ \t-].*\r?\n|\r?\n)*').Value
        $section | Should -Not -Match 'wrap_in_directory'
    }

    It 'still carries the project name install.ps1 spells out' {
        # Every asset name starts with it, and install.ps1 writes it literally.
        $script:Goreleaser | Should -Match '(?m)^project_name:\s*alpacon\s*$'
    }

    It 'still names the checksum file the way install.ps1 assembles it' {
        $section = [regex]::Match($script:Goreleaser, '(?m)^checksum:\r?\n(?:[ \t].*\r?\n|\r?\n)*').Value
        $section | Should -Match '\{\{ \.ProjectName \}\}-\{\{ trimprefix \.Version "v" \}\}-checksums\.sha256'
    }

    It 'documents a URL whose file name is the asset GoReleaser uploads' {
        $script:Readme | Should -Match 'releases/latest/download/install\.ps1'
    }

    It 'documents the parameterized form too, since irm | iex cannot pass arguments' {
        $script:Readme | Should -Match 'scriptblock\]::Create'
    }
}
