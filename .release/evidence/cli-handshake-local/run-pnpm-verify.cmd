@echo off
set "PATH=C:\codex_project\unitTest\.worktrees\fix-cli-launch-handshake\.bundled-tools\cmake\4.3.4\win32-x64\cmake-4.3.4-windows-x86_64\bin;C:\codex_project\unitTest\.worktrees\fix-cli-launch-handshake\.release\evidence\cli-handshake-local;C:\Users\DELL\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin;%PATH%"
set "GOCACHE=C:\codex_project\unitTest\.worktrees\fix-cli-launch-handshake\.superpowers\runtime\gocache"
"C:\Users\DELL\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe" "C:\Program Files\nodejs\node_modules\corepack\dist\corepack.js" pnpm verify
