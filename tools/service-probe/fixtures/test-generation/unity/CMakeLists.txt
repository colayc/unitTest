cmake_minimum_required(VERSION 3.25)
project(unit_test_ide_test_generation_unity LANGUAGES C)

set(CMAKE_C_STANDARD 11)
find_path(UNITY_INCLUDE_DIR unity.h REQUIRED)
find_library(UNITY_LIBRARY NAMES unity REQUIRED)

add_library(classifier STATIC src/classifier.c)
target_include_directories(classifier PUBLIC include)
add_executable(classifier-tests tests/classifier_test.c)
target_include_directories(classifier-tests PRIVATE "${UNITY_INCLUDE_DIR}")
target_link_libraries(classifier-tests PRIVATE classifier "${UNITY_LIBRARY}")
enable_testing()
add_test(NAME classifier-tests COMMAND classifier-tests)
