; StreamGremlin's Windows installer (D-084), built by `make release` with
; NSIS 3 (makensis), which runs on macOS and Linux too:
;
;   makensis -DVERSION=0.1.0-beta.1 -DWINVERSION=0.1.0.1 -DSRC=<staged files> -DOUT=<setup.exe> StreamGremlin.nsi
;
; It installs for the current user only, with no administrator prompt, to
; %LocalAppData%\Programs\StreamGremlin. The streamer's data folder,
; %LocalAppData%\StreamGremlin, is never touched: the uninstaller removes
; exactly the files it installed. Unsigned during the beta (D-084).
;
; StreamGremlin installs its own updates (D-120) by running this with
; /S /update: silently, it waits for the app to close, installs, keeps a
; desktop shortcut only if there was one, and opens the app again.

Unicode true
ManifestDPIAware true
RequestExecutionLevel user
SetCompressor /SOLID lzma

!ifndef VERSION | WINVERSION | SRC | OUT
  !error "Define VERSION, WINVERSION, SRC and OUT."
!endif

!define APP "StreamGremlin"
!define EXE "StreamGremlin.exe"
!define UNINSTALLER "Uninstall StreamGremlin.exe"
!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\StreamGremlin"
; Microsoft's documented registry key for the WebView2 Runtime.
!define WEBVIEW2_KEY "Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"

!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "x64.nsh"
!include "nsDialogs.nsh"
!include "FileFunc.nsh"

Name "${APP}"
OutFile "${OUT}"
InstallDir "$LOCALAPPDATA\Programs\${APP}"
BrandingText "${APP} ${VERSION}"
ShowInstDetails show
ShowUninstDetails show

VIProductVersion "${WINVERSION}"
VIFileVersion "${WINVERSION}"
VIAddVersionKey "ProductName" "${APP}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "FileDescription" "${APP} ${VERSION} installer"
VIAddVersionKey "CompanyName" "${APP}"
VIAddVersionKey "LegalCopyright" "Copyright 2026 the StreamGremlin authors. Apache License 2.0."

!define MUI_ICON "${SRC}\StreamGremlin.ico"
!define MUI_UNICON "${SRC}\StreamGremlin.ico"
!define MUI_ABORTWARNING
!define MUI_COMPONENTSPAGE_NODESC
!define MUI_WELCOMEPAGE_TITLE "Install ${APP} ${VERSION}"
!define MUI_WELCOMEPAGE_TEXT "${APP} is stream automation that runs on your own computer.$\r$\n$\r$\nThis is a beta. It installs for you only, in your user folder, and doesn't need an administrator.$\r$\n$\r$\nIf StreamGremlin is open, close it first. Installing a newer version over an older one keeps your workflows, settings and sign-ins."
!define MUI_FINISHPAGE_RUN "$INSTDIR\${EXE}"
!define MUI_FINISHPAGE_RUN_TEXT "Open ${APP}"
!define MUI_FINISHPAGE_SHOWREADME "$INSTDIR\Read me first.txt"
!define MUI_FINISHPAGE_SHOWREADME_TEXT "Show the beta guide"
!define MUI_FINISHPAGE_SHOWREADME_NOTCHECKED
; A third box, "Install the Stream Deck plugin", added by FinishShow.
!define MUI_PAGE_CUSTOMFUNCTION_SHOW FinishShow
!define MUI_PAGE_CUSTOMFUNCTION_LEAVE FinishLeave

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Var DeckBox
; 1 when StreamGremlin runs this to install an update of itself.
Var Updating

; Modern UI's finish page has room for two boxes of its own; this one goes
; 20 units below the second (110u), as Modern UI spaces its own. Unticked:
; not everyone has a Stream Deck.
Function FinishShow
  ${NSD_CreateCheckbox} 120u 130u 195u 10u "Install the Stream Deck plugin"
  Pop $DeckBox
  SetCtlColors $DeckBox "${MUI_TEXTCOLOR}" "${MUI_BGCOLOR}"
  ; As Modern UI does for its own boxes: in high contrast, the text colour
  ; only shows without the theme.
  ${If} ${IsHighContrastModeActive}
    System::Call 'UXTHEME::SetWindowTheme(p$DeckBox,w" ",w" ")'
  ${EndIf}
FunctionEnd

; Opening the plugin file hands it to Stream Deck, which asks to install it.
Function FinishLeave
  ${NSD_GetState} $DeckBox $0
  ${If} $0 == ${BST_CHECKED}
    ClearErrors
    ExecShell "open" "$INSTDIR\Stream Deck\com.streamgremlin.streamdeck.streamDeckPlugin"
    ${If} ${Errors}
      MessageBox MB_OK|MB_ICONINFORMATION "Stream Deck didn't open the plugin. Install Stream Deck 7.1 or later from Elgato, then double-click com.streamgremlin.streamdeck.streamDeckPlugin in $INSTDIR\Stream Deck."
    ${EndIf}
  ${EndIf}
FunctionEnd

Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_OK|MB_ICONSTOP "${APP} needs 64-bit Windows 10 or 11."
    Abort
  ${EndIf}
  StrCpy $Updating 0
  ${GetParameters} $R0
  ClearErrors
  ${GetOptions} $R0 "/update" $R1
  ${IfNot} ${Errors}
    StrCpy $Updating 1
    Call UpdateMode
  ${EndIf}
FunctionEnd

; Reopens StreamGremlin after an update it started.
Function .onInstSuccess
  ${If} $Updating == 1
    Exec '"$INSTDIR\${EXE}"'
  ${EndIf}
FunctionEnd

; A running StreamGremlin (or a Lua script it started) holds its .exe open,
; so it can't be replaced or removed until it's closed.
Function CloseApp
  StrCpy $R2 0
  retry:
  ${If} ${FileExists} "$INSTDIR\${EXE}"
    ClearErrors
    FileOpen $0 "$INSTDIR\${EXE}" a
    ${If} ${Errors}
      ; An update: the app is closing itself, so wait up to a minute.
      ${If} $Updating == 1
      ${AndIf} $R2 < 120
        IntOp $R2 $R2 + 1
        Sleep 500
        Goto retry
      ${EndIf}
      MessageBox MB_RETRYCANCEL|MB_ICONEXCLAMATION "${APP} is open. Close it, wait a few seconds, then choose Retry." IDRETRY retry
      Abort
    ${EndIf}
    FileClose $0
  ${EndIf}
FunctionEnd

Function un.CloseApp
  retry:
  ${If} ${FileExists} "$INSTDIR\${EXE}"
    ClearErrors
    FileOpen $0 "$INSTDIR\${EXE}" a
    ${If} ${Errors}
      MessageBox MB_RETRYCANCEL|MB_ICONEXCLAMATION "${APP} is open. Close it, wait a few seconds, then choose Retry." IDRETRY retry
      Abort
    ${EndIf}
    FileClose $0
  ${EndIf}
FunctionEnd

; The app's window is Microsoft Edge WebView2, part of Windows 10 and 11.
Function CheckWebView2
  SetRegView 64
  ReadRegStr $1 HKLM "SOFTWARE\WOW6432Node\${WEBVIEW2_KEY}" "pv"
  ${If} $1 == ""
  ${OrIf} $1 == "0.0.0.0"
    ReadRegStr $1 HKCU "Software\${WEBVIEW2_KEY}" "pv"
  ${EndIf}
  SetRegView 32
  ${If} $1 == ""
  ${OrIf} $1 == "0.0.0.0"
    MessageBox MB_YESNO|MB_ICONINFORMATION "${APP}'s window needs Microsoft Edge WebView2, which this computer doesn't seem to have. Open Microsoft's download page? (Choose the Evergreen Bootstrapper.)" IDNO done
    ExecShell "open" "https://developer.microsoft.com/microsoft-edge/webview2/"
    done:
  ${EndIf}
FunctionEnd

Section "${APP}" SecApp
  SectionIn RO
  Call CloseApp
  SetOutPath "$INSTDIR"
  File "${SRC}\${EXE}"
  File "${SRC}\StreamGremlin.ico"
  File "${SRC}\Read me first.txt"
  File "${SRC}\LICENSE.txt"
  File "${SRC}\THIRD-PARTY-NOTICES.md"
  File "${SRC}\THIRD-PARTY-LICENSES.txt"
  ; The command line has its own folder: Windows file names ignore case, so
  ; streamgremlin.exe and StreamGremlin.exe would be the same file.
  SetOutPath "$INSTDIR\cli"
  File "${SRC}\cli\streamgremlin.exe"
  SetOutPath "$INSTDIR\Stream Deck"
  File "${SRC}\Stream Deck\com.streamgremlin.streamdeck.streamDeckPlugin"
  SetOutPath "$INSTDIR"

  WriteUninstaller "$INSTDIR\${UNINSTALLER}"
  CreateShortcut "$SMPROGRAMS\${APP}.lnk" "$INSTDIR\${EXE}" "" "$INSTDIR\StreamGremlin.ico"

  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayName" "${APP}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "Publisher" "${APP}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\StreamGremlin.ico"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "UninstallString" '"$INSTDIR\${UNINSTALLER}"'
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoRepair" 1

  Call CheckWebView2
SectionEnd

Section "Desktop shortcut" SecDesktop
  CreateShortcut "$DESKTOP\${APP}.lnk" "$INSTDIR\${EXE}" "" "$INSTDIR\StreamGremlin.ico"
SectionEnd

; An update keeps the desktop shortcut only if there is one: the
; streamer may have removed it.
Function UpdateMode
  ${IfNot} ${FileExists} "$DESKTOP\${APP}.lnk"
    SectionSetFlags ${SecDesktop} 0
  ${EndIf}
FunctionEnd

Section "Uninstall"
  Call un.CloseApp
  Delete "$INSTDIR\${EXE}"
  Delete "$INSTDIR\StreamGremlin.ico"
  Delete "$INSTDIR\Read me first.txt"
  Delete "$INSTDIR\LICENSE.txt"
  Delete "$INSTDIR\THIRD-PARTY-NOTICES.md"
  Delete "$INSTDIR\THIRD-PARTY-LICENSES.txt"
  Delete "$INSTDIR\cli\streamgremlin.exe"
  Delete "$INSTDIR\Stream Deck\com.streamgremlin.streamdeck.streamDeckPlugin"
  Delete "$INSTDIR\${UNINSTALLER}"
  ; Only empty folders are removed: nothing else in them is ever deleted.
  RMDir "$INSTDIR\cli"
  RMDir "$INSTDIR\Stream Deck"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\${APP}.lnk"
  Delete "$DESKTOP\${APP}.lnk"
  ; WebView2's cache for the app's window (Wails puts it at
  ; %AppData%\StreamGremlin.exe); none of the streamer's data is in it.
  RMDir /r "$APPDATA\${EXE}"
  DeleteRegKey HKCU "${UNINSTALL_KEY}"
  DetailPrint "Your workflows, settings and history are kept in $LOCALAPPDATA\${APP}."
  DetailPrint "Delete that folder too if you won't use StreamGremlin again. Sign-ins are in Windows Credential Manager."
SectionEnd
