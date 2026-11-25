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
    @{ Status = 'Valid'; Type = 'Catalog'; Timestamp = 'test TSA'; Error = 'Expected an embedded Authenticode signature' }
    @{ Status = 'HashMismatch'; Timestamp = 'test TSA'; Error = 'Invalid Authenticode signature' }
)) {
    $type = if ($case.Type) { $case.Type } else { 'Authenticode' }
    $sample = [pscustomobject]@{ Status = $case.Status; SignatureType = $type; TimeStamperCertificate = $case.Timestamp }
    $message = ''
    try { & $verify -Path 'artifact.msi' } catch { $message = $_.Exception.Message }
    if (($case.Error -eq '' -and $message) -or ($case.Error -and -not $message.StartsWith($case.Error))) {
        throw "Expected '$($case.Error)' for $($case.Status), received '$message'."
    }
}
