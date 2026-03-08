; DNS HE.NET Automation Server — Inno Setup 6 Installer
;
; Build from the project root:
;   "C:\Users\vladimir\AppData\Local\Programs\Inno Setup 6\ISCC.exe" installer\dnshenet-server.iss
;
; Output: dnshenet-server-installer.exe (placed in project root)
;
; INSTALL actions:
;   1. Copy dnshenet-server.exe (+ server.crt/server.key if present) to {app}
;   2. Generate self-signed TLS cert at {app}\server.crt / server.key (skipped if present)
;   3. Register dnshenet-server as a Windows auto-start service
;   4. Install Playwright/Chromium browser binaries to {app}\browsers
;   5. Write service environment to registry (ENV_FILE, PLAYWRIGHT_BROWSERS_PATH, etc.)
;   6. Generate a random JWT_SECRET and patch it into C:\dnshenet-server.env
;   7. Start the service — https://localhost:9001/admin accessible immediately
;
;   C:\dnshenet-server.env is written on step 6 (from .env.example template) only when
;   the file does not already exist — preserves existing config on upgrade/reinstall.
;
; UNINSTALL actions:
;   1. Stop and delete the Windows service
;   2. Remove all installed files and the install directory
;   3. Remove the Programs & Features entry
;   4. KEEP C:\dnshenet-server.env  (contains credentials — operator deletes manually)

; WHY injectable AppVersion via ISCC define (not hardcoded):
;   CI passes the resolved semver via: ISCC.exe /DMyAppVersion=1.2.3 ...
;   This embeds the version string in Programs & Features and the installer
;   filename without requiring manual edits to this file before each release.
;   Local / dev builds that don't pass /DMyAppVersion get "dev" as a safe default.
;
; DEPENDENCY: the CI job in .gitlab-ci.yml must pass /DMyAppVersion=$VERSION
;   to ISCC.exe. If that flag is omitted, the installer shows "dev" as its version.
#ifndef MyAppVersion
  #define MyAppVersion "dev"
#endif

[Setup]
AppName=DNS HE.NET Automation
AppVersion={#MyAppVersion}
AppPublisher=dns-he-net-automation
AppId={{A1B2C3D4-E5F6-7890-ABCD-EF1234567890}
DefaultDirName={autopf}\dnshenet-server
DefaultGroupName=DNS HE.NET Automation
OutputBaseFilename=dnshenet-server-installer
OutputDir=..
Compression=lzma2
SolidCompression=yes
; Require admin rights — needed for service install and HKLM registry writes
PrivilegesRequired=admin
; Target 64-bit Windows; {sys} resolves to the real System32 (not SysWOW64)
ArchitecturesInstallIn64BitMode=x64compatible
; Show uninstall entry in Programs & Features
UninstallDisplayName=DNS HE.NET Automation
UninstallDisplayIcon={app}\dnshenet-server.exe
MinVersion=10.0

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Files]
; Main executable
Source: "..\dnshenet-server.exe"; DestDir: "{app}"; Flags: ignoreversion

; TLS certificate and private key — included if present in the project root.
; skipifsourcedoesntexist: build does not fail when certs are managed separately.
Source: "..\server.crt"; DestDir: "{app}"; Flags: ignoreversion skipifsourcedoesntexist
Source: "..\server.key"; DestDir: "{app}"; Flags: ignoreversion skipifsourcedoesntexist

; .env template — extracted to {tmp} during install; Pascal code below copies it
; to C:\dnshenet-server.env only when the file does not yet exist.
; deleteafterinstall: cleaned up from {tmp} automatically when the installer exits.
Source: "..\.env.example"; DestDir: "{tmp}"; Flags: deleteafterinstall

[Run]
; ── Generate self-signed TLS certificate (if not already present) ──────────
;
; WHY generate cert here (not bundle from source):
;   server.crt and server.key are gitignored — CI builds never have them.
;   Bundling certs from the dev machine would be wrong anyway (each install
;   should have its own key pair). Generating at install time ensures the
;   cert exists at the path .env.example expects, so the service starts in
;   HTTPS mode out of the box.
;
; WHY run gen-cert directly (not via PowerShell wrapper):
;   The server binary gen-cert subcommand is self-contained — no openssl or
;   PowerShell required. Calling it directly from [Run] is simpler and avoids
;   PowerShell quoting edge cases. The installer already runs as Administrator
;   so the binary can write to {app} without elevation tricks.
;
; WHY Check: not FileExists:
;   Reinstall or upgrade must not overwrite existing certs — that would
;   invalidate any client trust anchors or browser exceptions the operator
;   has already accepted. The Check condition skips this entry entirely
;   when server.crt is already present.
;
; WHY the server binary gen-cert subcommand (not openssl):
;   openssl is not installed by default on Windows. The server binary already
;   links crypto/tls and generates a self-signed ECDSA-P256 cert with SAN
;   for localhost and 127.0.0.1 using only the Go standard library.
;
; PREVIOUSLY TRIED: bundling certs from project root with skipifsourcedoesntexist.
;   Worked for local builds (certs present) but broke CI builds (certs gitignored)
;   → service started with SSL_CERT/SSL_KEY pointing to nonexistent files
;   → ListenAndServeTLS failed immediately → Error 1067.
;
; PREVIOUSLY TRIED: PowerShell wrapper with Test-Path check.
;   Worked but was unnecessarily complex — PowerShell quoting inside Inno Setup
;   Parameters strings is error-prone. Direct binary call is simpler and equivalent.
Filename: "{app}\dnshenet-server.exe"; \
  Parameters: "gen-cert --cert ""{app}\server.crt"" --key ""{app}\server.key"""; \
  WorkingDir: "{app}"; \
  StatusMsg: "Generating self-signed TLS certificate..."; \
  Flags: waituntilterminated; \
  Check: not FileExists(ExpandConstant('{app}\server.crt'))

; ── Install Windows service ────────────────────────────────────────────────
;
; WHY quoted inner path in binPath:
;   sc.exe parses the binPath value as a command line. Without inner quotes a path
;   with spaces (C:\Program Files\...) is split at the first space and Windows
;   cannot locate the binary. The outer quotes delimit the Parameters string;
;   the escaped inner quotes are consumed by sc.exe.
;
; WHY sc create followed by sc config (not sc create alone):
;   sc create fails with exit code 1073 ("service already exists") on reinstall
;   or when a previous sc delete left the service in pending-delete state (open
;   handle from Services.msc or another process). ISS does not fail the install
;   on non-zero exit codes — it silently continues. The existing service retains
;   whatever start type it had, which may be DISABLED after repeated SCM failures.
;   sc config runs unconditionally after sc create and enforces start= auto
;   regardless of whether sc create succeeded or found an existing service.
;   PREVIOUSLY: sc create alone left start type DISABLED on reinstall.
Filename: "{sys}\sc.exe"; \
  Parameters: "create dnshenet-server binPath= ""{app}\dnshenet-server.exe"" DisplayName= ""DNS HE.NET Automation"" start= auto"; \
  StatusMsg: "Registering Windows service..."; Flags: runhidden waituntilterminated

Filename: "{sys}\sc.exe"; \
  Parameters: "config dnshenet-server start= auto"; \
  StatusMsg: "Setting service start type to automatic..."; Flags: runhidden waituntilterminated

Filename: "{sys}\sc.exe"; \
  Parameters: "description dnshenet-server ""Automates dns.he.net DNS management. Exposes REST API on HTTPS."""; \
  Flags: runhidden waituntilterminated

; ── Pre-install Playwright browser binaries ───────────────────────────────
;
; WHY required for Windows service:
;   playwright.Run() (called inside browser.NewLauncher at service start) downloads
;   browsers on first run to %LOCALAPPDATA%\ms-playwright. When running as LocalSystem
;   the service account has a different LOCALAPPDATA than the installing user, so the
;   browsers are not found. This causes a 180-second hang and SCM Error 1053.
;
; WHY PLAYWRIGHT_BROWSERS_PATH = {app}\browsers:
;   A fixed path inside the install dir is accessible to LocalSystem (read access
;   to Program Files is granted by default) and survives user profile changes.
;
; WHY playwright-install subcommand (not auto-download at service start):
;   Running download at install time avoids any SCM timeout and gives the operator
;   clear feedback if the download fails. The subcommand calls playwright.Install()
;   which respects PLAYWRIGHT_BROWSERS_PATH.
;
; WHY PowerShell wrapper (not Filename: "{app}\dnshenet-server.exe" directly):
;   PLAYWRIGHT_BROWSERS_PATH must be set in the environment BEFORE playwright.Install()
;   is called so browsers land in {app}\browsers (accessible to LocalSystem service).
;   Inno Setup [Run] has no per-entry env-var injection; PowerShell sets the variable
;   in the child process before exec-ing the binary.
;
; PREVIOUSLY TRIED: running dnshenet-server.exe directly without env var set.
;   Playwright installed to %LOCALAPPDATA%\ms-playwright of the installing user.
;   The service (LocalSystem) has a different LOCALAPPDATA — browsers not found at
;   service start → 180-second timeout → SCM Error 1053 on first service start.
; WHY PLAYWRIGHT_DRIVER_PATH set here alongside PLAYWRIGHT_BROWSERS_PATH:
;   playwright-go stores the driver binary in os.UserCacheDir()/ms-playwright-go/<ver>.
;   When the installer runs as the admin user that path is the admin's %LOCALAPPDATA%.
;   The service runs as LocalSystem whose %LOCALAPPDATA% is
;   C:\Windows\System32\config\systemprofile\AppData\Local — a different directory.
;   Passing PLAYWRIGHT_DRIVER_PATH={app}\driver forces both the installer (here) and
;   the service (via registry env below) to use the same fixed path, so the driver
;   installed during setup is found at service start regardless of the user account.
; PREVIOUSLY TRIED: installing without PLAYWRIGHT_DRIVER_PATH set.
;   Driver landed in admin user's AppData; service (LocalSystem) looked elsewhere →
;   "please install the driver (v1.57.0) first" → os.Exit(1) → SCM Error 1053.
Filename: "powershell.exe"; \
  Parameters: "-NoProfile -Command ""$env:PLAYWRIGHT_BROWSERS_PATH='{app}\browsers'; $env:PLAYWRIGHT_DRIVER_PATH='{app}\driver'; & '{app}\dnshenet-server.exe' playwright-install"""; \
  WorkingDir: "{app}"; \
  StatusMsg: "Installing Playwright browser binaries (downloading ~200 MB, please wait)..."; \
  Flags: waituntilterminated

; ── Set service environment variables in registry ─────────────────────────
;
; WHY HKLM\...\Services\dnshenet-server\Environment (REG_MULTI_SZ):
;   Windows services start with a minimal environment that does not inherit the
;   logged-in user's variables. ENV_FILE is injected here so loadEnvFile() finds
;   C:\dnshenet-server.env regardless of the service working directory. Without it
;   the fallback is `.env` in %SystemRoot%\System32 — which never exists — so
;   JWT_SECRET is empty, config.Load() fails, and SCM reports Error 1053.
;
; WHY PowerShell New-ItemProperty (not reg.exe /d multiple times):
;   reg.exe accepts only ONE /d flag for REG_MULTI_SZ; a second /d silently replaces
;   the first. Only the last value (PLAYWRIGHT_BROWSERS_PATH) was stored; ENV_FILE
;   was dropped entirely. PowerShell New-ItemProperty with -Value @(...) correctly
;   writes all strings into a single REG_MULTI_SZ value.
;
; WHY TWO SEPARATE PowerShell steps (registry write + JWT patch, not merged):
;   A single merged step failed silently: when Set-Content threw a terminating error
;   (file not yet created, or access timing issue in ssPostInstall ordering),
;   PowerShell stopped mid-command before New-ItemProperty ran. runhidden suppressed
;   all output so the failure was invisible. The registry key was never written.
;   After uninstall+reinstall the previous working registry key was gone; the new
;   install never replaced it → even manual sc start failed (no ENV_FILE in registry).
;
;   Separation makes each step independently debuggable: if the registry step fails
;   the service is still broken but the JWT step is unaffected and vice versa.
;
; PREVIOUSLY TRIED: merged JWT+registry into one PowerShell -Command invocation.
;   Silent mid-command failure left registry unwritten. Diagnosed by reading the
;   installer log (C:\Users\<user>\AppData\Local\Temp\Setup Log*.txt) after install.
;
; PREVIOUSLY TRIED: reg.exe with /d "ENV_FILE=..." /d "PLAYWRIGHT_BROWSERS_PATH=..."
;   Only PLAYWRIGHT_BROWSERS_PATH ended up in the registry (last /d wins in REG_MULTI_SZ).
;   ENV_FILE was silently dropped → JWT_SECRET always empty → Error 1053.
;
; WHY LOG_FILE added to service environment:
;   Windows services redirect stdout/stderr to /dev/null — all slog JSON output is
;   silently dropped. LOG_FILE causes the server to tee slog output to a file so
;   startup errors (config issues, Playwright failures) are visible without a debugger.
; WHY JWT_SECRET=placeholder written here (not only in the JWT step below):
;   The JWT step generates a real random secret and patches both the env file and
;   this registry key. But if the JWT step fails for any reason (RNG, file write,
;   PowerShell parse error), the service process would have no JWT_SECRET at all —
;   the required+notEmpty validation fails and the service exits immediately.
;   Writing the 34-char placeholder here ensures the service can always start even
;   if the JWT step never runs. The JWT step then overwrites this with the real secret.
;   Belt-and-suspenders: JWT_SECRET comes from the registry even if godotenv fails
;   to parse the env file (BOM, encoding, permission issues).
Filename: "powershell.exe"; \
  Parameters: "-NoProfile -NonInteractive -Command ""New-ItemProperty -Path 'HKLM:\SYSTEM\CurrentControlSet\Services\dnshenet-server' -Name Environment -Value @('ENV_FILE=C:\dnshenet-server.env', 'JWT_SECRET=REPLACE_WITH_RANDOM_32_CHAR_SECRET', 'PLAYWRIGHT_BROWSERS_PATH={app}\browsers', 'PLAYWRIGHT_DRIVER_PATH={app}\driver', 'LOG_FILE=C:\dnshenet-service.log') -PropertyType MultiString -Force | Out-Null"""; \
  StatusMsg: "Configuring service environment variables..."; \
  Flags: runhidden waituntilterminated

; ── Generate JWT secret and patch env file ────────────────────────────────
;
; WHY separate from the registry step above:
;   See "WHY TWO SEPARATE PowerShell steps" comment above. Keeping JWT generation
;   isolated means a failure here (e.g. -replace finds no match) does not also
;   destroy the registry write. The service still starts — with the 34-char
;   placeholder as JWT_SECRET (passes notEmpty validation) — and the operator
;   is reminded to re-run the installer or set a real secret manually.
;
; WHY RNGCryptoServiceProvider (not RandomNumberGenerator::Fill or Get-Random):
;   - Get-Random: seeded from clock, not cryptographically secure.
;   - RandomNumberGenerator::Fill(): .NET 6+ only. Windows PowerShell 5.1 uses
;     .NET Framework 4.x — Fill() throws MethodNotFound silently, $bytes stays zero.
;   - RNGCryptoServiceProvider: available since .NET Framework 1.1 / PowerShell 2.0.
;     GetBytes() calls CryptGenRandom (Windows CSPRNG) — same source as openssl rand.
;   PREVIOUSLY TRIED: RandomNumberGenerator::Fill() — silently produced 64 zeros on
;     Windows 11 / PowerShell 5.1; placeholder JWT_SECRET was never replaced.
;
; WHY {{ and }} (double braces) in the Parameters string:
;   ISS preprocessor treats every { as a potential constant reference and errors with
;   "Unknown constant" if unrecognised. Double braces escape to literal { / }.
;   Two spots require escaping: ForEach-Object {{ ... }} and the format string '{{0:x2}}'.
;   PREVIOUSLY TRIED: single braces → ISS compile errors "Unknown constant '{0:x2}'"
;   and "Unknown constant '{{0:x2'" (the scriptblock brace was also misidentified).
Filename: "powershell.exe"; \
  Parameters: "-NoProfile -NonInteractive -Command ""$rng = New-Object System.Security.Cryptography.RNGCryptoServiceProvider; $bytes = New-Object byte[] 32; $rng.GetBytes($bytes); $secret = ($bytes | ForEach-Object {{ '{{0:x2}}' -f $_ }}) -join ''; $content = Get-Content 'C:\dnshenet-server.env' -Encoding UTF8; $patched = $content -replace 'REPLACE_WITH_RANDOM_32_CHAR_SECRET', $secret; $patched | Set-Content 'C:\dnshenet-server.env' -Encoding UTF8; New-ItemProperty -Path 'HKLM:\SYSTEM\CurrentControlSet\Services\dnshenet-server' -Name Environment -Value @('ENV_FILE=C:\dnshenet-server.env', ('JWT_SECRET=' + $secret), 'PLAYWRIGHT_BROWSERS_PATH={app}\browsers', 'PLAYWRIGHT_DRIVER_PATH={app}\driver', 'LOG_FILE=C:\dnshenet-service.log') -PropertyType MultiString -Force | Out-Null"""; \
  StatusMsg: "Generating JWT secret..."; \
  Flags: runhidden waituntilterminated

; ── Start service ──────────────────────────────────────────────────────────
; JWT_SECRET is now a valid random value — service can start immediately.
Filename: "{sys}\sc.exe"; \
  Parameters: "start dnshenet-server"; \
  StatusMsg: "Starting service..."; \
  Flags: runhidden waituntilterminated

[UninstallRun]
; Stop and delete handled in Pascal code (CurUninstallStepChanged) below,
; which adds a sleep between stop and delete and handles errors gracefully.

[Code]
// ── Install: copy .env.example → C:\dnshenet-server.env (if absent) ────────
//
// WHY Pascal code instead of a [Files] entry:
//   Inno Setup's [Files] section always overwrites the destination. We need
//   conditional copy: skip if C:\dnshenet-server.env already exists so that
//   upgrade/reinstall does not overwrite the operator's credentials.
//
// WHY C:\dnshenet-server.env (not inside {app}):
//   Keeping config outside the install dir means:
//     - Uninstall removes the binary but never silently deletes credentials
//     - Upgrade (re-running the installer) preserves custom configuration
//     - The file survives a full uninstall + reinstall cycle intact
procedure CurStepChanged(CurStep: TSetupStep);
var
  EnvFileDest: string;
  EnvFileSrc:  string;
begin
  if CurStep = ssPostInstall then
  begin
    EnvFileDest := 'C:\dnshenet-server.env';
    EnvFileSrc  := ExpandConstant('{tmp}') + '\.env.example';

    if not FileExists(EnvFileDest) then
    begin
      if CopyFile(EnvFileSrc, EnvFileDest, False) then
        Log('Created ' + EnvFileDest + ' from template.')
      else
        MsgBox(
          'Could not create ' + EnvFileDest + '.' + #13#10 +
          'Copy .env.example from the project and edit it before starting the service.',
          mbError, MB_OK);
    end
    else
      Log(EnvFileDest + ' already exists — keeping existing configuration.');
  end;
end;

// ── Uninstall: stop service, ask about DB, delete service + browsers ─────────
//
// WHY Sleep between stop and delete:
//   sc stop is asynchronous — the service process needs time to finish its
//   30-second graceful drain (browser session cleanup, DB close). Calling
//   sc delete immediately after sc stop can leave the binary locked,
//   causing "Access Denied" when the installer tries to delete the .exe.
//   5 seconds covers the typical drain; the SCM will force-kill after its own
//   timeout if the service has not stopped by then.
//
// WHY the database question is here (usUninstall) and not usPostUninstall:
//   usPostUninstall fires AFTER {app} files are already removed. Asking at that
//   point is confusing ("uninstall complete... also do you want to delete X?").
//   Asking here — after the service is stopped but before the binary is removed —
//   gives the operator a clear choice while the context is still obvious.
//
// WHY the database survives reinstall automatically (no install-side check needed):
//   {app}\dnshenet-server.db is NOT listed in [Files], so the installer never
//   tracks it and never overwrites it. Running the installer again over an existing
//   installation only replaces the binary and TLS certs — data is untouched.
//   Only an explicit "No" answer here causes the database to be deleted.
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ResultCode: Integer;
  DBPath:     string;
begin
  if CurUninstallStep = usUninstall then
  begin
    // Stop the service and wait for it to terminate
    Exec(ExpandConstant('{sys}\sc.exe'), 'stop dnshenet-server',
         '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    Log('sc stop returned ' + IntToStr(ResultCode));

    // Give the service time to drain in-flight operations
    Sleep(5000);

    // Delete the service registration
    Exec(ExpandConstant('{sys}\sc.exe'), 'delete dnshenet-server',
         '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    Log('sc delete returned ' + IntToStr(ResultCode));

    // ── Ask operator whether to keep the database ───────────────────────────
    // The database is not in [Files] so Inno Setup never removes it automatically.
    // Default (Yes / closing the dialog) = keep, so a misclick never destroys data.
    DBPath := ExpandConstant('{app}') + '\dnshenet-server.db';
    if FileExists(DBPath) then
    begin
      if MsgBox(
           'Keep the database?' + #13#10 + #13#10 +
           DBPath + #13#10 + #13#10 +
           'The database stores accounts, tokens, and audit logs.' + #13#10 +
           'Keeping it lets the next installation pick up existing data' + #13#10 +
           'without any additional configuration.' + #13#10 + #13#10 +
           'Yes = keep (recommended)     No = delete permanently',
           mbConfirmation, MB_YESNO) = IDNO then
      begin
        DeleteFile(DBPath);
        Log('Database deleted by operator choice: ' + DBPath);
      end
      else
        Log('Database kept by operator choice: ' + DBPath);
    end;

    // Remove the pre-installed Playwright browser binaries.
    // WHY DelTree here (not a [Files] uninstall entry):
    //   The browsers\ directory is populated by `dnshenet-server.exe playwright-install`
    //   during [Run], not by a [Files] entry, so Inno Setup's automatic uninstall does
    //   not know about it. DelTree removes it explicitly.
    //   True, True, True = delete subdirs, delete files, delete root dir itself.
    DelTree(ExpandConstant('{app}\browsers'), True, True, True);
    Log('Removed browsers directory.');
  end;

  // After all files are removed, remind the operator about preserved files.
  if CurUninstallStep = usPostUninstall then
  begin
    MsgBox(
      'Uninstall complete.' + #13#10 + #13#10 +
      'C:\dnshenet-server.env was intentionally preserved.' + #13#10 +
      'It contains your credentials and configuration.' + #13#10 +
      'Delete it manually when it is no longer needed.',
      mbInformation, MB_OK);
  end;
end;
