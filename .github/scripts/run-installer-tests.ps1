# Runs the installer suite under whichever PowerShell edition launched this
# script. It exists as a file because the matrix context is not available in a
# step's `shell:`, so the edition has to be the host the step launches.
Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'

Install-Module Pester -RequiredVersion 6.1.0 -Force -SkipPublisherCheck -Scope CurrentUser
# The runner image ships its own Pester, and auto-loading takes the highest
# version it finds, not the one installed above.
Import-Module Pester -RequiredVersion 6.1.0 -Force

# Located from this script rather than from the working directory, so an
# absolute call from anywhere runs the suite instead of discovering nothing.
$suite = Join-Path $PSScriptRoot '..\..\install.Tests.ps1'
$result = Invoke-Pester $suite -Output Detailed -PassThru
# FailedCount stays 0 when discovery itself throws or when the file yields no
# tests at all, and Invoke-Pester raises nothing either way. Exactly one skip is
# expected here: the suite has a single deliberately Unix-only test, and every
# other -Skip condition is false on Windows. Any other count means coverage went
# missing.
if ($result.Result -ne 'Passed' -or $result.TotalCount -eq 0 -or $result.SkippedCount -ne 1) { exit 1 }
