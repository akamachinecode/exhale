; Exhale-Setup.exe (ADR 0029, 0030): a one-click installer for the person
; using Windows, no administrator needed. It copies Exhale.exe, adds Exhale
; to the Start menu and to Settings → Apps, and opens it; Exhale.exe then
; starts the server, makes it start at sign-in and shows its window. Built
; by build.sh.
Unicode true
SetCompressor /SOLID lzma

!ifndef VERSION
  !define VERSION "0.0.0"
!endif
!ifndef WINVERSION
  !define WINVERSION "0.0.0.0"
!endif
!ifndef OUTFILE
  !define OUTFILE "Exhale-Setup.exe"
!endif
!define UNINST "Software\Microsoft\Windows\CurrentVersion\Uninstall\Exhale"
!define RUN "Software\Microsoft\Windows\CurrentVersion\Run"

!include "MUI2.nsh"
!include "LogicLib.nsh"

Name "Exhale"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\Exhale"
RequestExecutionLevel user
BrandingText "Exhale ${VERSION}"
AutoCloseWindow true
ShowInstDetails nevershow
ShowUninstDetails nevershow

VIProductVersion "${WINVERSION}"
VIAddVersionKey /LANG=0 "ProductName" "Exhale"
VIAddVersionKey /LANG=0 "FileDescription" "Exhale installer"
VIAddVersionKey /LANG=0 "ProductVersion" "${VERSION}"
VIAddVersionKey /LANG=0 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=0 "LegalCopyright" "AGPL-3.0-or-later"

!define MUI_ICON "exhale.ico"
!define MUI_UNICON "exhale.ico"
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "Spanish"

Section
  SetShellVarContext current
  ; A running Exhale holds its files open: an upgrade stops it first. The
  ; database is safe: SQLite survives a stop at any moment.
  nsExec::Exec '"$SYSDIR\taskkill.exe" /F /IM Exhale.exe'
  Pop $0
  Sleep 500

  SetOutPath "$INSTDIR"
  File "Exhale.exe"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  CreateShortcut "$SMPROGRAMS\Exhale.lnk" "$INSTDIR\Exhale.exe"

  WriteRegStr HKCU "${UNINST}" "DisplayName" "Exhale"
  WriteRegStr HKCU "${UNINST}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINST}" "Publisher" "Exhale"
  WriteRegStr HKCU "${UNINST}" "URLInfoAbout" "https://github.com/akamachinecode/exhale"
  WriteRegStr HKCU "${UNINST}" "DisplayIcon" "$INSTDIR\Exhale.exe"
  WriteRegStr HKCU "${UNINST}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINST}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKCU "${UNINST}" "QuietUninstallString" '"$INSTDIR\Uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINST}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST}" "NoRepair" 1

  ; Open Exhale: it starts the server, adds itself to sign-in and shows its
  ; window. A silent install (/S) leaves that to the next sign-in.
  ${IfNot} ${Silent}
    Exec '"$INSTDIR\Exhale.exe"'
  ${EndIf}
SectionEnd

Section "Uninstall"
  SetShellVarContext current
  nsExec::Exec '"$SYSDIR\taskkill.exe" /F /IM Exhale.exe'
  Pop $0
  Sleep 500
  DeleteRegValue HKCU "${RUN}" "Exhale"
  Delete "$SMPROGRAMS\Exhale.lnk"
  Delete "$INSTDIR\Exhale.exe"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKCU "${UNINST}"
  ; The data stays in %LOCALAPPDATA%\Exhale: delete that folder to remove it.
SectionEnd
