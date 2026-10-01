cmake_minimum_required(VERSION 3.25)
project(unit_test_ide_test_generation_cpputest LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
find_package(CppUTest CONFIG REQUIRED)

add_library(classifier STATIC src/classifier.cpp)
target_include_directories(classifier PUBLIC include)
add_executable(classifier-tests tests/classifier_test.cpp)
target_link_libraries(classifier-tests PRIVATE classifier CppUTest::CppUTest)
enable_testing()
add_test(NAME classifier-tests COMMAND classifier-tests)
