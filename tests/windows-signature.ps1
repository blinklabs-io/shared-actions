$ErrorActionPreference = 'Stop'
$verify = Join-Path $PSScriptRoot '../sign-windows/verify.ps1'
function Get-AuthenticodeSignature {
    param($LiteralPath, $ErrorAction)
    if ($LiteralPath -ne 'artifact.msi') { throw "Unexpected path: $LiteralPath" }
    return $sample
}
foreach ($case in @(
    @{ Status = 'Valid'; Timestamp = 'test TSA'; Error = '' }
    @{ Status = 'Valid'; Timestamp = $null; Error = 'Missing Authenticode timestamp' }
    @{ Status = 'HashMismatch'; Timestamp = 'test TSA'; Error = 'Invalid Authenticode signature' }
)) {
    $sample = [pscustomobject]@{ Status = $case.Status; TimeStamperCertificate = $case.Timestamp }
    $message = ''
    try { & $verify -Path 'artifact.msi' } catch { $message = $_.Exception.Message }
    if (($case.Error -eq '' -and $message) -or ($case.Error -and -not $message.StartsWith($case.Error))) {
        throw "Expected '$($case.Error)' for $($case.Status), received '$message'."
    }
}
