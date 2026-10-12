<#
.SYNOPSIS
Installs ado from GitHub Releases.

.DESCRIPTION
Downloads the Windows zip for this machine's architecture, verifies it against
checksums.txt (SHA-256), and checksums.txt against its cosign bundle when cosign
is installed. Installs ado.exe without admin rights and adds the directory to
the user PATH unless -NoModifyPath is given.

Under `irm | iex` parameters can't be passed: set ADO_VERSION and
ADO_INSTALL_DIR instead, or use the scriptblock form.

.EXAMPLE
irm https://snake-in-prod.github.io/ado-gh/install.ps1 | iex

.EXAMPLE
$env:ADO_VERSION = '1.2.3'; $env:ADO_INSTALL_DIR = 'C:\tools\ado'; irm https://snake-in-prod.github.io/ado-gh/install.ps1 | iex

.EXAMPLE
& ([scriptblock]::Create((irm https://snake-in-prod.github.io/ado-gh/install.ps1))) -Version 1.2.3 -BinDir C:\tools\ado -NoModifyPath
#>
[CmdletBinding()]
param(
    # Release to install, e.g. 1.2.3 (default: $env:ADO_VERSION, else latest).
    [string]$Version,
    # Install directory (default: $env:ADO_INSTALL_DIR, else %LocalAppData%\Programs\ado).
    [string]$BinDir,
    # Don't add the install directory to the user PATH.
    [switch]$NoModifyPath
)

# The machine architecture. $env:PROCESSOR_ARCHITECTURE is per process: x64
# PowerShell on an ARM64 machine reports AMD64, the registry doesn't.
function Get-AdoArch {
    [OutputType([string])]
    param()
    $arch = (Get-ItemProperty -Path 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Environment' -Name PROCESSOR_ARCHITECTURE).PROCESSOR_ARCHITECTURE
    switch ($arch) {
        'AMD64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        default { throw "ado-install: unsupported architecture: $arch" }
    }
}

function Invoke-AdoDownload {
    param(
        [Parameter(Mandatory = $true)][string]$Uri,
        [Parameter(Mandatory = $true)][string]$OutFile
    )
    # -UseBasicParsing: without it Windows PowerShell 5.1 shows a security prompt
    # that hangs `irm | iex`.
    Invoke-WebRequest -Uri $Uri -OutFile $OutFile -UseBasicParsing
}

# Get-AdoChecksum returns a name -> sha256 table from a GoReleaser checksums.txt.
function Get-AdoChecksum {
    [OutputType([hashtable])]
    param([Parameter(Mandatory = $true)][string]$Path)
    $table = @{}
    foreach ($line in Get-Content -LiteralPath $Path) {
        $fields = $line -split '\s+'
        if ($fields.Count -ge 2) { $table[$fields[1].TrimStart('*')] = $fields[0] }
    }
    return $table
}

# Broadcasts WM_SETTINGCHANGE so Explorer and new shells see the new PATH.
function Send-AdoEnvironmentChange {
    if (-not ('AdoInstall.NativeMethods' -as [type])) {
        Add-Type -Namespace AdoInstall -Name NativeMethods -MemberDefinition @'
[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);
'@
    }
    $result = [UIntPtr]::Zero
    # HWND_BROADCAST, WM_SETTINGCHANGE, SMTO_ABORTIFHUNG, 5 s.
    [void][AdoInstall.NativeMethods]::SendMessageTimeout([IntPtr]0xffff, 0x1a, [UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$result)
}

# Appends Dir to the user PATH in HKCU\<SubKey> unless present. Reads and writes
# the raw value: [Environment]::SetEnvironmentVariable would expand %VAR%
# entries and turn REG_EXPAND_SZ into REG_SZ. Returns $true when it changed.
function Add-AdoUserPath {
    [OutputType([bool])]
    param(
        [Parameter(Mandatory = $true)][string]$Dir,
        [string]$SubKey = 'Environment'
    )
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey($SubKey)
    try {
        $old = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $parts = @($old -split ';' | Where-Object { $_ })
        foreach ($p in $parts) {
            if ($p.TrimEnd('\') -eq $Dir.TrimEnd('\')) { return $false }
        }
        $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
        if ($key.GetValueNames() -contains 'Path') { $kind = $key.GetValueKind('Path') }
        $key.SetValue('Path', (($parts + $Dir) -join ';'), $kind)
    }
    finally {
        $key.Close()
    }
    Send-AdoEnvironmentChange
    return $true
}

function Install-Ado {
    param(
        [string]$Version,
        [string]$BinDir,
        [switch]$NoModifyPath
    )
    # Function scope: under `irm | iex` these don't leak into the caller's session.
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'

    if (-not $Version) { $Version = $env:ADO_VERSION }
    if (-not $BinDir) { $BinDir = $env:ADO_INSTALL_DIR }
    if (-not $BinDir) { $BinDir = Join-Path $env:LOCALAPPDATA 'Programs\ado' }

    # .NET 4.7+ picks TLS versions from the OS (SystemDefault); only older
    # frameworks need Tls12 switched on.
    if ("$([Net.ServicePointManager]::SecurityProtocol)" -ne 'SystemDefault') {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    }

    $repo = 'snake-in-prod/ado-gh'
    # ADO_INSTALL_BASE_URL is a test-only override.
    $base = $env:ADO_INSTALL_BASE_URL
    if (-not $base) { $base = "https://github.com/$repo/releases" }
    $suffix = '_windows_' + (Get-AdoArch) + '.zip'

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ('ado-install-' + [guid]::NewGuid())
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        $sumsFile = Join-Path $tmp 'checksums.txt'
        if ($Version) {
            $Version = $Version.TrimStart('v')
            if ($Version -notmatch '^[0-9A-Za-z.-]+$') { throw "ado-install: invalid version: $Version" }
            Invoke-AdoDownload -Uri "$base/download/v$Version/checksums.txt" -OutFile $sumsFile
            $sums = Get-AdoChecksum -Path $sumsFile
        }
        else {
            Invoke-AdoDownload -Uri "$base/latest/download/checksums.txt" -OutFile $sumsFile
            $sums = Get-AdoChecksum -Path $sumsFile
            $latest = $sums.Keys | Where-Object { $_ -like "ado_*$suffix" } | Select-Object -First 1
            if (-not $latest) { throw "ado-install: latest release has no ado_*$suffix archive" }
            $Version = $latest.Substring(4, $latest.Length - 4 - $suffix.Length)
        }
        $tag = "v$Version"
        $asset = "ado_$Version$suffix"

        if (Get-Command cosign -ErrorAction SilentlyContinue) {
            Write-Host 'ado-install: verifying checksums.txt with cosign'
            $bundle = Join-Path $tmp 'checksums.txt.sigstore.json'
            Invoke-AdoDownload -Uri "$base/download/$tag/checksums.txt.sigstore.json" -OutFile $bundle
            & cosign verify-blob --bundle $bundle `
                --certificate-identity "https://github.com/$repo/.github/workflows/release.yml@refs/tags/$tag" `
                --certificate-oidc-issuer https://token.actions.githubusercontent.com $sumsFile
            if ($LASTEXITCODE -ne 0) { throw 'ado-install: cosign verification of checksums.txt failed' }
        }

        $want = $sums[$asset]
        if (-not $want) { throw "ado-install: $asset is not listed in checksums.txt" }
        Write-Host "ado-install: downloading $asset"
        $zip = Join-Path $tmp $asset
        Invoke-AdoDownload -Uri "$base/download/$tag/$asset" -OutFile $zip
        $got = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash
        if ($got -ne $want) { throw "ado-install: checksum mismatch for ${asset}: got $got, want $want" }

        $x = Join-Path $tmp 'x'
        Expand-Archive -LiteralPath $zip -DestinationPath $x
        New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
        Copy-Item -LiteralPath (Join-Path $x 'ado.exe') -Destination (Join-Path $BinDir 'ado.exe') -Force
        Write-Host "ado-install: installed ado $Version to $(Join-Path $BinDir 'ado.exe')"
    }
    finally {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }

    $onPath = @($env:Path -split ';' | Where-Object { $_ -and $_.TrimEnd('\') -eq $BinDir.TrimEnd('\') }).Count -gt 0
    if ($NoModifyPath) {
        if (-not $onPath) { Write-Host "ado-install: $BinDir is not on your PATH; add it to run ado by name." }
        return
    }
    if (Add-AdoUserPath -Dir $BinDir) { Write-Host "ado-install: added $BinDir to your user PATH." }
    if (-not $onPath) { $env:Path = "$env:Path;$BinDir" }
}

Install-Ado -Version $Version -BinDir $BinDir -NoModifyPath:$NoModifyPath
