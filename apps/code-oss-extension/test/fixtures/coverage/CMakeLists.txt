cmake_minimum_required(VERSION 3.25)
if(CMAKE_HOST_WIN32)
  project(unit_test_ide_coverage_fixture LANGUAGES CXX)
else()
  project(unit_test_ide_coverage_fixture LANGUAGES C CXX)
endif()

add_library(coverage_math STATIC src/math.cpp)
target_compile_features(coverage_math PUBLIC cxx_std_20)
add_custom_command(
  TARGET coverage_math POST_BUILD
  COMMAND "${CMAKE_COMMAND}" -E touch "${CMAKE_CURRENT_BINARY_DIR}/coverage-custom-command.stamp"
  VERBATIM
)

add_executable(coverage-tests test/math_test.cpp)
target_compile_features(coverage-tests PRIVATE cxx_std_20)
target_link_libraries(coverage-tests PRIVATE coverage_math)

enable_testing()

if(WIN32)
  # Preserve the pre-Task-8 self-contained Windows smoke fixture. Windows
  # coverage never depends on a host-installed CppUTest package.
  add_test(NAME coverage-tests COMMAND coverage-tests)
else()
  include("${CMAKE_CURRENT_LIST_DIR}/linux-inputs.cmake")
  set(TESTS OFF CACHE BOOL "" FORCE)
  add_subdirectory("${UTIDE_TEST_CPPUTEST_ROOT}" "${CMAKE_CURRENT_BINARY_DIR}/cpputest" EXCLUDE_FROM_ALL)
  target_compile_options(CppUTest PRIVATE -fno-profile-arcs -fno-test-coverage)
  include("${UTIDE_TEST_CMAKE_HELPER}")
  target_link_libraries(coverage-tests PRIVATE CppUTest)
  unit_test_ide_add_cpputest(
    TEST coverage-tests
    TARGET coverage-tests
  )
endif()
