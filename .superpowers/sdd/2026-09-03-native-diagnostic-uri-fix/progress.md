# SDD ledger — plan: docs/superpowers/plans/2026-09-03-native-diagnostic-uri-fix.md

Execution started on branch `codex/fix-cli-launch-handshake`.

Task 1: complete (commits 4f2b8a3..ceb87d4, review clean; implementation commit was cherry-picked from the root worktree as `ceb87d4`).
Task 2: fix round 1/5 (1 addressed, 0 open — manager-level fallback regression; commits 076bb95..6655b1a).
Task 2: complete (commits ceb87d4..6655b1a, review clean).
Task 3: fix round 1/5 (1 addressed, 0 open — query/fragment rejection coverage; commits 1a4cd1b..2776bf8).
Task 3: complete (commits 6655b1a..2776bf8, review clean).
Task 4: complete with concerns (pinned `pnpm verify` passed; focused Go and release tests passed; native MSVC family including compiler-diagnostic `<workspace>/src/main.cpp` passed; the full matrix stopped at clang-cl because the globally mandated `CMAKE_GENERATOR_TOOLSET=version=14.44.35207` is rejected by its Ninja generator; final process/diff audits clean).
Final fix: source commit `759f5c9` makes final-path containment and relative mapping atomic, with POSIX symlink and Windows junction/cross-volume regression coverage. Pinned `pnpm verify` passed. Split native qualification passed MSVC 14.44 (16/16) and clang-cl 22.1.8/Ninja without a toolset variable (16/16); the standard combined report is path-free. Status remains `DONE_WITH_CONCERNS` only because Vitest is not installed; the exact command fails and the pinned TypeScript+Node fallback passes 15/15.
