set(UTIDE_UNITY_ROOT "${CMAKE_SOURCE_DIR}/.unit-test-ide/inputs/unity")
if(NOT EXISTS "${UTIDE_UNITY_ROOT}/src/unity.c")
  message(FATAL_ERROR "verified Unity input is unavailable")
endif()

add_library(unity STATIC "${UTIDE_UNITY_ROOT}/src/unity.c")
target_include_directories(unity PUBLIC "${UTIDE_UNITY_ROOT}/src")

add_executable(classifier-tests classifier_test.c)
target_link_libraries(classifier-tests PRIVATE classifier unity)
add_test(NAME classifier-tests COMMAND classifier-tests)
