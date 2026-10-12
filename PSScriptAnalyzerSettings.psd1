# CI fails on any result (any severity):
#   if ($r = Invoke-ScriptAnalyzer -Path scripts -Recurse -Settings ./PSScriptAnalyzerSettings.psd1) { $r; exit 1 }
@{
    # The installer talks to a person through `irm | iex`; Write-Output would
    # leak into the caller's pipeline.
    ExcludeRules = @('PSAvoidUsingWriteHost')
    Rules        = @{
        # install.ps1 must run on Windows PowerShell 5.1.
        PSUseCompatibleSyntax = @{
            Enable         = $true
            TargetVersions = @('5.1', '7.0')
        }
    }
}
