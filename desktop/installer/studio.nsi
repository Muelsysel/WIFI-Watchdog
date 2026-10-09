; WiFi Watchdog Studio: per-user installation, no UAC, no services, no drivers.
; Compiled from the self-contained Windows x64 publish directory.
Unicode true
!include "MUI2.nsh"
!ifndef STUDIO_SOURCE
  !error "Pass /DSTUDIO_SOURCE=<absolute publish folder>"
!endif
!ifndef STUDIO_OUTPUT
  !error "Pass /DSTUDIO_OUTPUT=<absolute setup exe>"
!endif
!ifndef STUDIO_VERSION
  !define STUDIO_VERSION "0.3.0-preview"
!endif

Name "WiFi Watchdog Studio"
OutFile "${STUDIO_OUTPUT}"
InstallDir "$LOCALAPPDATA\Programs\WiFiWatchdogStudio"
RequestExecutionLevel user
SetCompressor /SOLID lzma
ShowInstDetails show
ShowUninstDetails show
BrandingText "WiFi Watchdog Studio · ${STUDIO_VERSION}"

!define MUI_ABORTWARNING
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

Section "Studio" SecMain
  SetShellVarContext current
  SetOutPath "$INSTDIR"
  File /r "${STUDIO_SOURCE}\*.*"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  CreateDirectory "$SMPROGRAMS"
  CreateShortcut "$SMPROGRAMS\WiFi Watchdog Studio.lnk" "$INSTDIR\WiFiWatchdog.Studio.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\WiFiWatchdogStudio" "DisplayName" "WiFi Watchdog Studio"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\WiFiWatchdogStudio" "DisplayVersion" "${STUDIO_VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\WiFiWatchdogStudio" "Publisher" "WiFi Watchdog"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\WiFiWatchdogStudio" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\WiFiWatchdogStudio" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\WiFiWatchdogStudio" "QuietUninstallString" '"$INSTDIR\Uninstall.exe" /S'
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\WiFiWatchdogStudio" "NoModify" 1
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\WiFiWatchdogStudio" "NoRepair" 1
SectionEnd

Section "Uninstall"
  SetShellVarContext current
  Delete "$SMPROGRAMS\WiFi Watchdog Studio.lnk"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\WiFiWatchdogStudio"
  ; The installation folder contains only Studio application binaries.
  ; NEVER delete %LOCALAPPDATA%\WiFiWatchdog: user config/logs/history
  ; and the independently-installed Go portable engine live there.
  RMDir /r "$INSTDIR"
SectionEnd
