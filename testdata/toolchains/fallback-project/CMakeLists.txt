cmake_minimum_required(VERSION 3.31)
project(unit_test_ide_fallback LANGUAGES CXX)

add_executable(sample_app src/main.cpp)
add_executable(secondary_app src/main.cpp)
target_compile_features(sample_app PRIVATE cxx_std_20)
target_compile_features(secondary_app PRIVATE cxx_std_20)

add_custom_target(
  slow_target
  COMMAND "${CMAKE_COMMAND}" -E sleep 30
  VERBATIM
)
