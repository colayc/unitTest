cmake_minimum_required(VERSION 3.25)
project(unit_test_ide_coverage_unity_fixture LANGUAGES C CXX)

include("${CMAKE_CURRENT_LIST_DIR}/linux-inputs.cmake")
add_library(utide_unity STATIC "${UTIDE_TEST_UNITY_ROOT}/src/unity.c")
target_include_directories(utide_unity PUBLIC "${UTIDE_TEST_UNITY_ROOT}/src")
# The service injects GCC coverage options at directory scope. Keep the
# third-party Unity implementation out of the fixture's evidence at source
# scope as well, so only the project and generated runner are measured.
set_source_files_properties(
  "${UTIDE_TEST_UNITY_ROOT}/src/unity.c"
  PROPERTIES COMPILE_OPTIONS "-fno-profile-arcs;-fno-test-coverage"
)
target_compile_options(utide_unity PRIVATE -fno-profile-arcs -fno-test-coverage)
include("${UTIDE_TEST_CMAKE_HELPER}")

add_library(coverage_unity_math STATIC src/math.c)
set_target_properties(coverage_unity_math PROPERTIES C_STANDARD 11 C_STANDARD_REQUIRED YES)

add_executable(coverage-unity-tests test/test_math.c)
set_target_properties(coverage-unity-tests PROPERTIES C_STANDARD 11 C_STANDARD_REQUIRED YES)
target_link_libraries(coverage-unity-tests PRIVATE coverage_unity_math utide_unity)

enable_testing()
unit_test_ide_add_unity_test(
  TEST coverage-unity-tests
  TARGET coverage-unity-tests
  TEST_SOURCES test/test_math.c
)
