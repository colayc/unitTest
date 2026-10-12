set(UTIDE_CPPUTEST_ROOT "${CMAKE_SOURCE_DIR}/.unit-test-ide/inputs/cpputest")
if(NOT EXISTS "${UTIDE_CPPUTEST_ROOT}/CMakeLists.txt")
  message(FATAL_ERROR "verified CppUTest input is unavailable")
endif()

set(CPPUTEST_FLAGS OFF CACHE BOOL "" FORCE)
set(MEMORY_LEAK_DETECTION OFF CACHE BOOL "" FORCE)
set(TESTS OFF CACHE BOOL "" FORCE)
add_subdirectory("${UTIDE_CPPUTEST_ROOT}" "${CMAKE_BINARY_DIR}/third_party/cpputest" EXCLUDE_FROM_ALL)

add_executable(classifier-tests classifier_test.cpp)
target_link_libraries(classifier-tests PRIVATE classifier CppUTest CppUTestExt)
target_compile_definitions(classifier-tests PRIVATE CPPUTEST_MEM_LEAK_DETECTION_DISABLED)
add_test(NAME classifier-tests COMMAND classifier-tests)
