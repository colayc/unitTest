cmake_minimum_required(VERSION 3.25)

project(unit_test_ide_helper_smoke LANGUAGES C CXX)

enable_testing()

if(NOT DEFINED UNIT_TEST_IDE_HELPER)
  message(FATAL_ERROR "UNIT_TEST_IDE_HELPER is required")
endif()
if(NOT DEFINED UTIDE_SCENARIO)
  set(UTIDE_SCENARIO "valid")
endif()

include("${UNIT_TEST_IDE_HELPER}")

add_executable(cpputest_target cpputest_main.cpp)
add_executable(unity_target unity_tests.c "unity extra-单元.c")
target_include_directories(unity_target PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}")

if(UTIDE_SCENARIO STREQUAL "valid")
  unit_test_ide_add_cpputest(
    TEST cpputest.case
    TARGET cpputest_target
  )
  unit_test_ide_add_unity_test(
    TEST "unity case-单元"
    TARGET unity_target
    TEST_SOURCES unity_tests.c "unity extra-单元.c"
  )
elseif(UTIDE_SCENARIO STREQUAL "duplicate-test")
  unit_test_ide_add_cpputest(TEST duplicate.case TARGET cpputest_target)
  unit_test_ide_add_unity_test(
    TEST duplicate.case
    TARGET unity_target
    TEST_SOURCES unity_tests.c
  )
elseif(UTIDE_SCENARIO STREQUAL "missing-target")
  unit_test_ide_add_cpputest(TEST missing.case TARGET missing_target)
elseif(UTIDE_SCENARIO STREQUAL "wrong-target")
  add_library(not_an_executable STATIC unity_tests.c)
  unit_test_ide_add_cpputest(TEST library.case TARGET not_an_executable)
elseif(UTIDE_SCENARIO STREQUAL "unity-existing-main")
  target_sources(unity_target PRIVATE cpputest_main.cpp)
  unit_test_ide_add_unity_test(
    TEST unity.main
    TARGET unity_target
    TEST_SOURCES unity_tests.c
  )
elseif(UTIDE_SCENARIO STREQUAL "source-escape")
  if(NOT DEFINED UTIDE_ESCAPE_SOURCE)
    message(FATAL_ERROR "UTIDE_ESCAPE_SOURCE is required")
  endif()
  unit_test_ide_add_unity_test(
    TEST unity.escape
    TARGET unity_target
    TEST_SOURCES "${UTIDE_ESCAPE_SOURCE}"
  )
elseif(UTIDE_SCENARIO STREQUAL "unparsed-keyword")
  unit_test_ide_add_cpputest(
    TEST unparsed.case
    TARGET cpputest_target
    EXTRA unexpected
  )
elseif(UTIDE_SCENARIO STREQUAL "unsafe-keyword")
  unit_test_ide_add_cpputest(
    TEST unsafe.case
    TARGET cpputest_target
    COMMAND arbitrary-command
  )
else()
  message(FATAL_ERROR "unknown UTIDE_SCENARIO: ${UTIDE_SCENARIO}")
endif()
