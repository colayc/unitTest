cmake_minimum_required(VERSION 3.28)

project(unit_test_ide_framework_matrix LANGUAGES C CXX)

if(NOT UNIT_TEST_IDE_FRAMEWORK MATCHES "^(cpputest|unity)$")
  message(FATAL_ERROR "UNIT_TEST_IDE_FRAMEWORK must be cpputest or unity")
endif()
if(NOT DEFINED UNIT_TEST_IDE_HELPER OR NOT IS_ABSOLUTE "${UNIT_TEST_IDE_HELPER}")
  message(FATAL_ERROR "UNIT_TEST_IDE_HELPER must be an absolute path")
endif()

enable_testing()
include("${UNIT_TEST_IDE_HELPER}")

if(UNIT_TEST_IDE_FRAMEWORK STREQUAL "cpputest")
  add_subdirectory("../frameworks/cpputest" "f1")
  set_property(
    TEST cpputest.framework
    DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}/../frameworks/cpputest"
    PROPERTY TIMEOUT 2
  )

  add_executable(phase9_cpputest_malformed malformed_cpputest.cpp)
  target_link_libraries(phase9_cpputest_malformed PRIVATE CppUTest CppUTestExt)
  unit_test_ide_add_cpputest(
    TEST cpputest.matrix.malformed
    TARGET phase9_cpputest_malformed
  )
else()
  add_subdirectory("../frameworks/unity" "f1")
  set_property(
    TEST unity.framework
    DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}/../frameworks/unity"
    PROPERTY TIMEOUT 2
  )

  add_executable(phase9_unity_malformed malformed_unity.c)
  target_include_directories(phase9_unity_malformed PRIVATE
    "${UNIT_TEST_IDE_UNITY_ROOT}/src"
  )
  target_link_libraries(phase9_unity_malformed PRIVATE utide_unity)
  set_target_properties(phase9_unity_malformed PROPERTIES
    C_STANDARD 11
    C_STANDARD_REQUIRED YES
  )
  unit_test_ide_add_unity_test(
    TEST unity.matrix.malformed
    TARGET phase9_unity_malformed
    TEST_SOURCES malformed_unity.c
  )
endif()

add_executable(phase9_matrix_opaque opaque.c)
add_test(
  NAME "${UNIT_TEST_IDE_FRAMEWORK}.matrix.opaque"
  COMMAND phase9_matrix_opaque
)
set_tests_properties("${UNIT_TEST_IDE_FRAMEWORK}.matrix.opaque" PROPERTIES TIMEOUT 2)
