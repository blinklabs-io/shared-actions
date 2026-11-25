param([Parameter(Mandatory)][string]$Path)
$signature = Get-AuthenticodeSignature -LiteralPath $Path -ErrorAction Stop
if ($signature.Status -ne 'Valid') { throw "Invalid Authenticode signature on ${Path}: $($signature.Status)" }
if ($null -eq $signature.TimeStamperCertificate) { throw "Missing Authenticode timestamp on ${Path}." }
Write-Host "Verified signature and timestamp on $Path"
