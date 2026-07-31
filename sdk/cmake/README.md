# Unit Test IDE CMake helper

`UnitTestIDE.cmake` 是 Project 显式 opt-in 的 CMake SDK。它只注册已有 executable target，不修改用户源码，不扫描未声明文件，也不接受 raw command、args、environment、working directory、Shell 或 hook。

## 要求

- CMake 3.25 或更高版本；
- Project 已调用 `enable_testing()`，或通过 `include(CTest)` 启用 CTest；
- CppUTest 和 Unity 由 Project 自行提供并链接；
- Unity target 尚未包含其他 `main`；
- `UTIDE_UNITY_RUNNER_GENERATOR` 指向绝对、固定版本的 `unity-runner-generator`。

Service 从产品 installation manifest 注入 `UTIDE_UNITY_RUNNER_GENERATOR`。在 IDE 外独立配置时，也必须显式传入同版本工具；helper 不搜索 `PATH`。

## CppUTest

```cmake
include(CTest)
include("/path/to/UnitTestIDE.cmake")

add_executable(core_cpputest core_cpputest.cpp)
target_link_libraries(core_cpputest PRIVATE CppUTest CppUTestExt)

unit_test_ide_add_cpputest(
  TEST core_cpputest
  TARGET core_cpputest
)
```

helper 使用 `$<TARGET_FILE:...>` 注册 CTest，并写入固定 label：

```text
utide.framework.cpputest
```

## Unity

```cmake
include(CTest)
include("/path/to/UnitTestIDE.cmake")

add_executable(core_unity test_math.c test_buffer.c)
target_link_libraries(core_unity PRIVATE unity)

unit_test_ide_add_unity_test(
  TEST core_unity
  TARGET core_unity
  TEST_SOURCES
    test_math.c
    test_buffer.c
)
```

`TEST_SOURCES` 按调用位置相对于 `CMAKE_CURRENT_SOURCE_DIR` 解析，但解析后的文件必须位于顶层 `CMAKE_SOURCE_DIR` 内。helper 验证 generator 的 `--version=json-v1`，生成 runner C 和 manifest，把 runner 加入 target，并将声明源加入 `CMAKE_CONFIGURE_DEPENDS`。

Unity CTest 固定写入：

```text
utide.framework.unity
utide.runner.v1
```

sidecar 路径完全由 build root 和 CTest logical name 的 SHA-256 派生：

```text
<build-root>/.unit-test-ide/<sha256(ctest-name)>/runner.c
<build-root>/.unit-test-ide/<sha256(ctest-name)>/manifest.json
```

路径不受 Protocol 或 Workspace 自定义字段控制；single-config 与 multi-config generator 共享相同的 deterministic runner/manifest，CTest executable 路径仍由 `$<TARGET_FILE:...>` 针对所选 configuration 解析。
