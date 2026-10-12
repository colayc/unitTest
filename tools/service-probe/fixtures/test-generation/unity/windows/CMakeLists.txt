cmake_minimum_required(VERSION 3.28)
project(unit_test_ide_test_generation_unity LANGUAGES C)

set(CMAKE_C_STANDARD 11)
set(CMAKE_C_STANDARD_REQUIRED ON)

include("${CMAKE_SOURCE_DIR}/.unit-test-ide/inputs/framework-recipes/windows-unity-2.6.1.cmake")

add_library(classifier STATIC src/classifier.c)
target_include_directories(classifier PUBLIC include)

enable_testing()
add_subdirectory(tests)
