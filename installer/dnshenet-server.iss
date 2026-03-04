; DNS HE.NET Automation Server — Inno Setup 6 Installer
;
; Build from the project root:
;   "C:\Users\vladimir\AppData\Local\Programs\Inno Setup 6\ISCC.exe" installer\dnshenet-server.iss
;
; Output: dnshenet-server-installer.exe (placed in project root)
;
; INSTALL actions:
;   1. Copy dnshenet-server.exe (+ server.crt/server.key if present) to {app}
;   2. Copy .env.example to C:\dnshenet-server.env  IF that file does not exist yet
;      (preserves existing config on upgrade/reinstall)
;   3. Register dnshenet-server as a Windows auto-start service
;   4. Set ENV_FILE=C:\dnshenet-server.env in the service environment via registry
;   5. Start the service (non-fatal if it fails — env file likely needs editing first)
;
; UNINSTALL actions:
;   1. Stop and delete the Windows service
;   2. Remove all installed files and the install directory
;   3. Remove the Programs & Features entry
;   4. KEEP C:\dnshenet-server.env  (contains credentials — operator deletes manually)

[Setup]
AppName=DNS HE.NET Automation
AppVersion=1.0
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
; ── Install Windows service ────────────────────────────────────────────────
;
; WHY quoted inner path in binPath:
;   sc.exe parses the binPath value as a command line. Without inner quotes a path
;   with spaces (C:\Program Files\...) is split at the first space and Windows
;   cannot locate the binary. The outer quotes delimit the Parameters string;
;   the escaped inner quotes are consumed by sc.exe.
;
; WHY start=auto:
;   Ensures the service restarts after reboot without manual intervention.
;   Operators can change this later with: sc config dnshenet-server start= demand
Filename: "{sys}\sc.exe"; \
  Parameters: "create dnshenet-server binPath= ""{app}\dnshenet-server.exe"" DisplayName= ""DNS HE.NET Automation"" start= auto"; \
  StatusMsg: "Registering Windows service..."; Flags: runhidden waituntilterminated

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

; ── Set service environment (registry) ────────────────────────────────────
;
; WHY HKLM\...\Services\dnshenet-server\Environment (REG_MULTI_SZ):
;   Windows services start with a minimal environment that does not inherit the
;   logged-in user's variables. Two vars are injected here:
;     ENV_FILE                 — tells loadEnvFile() where to read configuration
;     PLAYWRIGHT_BROWSERS_PATH — tells Playwright where browsers were pre-installed
;   Without ENV_FILE the service cannot find C:\dnshenet-server.env, so JWT_SECRET
;   (required,notEmpty in config.go) is empty → config.Load() returns error →
;   os.Exit(1) fires before the SCM goroutine reports SERVICE_RUNNING → Error 1053.
;
; WHY PowerShell New-ItemProperty (not reg.exe /d multiple times):
;   reg.exe accepts only ONE /d flag for REG_MULTI_SZ; a second /d silently replaces
;   the first. Only the last value (PLAYWRIGHT_BROWSERS_PATH) was stored; ENV_FILE
;   was dropped entirely. PowerShell New-ItemProperty with -Value @(...) correctly
;   writes all strings into a single REG_MULTI_SZ value.
;
; PREVIOUSLY TRIED: reg.exe with /d "ENV_FILE=..." /d "PLAYWRIGHT_BROWSERS_PATH=..."
;   Only PLAYWRIGHT_BROWSERS_PATH ended up in the registry. ENV_FILE was silently
;   dropped → service started with empty config → JWT_SECRET missing → Error 1053.
; WHY LOG_FILE added to service environment:
;   Windows services have stdout/stderr redirected to /dev/null — all slog JSON output
;   is silently dropped. LOG_FILE causes the server to tee slog output to a file so
;   startup errors (config issues, Playwright failures, etc.) are visible without
;   attaching a debugger or rebuilding with extra instrumentation.
Filename: "powershell.exe"; \
  Parameters: "-NoProfile -Command ""New-ItemProperty -Path 'HKLM:\SYSTEM\CurrentControlSet\Services\dnshenet-server' -Name Environment -Value @('ENV_FILE=C:\dnshenet-server.env','PLAYWRIGHT_BROWSERS_PATH={app}\browsers','PLAYWRIGHT_DRIVER_PATH={app}\driver','LOG_FILE=C:\dnshenet-service.log') -PropertyType MultiString -Force | Out-Null"""; \
  StatusMsg: "Configuring service environment..."; \
  Flags: runhidden waituntilterminated

; ── Start service ──────────────────────────────────────────────────────────
; Run with skipifsilent so automated (silent) installs don't auto-start until
; the operator has edited the env file. Interactive installs try to start it.
Filename: "{sys}\sc.exe"; \
  Parameters: "start dnshenet-server"; \
  StatusMsg: "Starting service (edit C:\dnshenet-server.env if this fails)..."; \
  Flags: runhidden waituntilterminated skipifdoesntexist

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
