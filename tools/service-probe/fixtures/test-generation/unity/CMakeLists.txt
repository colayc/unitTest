cmake_minimum_required(VERSION 3.28)
project(unit_test_ide_test_generation_unity LANGUAGES C)

set(CMAKE_C_STANDARD 11)
set(CMAKE_C_STANDARD_REQUIRED ON)

add_library(classifier STATIC src/classifier.c)
target_include_directories(classifier PUBLIC include)

enable_testing()
add_subdirectory(tests)
