cmake_minimum_required(VERSION 3.31)
project(unit_test_ide_preset LANGUAGES CXX)

add_library(math STATIC src/math.cpp)
target_compile_features(math PUBLIC cxx_std_20)

add_executable(sample_app src/main.cpp)
target_link_libraries(sample_app PRIVATE math)
