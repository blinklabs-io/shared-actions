$ErrorActionPreference = 'Stop'
$sign = Join-Path $PSScriptRoot '../sign-windows/sign.ps1'
$root = New-Item -ItemType Directory -Path (Join-Path $env:RUNNER_TEMP ([guid]::NewGuid().ToString()))
function Invoke-WebRequest { param($Uri, $OutFile) Set-Content -LiteralPath $OutFile -Value 'fixture' }
function Get-FileHash { param($LiteralPath, $Algorithm) @{ Hash = '05ca18d4ab7b8c2183289b5378d32860f0ea0f3bdab1f1b8cae5894fb225fa8a' } }
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
    $verified.Add($LiteralPath)
    return @{ Status = 'Valid'; TimeStamperCertificate = $tsa }
}
try {
    $env:GITHUB_WORKSPACE = $root.FullName + [IO.Path]::DirectorySeparatorChar
    $env:RUNNER_TEMP = (New-Item -ItemType Directory -Path (Join-Path $root 'temp')).FullName
    $env:SIGN_FILES = "app.exe`nproduct.msi"
    $env:CERTIFICATE_CHAIN = 'Y2VydA=='
    $env:SIGN_KEYSTORE = 'fixture-store'
    $env:SIGN_KEY_NAME = 'fixture-alias'
    $env:SIGN_NAME = 'Fixture'
    $env:SIGN_URL = 'https://example.invalid'
    $env:TSA_URL = 'https://example.invalid/timestamp'
    foreach ($name in 'app.exe','product.msi') { Set-Content -LiteralPath (Join-Path $root $name) -Value 'fixture' }
    foreach ($test in @(
        @{ Java = 0; Timestamp = 'TSA'; Error = ''; Signed = 2; Verified = 2 }
        @{ Java = 1; Timestamp = 'TSA'; Error = 'jsign failed'; Signed = 1; Verified = 0 }
        @{ Java = 0; Timestamp = $null; Error = 'Missing Authenticode timestamp'; Signed = 1; Verified = 1 }
    )) {
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
