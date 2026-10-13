# Pester 5 tests for install.ps1. Run under Windows PowerShell 5.1 and pwsh:
#   Invoke-Pester -Path scripts/install.Tests.ps1
# Downloads are mocked from fixture releases; registry tests use a scratch
# HKCU key, so the real user PATH is never touched.

BeforeAll {
    $script:installScript = Join-Path $PSScriptRoot 'install.ps1'

    # Load the functions without running the final Install-Ado call.
    $parseErrors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile($script:installScript, [ref]$null, [ref]$parseErrors)
    if ($parseErrors) { throw "install.ps1 does not parse: $parseErrors" }
    $functions = $ast.FindAll({ $args[0] -is [Management.Automation.Language.FunctionDefinitionAst] }, $false)
    foreach ($f in $functions) { . ([scriptblock]::Create($f.Extent.Text)) }

    $script:defaultBase = 'https://github.com/snake-in-prod/ado-gh/releases'
    $script:cosignStub = $null # set by the cosign Context
    $script:srv = Join-Path $TestDrive 'srv'

    # Fixture releases: <srv>/download/v<version>/ado_<version>_windows_<arch>.zip
    # plus checksums.txt; ado.exe holds "ado <version> windows_<arch>".
    function Build-Release([string]$Version, [switch]$Corrupt) {
        $dir = Join-Path $script:srv "download/v$Version"
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
        $lines = @()
        foreach ($arch in 'amd64', 'arm64') {
            $build = Join-Path $TestDrive "build-$Version-$arch"
            New-Item -ItemType Directory -Path $build -Force | Out-Null
            Set-Content -LiteralPath (Join-Path $build 'ado.exe') -Value "ado $Version windows_$arch"
            $zip = Join-Path $dir "ado_${Version}_windows_$arch.zip"
            Compress-Archive -Path (Join-Path $build 'ado.exe') -DestinationPath $zip
            $sum = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLower()
            if ($Corrupt) { $sum = '0' * 64 }
            $lines += "$sum  ado_${Version}_windows_$arch.zip.sbom.json"
            $lines += "$sum  ado_${Version}_windows_$arch.zip"
        }
        Set-Content -LiteralPath (Join-Path $dir 'checksums.txt') -Value $lines
        Set-Content -LiteralPath (Join-Path $dir 'checksums.txt.sigstore.json') -Value '{}'
    }
    Build-Release -Version '1.2.3'
    Build-Release -Version '1.0.0'
    Build-Release -Version '9.9.9' -Corrupt
    New-Item -ItemType Directory -Path (Join-Path $script:srv 'latest/download') -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $script:srv 'download/v1.2.3/checksums.txt') -Destination (Join-Path $script:srv 'latest/download/checksums.txt')

    function Get-Installed([string]$Dir) {
        (Get-Content -LiteralPath (Join-Path $Dir 'ado.exe') -Raw).Trim()
    }
}

Describe 'install.ps1' {
    BeforeEach {
        $script:bin = Join-Path $TestDrive ('bin-' + [guid]::NewGuid())
        foreach ($name in 'ADO_VERSION', 'ADO_INSTALL_DIR', 'ADO_INSTALL_BASE_URL') {
            Remove-Item -Path "Env:$name" -ErrorAction SilentlyContinue
        }
        $script:savedPath = $env:Path

        Mock Invoke-WebRequest {
            $prefix = $script:defaultBase
            if ($env:ADO_INSTALL_BASE_URL) { $prefix = $env:ADO_INSTALL_BASE_URL }
            if (-not $Uri.ToString().StartsWith("$prefix/")) { throw "unexpected URL $Uri" }
            $file = Join-Path $script:srv $Uri.ToString().Substring($prefix.Length + 1)
            if (-not (Test-Path -LiteralPath $file)) { throw "404 $Uri" }
            Copy-Item -LiteralPath $file -Destination $OutFile
        }
        Mock Get-ItemProperty -ParameterFilter { $Path -like 'HKLM:*' } {
            [pscustomobject]@{ PROCESSOR_ARCHITECTURE = 'AMD64' }
        }
        Mock Add-AdoUserPath { $true }
        # A real cosign on PATH would verify the fake bundle and fail: only the
        # cosign Context's stub is found.
        Mock Get-Command -ParameterFilter { $Name -eq 'cosign' } {
            if ($script:cosignStub) { Microsoft.PowerShell.Core\Get-Command -Name $script:cosignStub }
        }
    }

    AfterEach {
        $env:Path = $script:savedPath
        foreach ($name in 'ADO_VERSION', 'ADO_INSTALL_DIR', 'ADO_INSTALL_BASE_URL') {
            Remove-Item -Path "Env:$name" -ErrorAction SilentlyContinue
        }
    }

    It 'parses without errors' {
        $errors = $null
        [Management.Automation.Language.Parser]::ParseFile($script:installScript, [ref]$null, [ref]$errors) | Out-Null
        $errors | Should -BeNullOrEmpty
    }

    It 'installs the latest release from GitHub with -UseBasicParsing' {
        Install-Ado -BinDir $script:bin
        Get-Installed $script:bin | Should -Be 'ado 1.2.3 windows_amd64'
        Should -Invoke Invoke-WebRequest -Times 1 -Exactly -ParameterFilter { "$Uri" -eq "$script:defaultBase/latest/download/checksums.txt" }
        Should -Invoke Invoke-WebRequest -Times 1 -Exactly -ParameterFilter { "$Uri" -eq "$script:defaultBase/download/v1.2.3/ado_1.2.3_windows_amd64.zip" }
        Should -Invoke Invoke-WebRequest -Times 0 -Exactly -ParameterFilter { -not $UseBasicParsing }
    }

    It 'honours the test-only base URL override' {
        $env:ADO_INSTALL_BASE_URL = 'http://127.0.0.1:1/fixture'
        Install-Ado -BinDir $script:bin
        Get-Installed $script:bin | Should -Be 'ado 1.2.3 windows_amd64'
        Should -Invoke Invoke-WebRequest -Times 0 -Exactly -ParameterFilter { "$Uri" -like "$script:defaultBase*" }
    }

    It 'installs -Version <V> with or without a leading v' -TestCases @(@{ V = '1.0.0' }, @{ V = 'v1.0.0' }) {
        Install-Ado -Version $V -BinDir $script:bin
        Get-Installed $script:bin | Should -Be 'ado 1.0.0 windows_amd64'
    }

    It 'falls back to ADO_VERSION and ADO_INSTALL_DIR' {
        $env:ADO_VERSION = '1.0.0'
        $env:ADO_INSTALL_DIR = $script:bin
        Install-Ado
        Get-Installed $script:bin | Should -Be 'ado 1.0.0 windows_amd64'
    }

    It 'prefers -Version and -BinDir over the environment' {
        $env:ADO_VERSION = '9.9.9'
        $env:ADO_INSTALL_DIR = Join-Path $TestDrive 'env-dir'
        Install-Ado -Version 1.0.0 -BinDir $script:bin
        Get-Installed $script:bin | Should -Be 'ado 1.0.0 windows_amd64'
        Join-Path $TestDrive 'env-dir' | Should -Not -Exist
    }

    It 'defaults to %LocalAppData%\Programs\ado' {
        $saved = $env:LOCALAPPDATA
        $env:LOCALAPPDATA = Join-Path $TestDrive 'localappdata'
        try {
            Install-Ado
            Get-Installed (Join-Path $env:LOCALAPPDATA 'Programs\ado') | Should -Be 'ado 1.2.3 windows_amd64'
        }
        finally {
            $env:LOCALAPPDATA = $saved
        }
    }

    It 'installs the arm64 build when HKLM reports ARM64' {
        Mock Get-ItemProperty -ParameterFilter { $Path -like 'HKLM:*' } {
            [pscustomobject]@{ PROCESSOR_ARCHITECTURE = 'ARM64' }
        }
        Install-Ado -BinDir $script:bin
        Get-Installed $script:bin | Should -Be 'ado 1.2.3 windows_arm64'
    }

    It 'rejects an unsupported architecture' {
        Mock Get-ItemProperty -ParameterFilter { $Path -like 'HKLM:*' } {
            [pscustomobject]@{ PROCESSOR_ARCHITECTURE = 'x86' }
        }
        { Install-Ado -BinDir $script:bin } | Should -Throw '*unsupported architecture: x86*'
    }

    It 'aborts on a checksum mismatch and installs nothing' {
        $tempBefore = @(Get-ChildItem -Path ([IO.Path]::GetTempPath()) -Filter 'ado-install-*').Count
        { Install-Ado -Version 9.9.9 -BinDir $script:bin } | Should -Throw '*checksum mismatch*'
        Join-Path $script:bin 'ado.exe' | Should -Not -Exist
        @(Get-ChildItem -Path ([IO.Path]::GetTempPath()) -Filter 'ado-install-*').Count | Should -Be $tempBefore
        Should -Invoke Add-AdoUserPath -Times 0 -Exactly
    }

    It 'rejects an invalid version' {
        { Install-Ado -Version '../../x' -BinDir $script:bin } | Should -Throw '*invalid version*'
    }

    It 'names the file and the likely cause when a download fails' {
        { Install-Ado -Version 4.5.6 -BinDir $script:bin } |
            Should -Throw 'ado-install: cannot download checksums.txt for v4.5.6 (does the release exist?): 404 *'
    }

    It 'resolves a relative -BinDir before adding it to the PATH' {
        Push-Location -LiteralPath $TestDrive
        try {
            Install-Ado -BinDir 'relative-bin'
        }
        finally {
            Pop-Location
        }
        $script:relativeBin = Join-Path $TestDrive 'relative-bin'
        Get-Installed $script:relativeBin | Should -Be 'ado 1.2.3 windows_amd64'
        Should -Invoke Add-AdoUserPath -Times 1 -Exactly -ParameterFilter { $Dir -eq $script:relativeBin }
    }

    It 'upgrades an ado.exe that is running' -Skip:($env:OS -ne 'Windows_NT') {
        Install-Ado -Version 1.0.0 -BinDir $script:bin
        $exe = Join-Path $script:bin 'ado.exe'
        # What a running image allows: renaming, not writing.
        $lock = [IO.File]::Open($exe, 'Open', 'Read', 'Read, Delete')
        try {
            Install-Ado -BinDir $script:bin
        }
        finally {
            $lock.Dispose()
        }
        Get-Installed $script:bin | Should -Be 'ado 1.2.3 windows_amd64'
    }

    It 'adds the bin dir to the user PATH and the session' {
        Install-Ado -BinDir $script:bin
        Should -Invoke Add-AdoUserPath -Times 1 -Exactly -ParameterFilter { $Dir -eq $script:bin }
        $env:Path -split ';' | Should -Contain $script:bin
    }

    It '-NoModifyPath leaves the user PATH untouched and prints a hint' {
        Mock Write-Host { }
        Install-Ado -BinDir $script:bin -NoModifyPath
        Get-Installed $script:bin | Should -Be 'ado 1.2.3 windows_amd64'
        Should -Invoke Add-AdoUserPath -Times 0 -Exactly
        $env:Path | Should -Be $script:savedPath
        Should -Invoke Write-Host -Times 1 -Exactly -ParameterFilter { "$Object" -like '*is not on your PATH*' }
    }

    # A real cosign.cmd on PATH: checks the exact arguments a native cosign gets.
    Context 'with cosign installed' -Skip:($env:OS -ne 'Windows_NT') {
        BeforeAll {
            $script:cosignDir = Join-Path $TestDrive 'cosign-bin'
            New-Item -ItemType Directory -Path $script:cosignDir | Out-Null
            Set-Content -LiteralPath (Join-Path $script:cosignDir 'cosign.cmd') -Encoding Ascii -Value @(
                '@echo off'
                'echo %*> "%STUB_COSIGN_LOG%"'
                'exit /b %STUB_COSIGN_EXIT%'
            )
            $script:cosignStub = Join-Path $script:cosignDir 'cosign.cmd'
            $script:pathBeforeCosign = $env:Path
            $env:Path = "$script:cosignDir;$env:Path"
            $env:STUB_COSIGN_LOG = Join-Path $TestDrive 'cosign.log'
        }

        AfterAll {
            $script:cosignStub = $null
            $env:Path = $script:pathBeforeCosign
            Remove-Item -Path Env:STUB_COSIGN_LOG, Env:STUB_COSIGN_EXIT -ErrorAction SilentlyContinue
        }

        It 'verifies checksums.txt against the bundle' {
            $env:STUB_COSIGN_EXIT = '0'
            Install-Ado -Version 1.0.0 -BinDir $script:bin
            Get-Installed $script:bin | Should -Be 'ado 1.0.0 windows_amd64'
            $log = Get-Content -LiteralPath $env:STUB_COSIGN_LOG -Raw
            $log | Should -BeLike 'verify-blob --bundle *checksums.txt.sigstore.json*'
            $log | Should -BeLike '*--certificate-identity https://github.com/snake-in-prod/ado-gh/.github/workflows/release.yml@refs/tags/v1.0.0 *'
            $log | Should -BeLike '*--certificate-oidc-issuer https://token.actions.githubusercontent.com *checksums.txt*'
            Should -Invoke Invoke-WebRequest -Times 1 -Exactly -ParameterFilter { "$Uri" -eq "$script:defaultBase/download/v1.0.0/checksums.txt.sigstore.json" }
        }

        It 'aborts when verification fails' {
            $env:STUB_COSIGN_EXIT = '1'
            { Install-Ado -BinDir $script:bin } | Should -Throw '*cosign verification*'
            Join-Path $script:bin 'ado.exe' | Should -Not -Exist
        }
    }
}

Describe 'Add-AdoUserPath' -Skip:($env:OS -ne 'Windows_NT') {
    BeforeAll {
        $script:subKey = 'Software\ado-install-test-' + [guid]::NewGuid()
        Mock Send-AdoEnvironmentChange { }
    }

    AfterAll {
        [Microsoft.Win32.Registry]::CurrentUser.DeleteSubKeyTree($script:subKey, $false)
    }

    It 'appends without expanding %VAR% entries and keeps REG_EXPAND_SZ' {
        $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey($script:subKey)
        $key.SetValue('Path', '%USERPROFILE%\bin;C:\x', [Microsoft.Win32.RegistryValueKind]::ExpandString)
        $key.Close()

        Add-AdoUserPath -Dir 'C:\ado' -SubKey $script:subKey | Should -BeTrue
        Add-AdoUserPath -Dir 'C:\ADO\' -SubKey $script:subKey | Should -BeFalse

        $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($script:subKey)
        try {
            $key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) | Should -Be '%USERPROFILE%\bin;C:\x;C:\ado'
            $key.GetValueKind('Path') | Should -Be ([Microsoft.Win32.RegistryValueKind]::ExpandString)
        }
        finally {
            $key.Close()
        }
        Should -Invoke Send-AdoEnvironmentChange -Times 1 -Exactly
    }

    It 'creates the value as REG_EXPAND_SZ when missing' {
        $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey($script:subKey)
        $key.DeleteValue('Path', $false)
        $key.Close()

        Add-AdoUserPath -Dir 'C:\ado' -SubKey $script:subKey | Should -BeTrue

        $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($script:subKey)
        try {
            $key.GetValue('Path') | Should -Be 'C:\ado'
            $key.GetValueKind('Path') | Should -Be ([Microsoft.Win32.RegistryValueKind]::ExpandString)
        }
        finally {
            $key.Close()
        }
    }
}
