# start.ps1 - Start Polyglot Meet + Cloudflare Tunnel
$cfBin   = "C:\Users\dell\AppData\Local\cloudflared\cloudflared.exe"
$envFile = Join-Path $PSScriptRoot ".env"

if (-not (Test-Path $envFile)) {
    Write-Host "[ERROR] .env not found." -ForegroundColor Red; exit 1
}

$apiKey = (Get-Content $envFile | Where-Object { $_ -match "^GEMINI_API_KEY=" }) -replace "^GEMINI_API_KEY=",""
if (-not $apiKey -or $apiKey -eq "your_gemini_api_key_here") {
    Write-Host "[ERROR] Set GEMINI_API_KEY in .env" -ForegroundColor Red; exit 1
}

Write-Host "[1/3] Starting Docker services..." -ForegroundColor Cyan
docker compose --env-file $envFile up --build -d
if ($LASTEXITCODE -ne 0) { Write-Host "[ERROR] Docker failed." -ForegroundColor Red; exit 1 }
Write-Host "[OK] Backend running on http://localhost:8080" -ForegroundColor Green

if (-not (Test-Path $cfBin)) {
    Write-Host "[ERROR] cloudflared not found at $cfBin" -ForegroundColor Red; exit 1
}

Write-Host "[2/3] Starting Cloudflare Tunnel..." -ForegroundColor Cyan
Get-Process -Name cloudflared -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 500

$logOut = [System.IO.Path]::GetTempFileName()
$logErr = [System.IO.Path]::GetTempFileName()
$proc = Start-Process -FilePath $cfBin `
    -ArgumentList "tunnel","--url","http://localhost:8080","--no-autoupdate" `
    -RedirectStandardOutput $logOut `
    -RedirectStandardError  $logErr `
    -PassThru -WindowStyle Hidden

Write-Host "[3/3] Waiting for public URL..." -ForegroundColor Cyan
$url = $null
for ($i = 0; $i -lt 30 -and -not $url; $i++) {
    Start-Sleep -Milliseconds 800
    # cloudflared prints the URL to stderr
    foreach ($f in @($logOut, $logErr)) {
        if (Test-Path $f) {
            $content = Get-Content $f -Raw -ErrorAction SilentlyContinue
            if ($content -match 'https://[a-z0-9\-]+\.trycloudflare\.com') {
                $url = $Matches[0]; break
            }
        }
    }
}

if ($url) {
    $url | Set-Clipboard
    Write-Host ""
    Write-Host "============================================" -ForegroundColor Cyan
    Write-Host "  POLYGLOT MEET IS LIVE" -ForegroundColor Green
    Write-Host "============================================" -ForegroundColor Cyan
    Write-Host "  Public URL : $url" -ForegroundColor Yellow
    Write-Host "  Local URL  : http://localhost:8080" -ForegroundColor Gray
    Write-Host "  (URL copied to clipboard - share it!)" -ForegroundColor Green
    Write-Host "============================================" -ForegroundColor Cyan
    Write-Host ""
} else {
    Write-Host "[WARN] Could not get URL. Check log: $logFile" -ForegroundColor Yellow
}

Write-Host "Streaming logs (CTRL+C to stop)..." -ForegroundColor Gray
try {
    docker compose logs -f --tail=50
} finally {
    $proc | Stop-Process -Force -ErrorAction SilentlyContinue
    Remove-Item $logOut,$logErr -ErrorAction SilentlyContinue
    Write-Host "Stopped." -ForegroundColor Gray
}
