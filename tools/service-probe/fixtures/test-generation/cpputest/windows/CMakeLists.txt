cmake_minimum_required(VERSION 3.28)
set(CMAKE_POLICY_VERSION_MINIMUM 3.5)
project(unit_test_ide_test_generation_cpputest LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

include("${CMAKE_SOURCE_DIR}/.unit-test-ide/inputs/framework-recipes/windows-cpputest-4.0.cmake")

add_library(classifier STATIC src/classifier.cpp)
target_include_directories(classifier PUBLIC include)

enable_testing()
add_subdirectory(tests)
