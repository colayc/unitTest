# Audited Windows framework recipes

These source-level SDK resources support the closed static CMake integration
subset, not arbitrary upstream framework projects. They use the locked real
CppUTest 4.0 and Unity 2.6.1 sources: no Mock/CMock, CppUTestExt, upstream
configuration, probes, scripts, GLOB, downloads, or generated config headers.
The existing framework archive, source-tree, license and UnitTestIDE.cmake locks
remain unchanged; this integration does not invoke that dynamic helper.

Users must explicitly integrate a recipe. Service startup, discovery and coverage
do not migrate or rewrite live project CMake files. Framework preparation must
authenticate the locked source tree before copying it into the project. Keep the
complete prepared source tree (including its license), with this layout:

```text
project/
  CMakeLists.txt
  src/ ...
  tests/ ...
  .unit-test-ide/
    workspace.json
    inputs/
      cpputest/                 # authenticated cpputest-4.0 tree
        include/CppUTest/ ...
        src/CppUTest/ ...
        src/Platforms/VisualCpp/UtestPlatform.cpp
        COPYING
      unity/                    # authenticated Unity-2.6.1 tree
        src/unity.c
        src/unity.h
        LICENSE.txt
      framework-recipes/
        windows-cpputest-4.0.cmake
        windows-unity-2.6.1.cmake
```

Only the selected framework source tree is needed. The two recipes are copied
byte-for-byte from this SDK directory by `copyFrameworkRecipeInputs(repositoryRoot,
workspaceRoot)`, after `verifyFrameworkRecipes` authenticates the closed manifest
and pinned recipe digests. The workspace must already exist. Publication refuses
an existing recipe destination, linked paths, and overwrites; this helper is not
a project migration tool. The manifest is descriptive inventory, not an arbitrary
executable CMake entry point.

After `project(...)` and before production target declarations, include the
selected recipe explicitly in the project root:

```cmake
include("${CMAKE_SOURCE_DIR}/.unit-test-ide/inputs/framework-recipes/windows-cpputest-4.0.cmake")
add_library(classifier STATIC src/classifier.cpp)
target_include_directories(classifier PUBLIC include)
add_executable(classifier-tests tests/classifier_test.cpp)
target_link_libraries(classifier-tests PRIVATE classifier CppUTest)
enable_testing()
add_test(NAME classifier-tests COMMAND classifier-tests)
```

For Unity, include `windows-unity-2.6.1.cmake`, use C sources and link
`classifier unity`. Do not redeclare either framework target. CppUTest exports
`CPPUTEST_USE_OWN_CONFIGURATION=1` (so CppUTestGeneratedConfig.h is not needed)
and `CPPUTEST_MEM_LEAK_DETECTION_DISABLED=1` to consumers, as provided by the
locked upstream configuration branches.

CppUTest requires a selected, verified Windows SDK/toolchain binding. The service
alone owns the readonly `UNIT_TEST_IDE_WINMM_LIBRARY` absolute file identity and
SHA-256 and verifies the actual executable link input. Do not set that variable,
pass a cache override, link a bare winmm name, or add a SDK search path. There is
no SDK library digest in this static recipe inventory. Missing or changed SDK
inputs fail closed in ordinary and instrumented builds.

With a trusted workspace, generated Debug profile and real coverage report,
use **Unit Test: Generate Managed Tests for Function**
(`unitTestIde.generateManagedTestsForFunction`), **Unit Test: Generate Managed
Tests for File** (`unitTestIde.generateManagedTestsForFile`), or **Unit Test:
Generate Managed Tests for Coverage Gap**
(`unitTestIde.generateManagedTestsForCoverageGap`). Review the validated candidate
and explicitly confirm application before the managed publisher writes test
sources and static CMake declarations; rerun all tests and coverage afterward.
The existing authenticated Unity runner path is unchanged.

Linux retains its original integration and does not select these Windows files.
This delivery is source-level only: new candidate packages still require explicit
resource inventory inclusion, digest authentication and package smoke acceptance.
Static resource/staging tests are not native build, coverage or release evidence.
