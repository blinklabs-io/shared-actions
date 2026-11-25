$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'Windows is required for Authenticode verification.' }

$workspace = [IO.Path]::TrimEndingDirectorySeparator((Get-Item -LiteralPath $env:GITHUB_WORKSPACE).FullName)
$prefix = $workspace.TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
$files = @(foreach ($line in ($env:SIGN_FILES -split '\r?\n')) {
    $path = $line.Trim()
    if (-not $path) { continue }
    if ([IO.Path]::IsPathRooted($path) -or $path.Contains(':') -or ($path -split '[/\\]') -contains '..') {
        throw 'Signing paths must be relative to the workspace.'
    }
    $file = Get-Item -LiteralPath (Join-Path $workspace $path)
    if ($file -isnot [IO.FileInfo]) { throw "Expected an artifact file: $path" }
    if (-not $file.FullName.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Artifact outside the workspace: $path"
    }
    # A linked directory can redirect an otherwise workspace-relative path.
    if ($file.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Linked signing path: $path" }
    for ($dir = $file.Directory; $null -ne $dir -and $dir.FullName -ne $workspace; $dir = $dir.Parent) {
        if ($dir.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Linked signing path: $path" }
    }
    if ($null -eq $dir) { throw "Could not establish workspace ancestry for $path." }
    $file
})
if (-not $files.Count) { throw 'At least one artifact path is required.' }

$tempDir = New-Item -ItemType Directory -Path (Join-Path $env:RUNNER_TEMP ([guid]::NewGuid().ToString()))
try {
    $jar = Join-Path $tempDir.FullName 'jsign.jar'
    Invoke-WebRequest -Uri 'https://github.com/ebourg/jsign/releases/download/6.0/jsign-6.0.jar' -OutFile $jar
    if ((Get-FileHash -LiteralPath $jar -Algorithm SHA256).Hash -ne '05ca18d4ab7b8c2183289b5378d32860f0ea0f3bdab1f1b8cae5894fb225fa8a') {
        throw 'jsign checksum mismatch.'
    }
    $chain = Join-Path $tempDir.FullName 'chain.pem'
    [IO.File]::WriteAllBytes($chain, [Convert]::FromBase64String($env:CERTIFICATE_CHAIN))
    $token = & gcloud auth print-access-token
    if ($LASTEXITCODE -ne 0 -or -not $token) { throw 'Could not obtain Google Cloud access token.' }
    Write-Host "::add-mask::$token"
    $env:JSIGN_STOREPASS = $token
    foreach ($file in $files) {
        # An existing embedded or catalog signature can mask the signature jsign adds.
        $prior = Get-AuthenticodeSignature -LiteralPath $file.FullName -ErrorAction Stop
        if ($prior.Status -ne 'NotSigned' -or $null -ne $prior.SignerCertificate) {
            throw "Expected an unsigned artifact: $($file.Name)"
        }
        $options = @(
            '--storetype', $env:SIGN_STORE_TYPE, '--storepass', 'env:JSIGN_STOREPASS',
            '--keystore', $env:SIGN_KEYSTORE, '--alias', $env:SIGN_KEY_NAME,
            '--certfile', $chain, '--alg', 'SHA-256',
            '--tsmode', 'RFC3161', '--tsaurl', $env:TSA_URL,
            '--tsretries', '3', '--tsretrywait', '10',
            '--name', $env:SIGN_NAME, '--url', $env:SIGN_URL
        )
        & java -jar $jar @options $file.FullName
        if ($LASTEXITCODE -ne 0) { throw "jsign failed for $($file.Name)." }
        & (Join-Path $PSScriptRoot 'verify.ps1') -Path $file.FullName
    }
} finally {
    $env:JSIGN_STOREPASS = $null
    Remove-Item -LiteralPath $tempDir.FullName -Recurse -Force
}
