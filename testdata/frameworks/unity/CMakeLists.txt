cmake_minimum_required(VERSION 3.25)

project(unit_test_ide_unity_fixture LANGUAGES C)

foreach(required_variable IN ITEMS
    UNIT_TEST_IDE_UNITY_ROOT
    UNIT_TEST_IDE_CMOCK_ROOT
    UNIT_TEST_IDE_HELPER
    UTIDE_UNITY_RUNNER_GENERATOR)
  if(NOT DEFINED ${required_variable} OR NOT IS_ABSOLUTE "${${required_variable}}")
    message(FATAL_ERROR "${required_variable} must be an absolute path")
  endif()
endforeach()

enable_testing()

add_library(utide_unity STATIC "${UNIT_TEST_IDE_UNITY_ROOT}/src/unity.c")
target_include_directories(utide_unity PUBLIC "${UNIT_TEST_IDE_UNITY_ROOT}/src")
set_target_properties(utide_unity PROPERTIES C_STANDARD 11 C_STANDARD_REQUIRED YES)

add_library(utide_cmock STATIC
  "${UNIT_TEST_IDE_CMOCK_ROOT}/src/cmock.c"
  mocks/MockDependency.c
)
target_include_directories(utide_cmock PUBLIC
  "${UNIT_TEST_IDE_CMOCK_ROOT}/src"
  "${UNIT_TEST_IDE_UNITY_ROOT}/src"
  "${CMAKE_CURRENT_SOURCE_DIR}/include"
  "${CMAKE_CURRENT_SOURCE_DIR}/mocks"
)
set_target_properties(utide_cmock PROPERTIES C_STANDARD 11 C_STANDARD_REQUIRED YES)

add_executable(phase9_unity tests/framework_tests.c tests/fixture_runtime.c)
target_include_directories(phase9_unity PRIVATE
  "${UNIT_TEST_IDE_UNITY_ROOT}/src"
  "${UNIT_TEST_IDE_CMOCK_ROOT}/src"
  "${CMAKE_CURRENT_SOURCE_DIR}/include"
  "${CMAKE_CURRENT_SOURCE_DIR}/mocks"
)
target_link_libraries(phase9_unity PRIVATE utide_unity utide_cmock)
set_target_properties(phase9_unity PROPERTIES
  C_STANDARD 11
  C_STANDARD_REQUIRED YES
  RUNTIME_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/bin"
  RUNTIME_OUTPUT_DIRECTORY_DEBUG "${CMAKE_BINARY_DIR}/bin"
  RUNTIME_OUTPUT_DIRECTORY_RELEASE "${CMAKE_BINARY_DIR}/bin"
  RUNTIME_OUTPUT_DIRECTORY_RELWITHDEBINFO "${CMAKE_BINARY_DIR}/bin"
  RUNTIME_OUTPUT_DIRECTORY_MINSIZEREL "${CMAKE_BINARY_DIR}/bin"
)

include("${UNIT_TEST_IDE_HELPER}")
unit_test_ide_add_unity_test(
  TEST unity.framework
  TARGET phase9_unity
  TEST_SOURCES tests/framework_tests.c
)
