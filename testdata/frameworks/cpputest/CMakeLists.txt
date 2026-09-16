cmake_minimum_required(VERSION 3.25)

project(unit_test_ide_cpputest_fixture LANGUAGES C CXX)

if(NOT DEFINED UNIT_TEST_IDE_CPPUTEST_ROOT OR NOT IS_ABSOLUTE "${UNIT_TEST_IDE_CPPUTEST_ROOT}")
  message(FATAL_ERROR "UNIT_TEST_IDE_CPPUTEST_ROOT must be an absolute path")
endif()
if(NOT DEFINED UNIT_TEST_IDE_HELPER OR NOT IS_ABSOLUTE "${UNIT_TEST_IDE_HELPER}")
  message(FATAL_ERROR "UNIT_TEST_IDE_HELPER must be an absolute path")
endif()

enable_testing()
add_subdirectory("${UNIT_TEST_IDE_CPPUTEST_ROOT}" "${CMAKE_BINARY_DIR}/upstream-cpputest" EXCLUDE_FROM_ALL)

add_executable(phase9_cpputest tests/framework_tests.cpp)
target_link_libraries(phase9_cpputest PRIVATE CppUTest CppUTestExt)
set_target_properties(phase9_cpputest PROPERTIES
  RUNTIME_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/bin"
  RUNTIME_OUTPUT_DIRECTORY_DEBUG "${CMAKE_BINARY_DIR}/bin"
  RUNTIME_OUTPUT_DIRECTORY_RELEASE "${CMAKE_BINARY_DIR}/bin"
  RUNTIME_OUTPUT_DIRECTORY_RELWITHDEBINFO "${CMAKE_BINARY_DIR}/bin"
  RUNTIME_OUTPUT_DIRECTORY_MINSIZEREL "${CMAKE_BINARY_DIR}/bin"
)

include("${UNIT_TEST_IDE_HELPER}")
unit_test_ide_add_cpputest(TEST cpputest.framework TARGET phase9_cpputest)
