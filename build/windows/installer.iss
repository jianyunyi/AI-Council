#define MyAppName "AI Council"
#define MyAppVersion "0.1.0"
#define MyAppPublisher "AI Council"
#define MyAppExeName "AI-Council.exe"

[Setup]
AppId={{57BD3304-A69C-4F2B-A6D4-0EE3B25BBA58}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
DefaultDirName={autopf}\AI Council
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
OutputDir=..\..\dist\installer
OutputBaseFilename=AI-Council-Setup-{#MyAppVersion}
Compression=lzma2
SolidCompression=yes
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=lowest
UninstallDisplayIcon={app}\{#MyAppExeName}

[Files]
Source: "..\..\dist\AI-Council.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\council-server.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\workspace-runner.exe"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式"; GroupDescription: "附加选项："

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "启动 AI Council"; Flags: nowait postinstall skipifsilent

; User data, encrypted provider keys, and bounded logs live in
; %LOCALAPPDATA%\AI-Council and are deliberately never removed on uninstall.
