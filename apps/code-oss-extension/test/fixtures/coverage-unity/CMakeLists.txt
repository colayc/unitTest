cmake_minimum_required(VERSION 3.25)
project(unit_test_ide_coverage_unity_fixture LANGUAGES C)

find_package(PkgConfig REQUIRED)
pkg_check_modules(UNITY REQUIRED IMPORTED_TARGET unity)
include(UnitTestIDE)

add_library(coverage_unity_math STATIC src/math.c)
set_target_properties(coverage_unity_math PROPERTIES C_STANDARD 11 C_STANDARD_REQUIRED YES)

add_executable(coverage-unity-tests src/math.c test/test_math.c)
set_target_properties(coverage-unity-tests PROPERTIES C_STANDARD 11 C_STANDARD_REQUIRED YES)
target_link_libraries(coverage-unity-tests PRIVATE PkgConfig::UNITY)

enable_testing()
unit_test_ide_add_unity_test(
  TEST coverage-unity-tests
  TARGET coverage-unity-tests
  TEST_SOURCES test/test_math.c
)
