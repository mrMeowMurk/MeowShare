@echo off
setlocal

if "%1"=="" (
    echo Usage:
    echo   start.bat web    - Start web interface
    echo   start.bat node   - Start P2P node
    echo   start.bat share  - Share a file
    echo   start.bat download - Download a file
    exit /b 1
)

set SHARE_DIR=%USERPROFILE%\.p2p-share
set KEY_FILE=%SHARE_DIR%\key

if not exist "%SHARE_DIR%" mkdir "%SHARE_DIR%"

if "%1"=="web" (
    echo Starting web interface on http://localhost:8080
    .\bin\p2p-web.exe --share-dir="%SHARE_DIR%" --key-file="%KEY_FILE%"
) else if "%1"=="node" (
    echo Starting P2P node
    .\bin\p2p-share.exe start --share-dir="%SHARE_DIR%" --key-file="%KEY_FILE%"
) else if "%1"=="share" (
    if "%2"=="" (
        echo Usage: start.bat share [file_path]
        exit /b 1
    )
    echo Sharing file: %2
    .\bin\p2p-share.exe share "%2" --share-dir="%SHARE_DIR%" --key-file="%KEY_FILE%"
) else if "%1"=="download" (
    if "%2"=="" (
        echo Usage: start.bat download [file_hash] [output_path]
        exit /b 1
    )
    if "%3"=="" (
        echo Usage: start.bat download [file_hash] [output_path]
        exit /b 1
    )
    echo Downloading file with hash: %2
    .\bin\p2p-share.exe download "%2" "%3" --share-dir="%SHARE_DIR%" --key-file="%KEY_FILE%"
) else (
    echo Unknown command: %1
    echo.
    echo Available commands:
    echo   web     - Start web interface
    echo   node    - Start P2P node
    echo   share   - Share a file
    echo   download - Download a file
    exit /b 1
) 