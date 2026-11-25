$ErrorActionPreference = 'Stop'
$sign = Join-Path $PSScriptRoot '../sign-windows/sign.ps1'
$root = New-Item -ItemType Directory -Path (Join-Path $env:RUNNER_TEMP ([guid]::NewGuid().ToString()))
function Invoke-WebRequest { param($Uri, $OutFile) Set-Content -LiteralPath $OutFile -Value 'fixture' }
function Get-FileHash { param($LiteralPath, $Algorithm) @{ Hash = $digest } }
function gcloud { $global:LASTEXITCODE = 0; 'fixture-token' }
function java {
    if ($args -notcontains 'env:JSIGN_STOREPASS' -or $args -contains 'fixture-token' -or $args -notcontains 'RFC3161') {
        throw 'Unexpected jsign arguments.'
    }
    $signed.Add($args[-1])
    $global:LASTEXITCODE = $javaStatus
}
function Get-AuthenticodeSignature {
    param($LiteralPath, $ErrorAction)
    if (-not $signed.Contains($LiteralPath)) {
        return @{ Status = $priorStatus; SignerCertificate = $null }
    }
    $verified.Add($LiteralPath)
    return @{ Status = 'Valid'; SignatureType = 'Authenticode'; TimeStamperCertificate = $tsa }
}
try {
    $env:GITHUB_WORKSPACE = $root.FullName + [IO.Path]::DirectorySeparatorChar
    $env:RUNNER_TEMP = (New-Item -ItemType Directory -Path (Join-Path $root 'temp')).FullName
    $env:CERTIFICATE_CHAIN = 'Y2VydA=='
    $env:SIGN_KEYSTORE = 'fixture-store'
    $env:SIGN_KEY_NAME = 'fixture-alias'
    $env:SIGN_NAME = 'Fixture'
    $env:SIGN_URL = 'https://example.invalid'
    $env:TSA_URL = 'https://example.invalid/timestamp'
    foreach ($name in 'app.exe','product.msi') { Set-Content -LiteralPath (Join-Path $root $name) -Value 'fixture' }
    $target = New-Item -ItemType Directory -Path (Join-Path $root 'target')
    Set-Content -LiteralPath (Join-Path $target 'app.exe') -Value 'fixture'
    $null = New-Item -ItemType Junction -Path (Join-Path $root 'junction') -Target $target.FullName
    foreach ($test in @(
        @{ WrongDigest = $true; Error = 'jsign checksum mismatch'; Signed = 0; Verified = 0 }
        @{ Files = (Join-Path $root 'app.exe'); Error = 'Signing paths must be relative'; Signed = 0; Verified = 0 }
        @{ Files = 'target/../app.exe'; Error = 'Signing paths must be relative'; Signed = 0; Verified = 0 }
        @{ Files = 'junction/app.exe'; Error = 'Linked signing path'; Signed = 0; Verified = 0 }
        @{ Java = 0; Timestamp = 'TSA'; Error = ''; Signed = 2; Verified = 2 }
        @{ Files = "app.exe`napp.exe"; Java = 0; Timestamp = 'TSA'; Error = 'Expected an unsigned artifact'; Signed = 1; Verified = 2 }
        @{ Prior = 'Valid'; Java = 0; Timestamp = 'TSA'; Error = 'Expected an unsigned artifact'; Signed = 0; Verified = 0 }
        @{ Java = 1; Timestamp = 'TSA'; Error = 'jsign failed'; Signed = 1; Verified = 0 }
        @{ Java = 0; Timestamp = $null; Error = 'Missing Authenticode timestamp'; Signed = 1; Verified = 1 }
    )) {
        $env:SIGN_FILES = if ($test.Files) { $test.Files } else { "app.exe`nproduct.msi" }
        $digest = if ($test.WrongDigest) { '0' * 64 } else { '05ca18d4ab7b8c2183289b5378d32860f0ea0f3bdab1f1b8cae5894fb225fa8a' }
        $priorStatus = if ($test.Prior) { $test.Prior } else { 'NotSigned' }
        $signed = [Collections.Generic.List[string]]::new()
        $verified = [Collections.Generic.List[string]]::new()
        $javaStatus = $test.Java
        $tsa = $test.Timestamp
        $message = ''
        try { & $sign } catch { $message = $_.Exception.Message }
        if (($test.Error -eq '' -and $message) -or ($test.Error -and -not $message.StartsWith($test.Error))) {
            throw "Expected '$($test.Error)', received '$message'."
        }
        if ($signed.Count -ne $test.Signed -or $verified.Count -ne $test.Verified) { throw 'Unexpected signing or verification count.' }
        if ($env:JSIGN_STOREPASS -or (Get-ChildItem -Force -LiteralPath $env:RUNNER_TEMP)) { throw 'Signing material survived cleanup.' }
    }
} finally {
    Remove-Item -LiteralPath $root.FullName -Recurse -Force
}
