cmake_minimum_required(VERSION 3.25)
project(unit_test_ide_coverage_fixture LANGUAGES CXX)

add_library(coverage_math STATIC src/math.cpp)
target_compile_features(coverage_math PUBLIC cxx_std_20)
add_custom_command(
  TARGET coverage_math POST_BUILD
  COMMAND "${CMAKE_COMMAND}" -E touch "${CMAKE_CURRENT_BINARY_DIR}/coverage-custom-command.stamp"
  VERBATIM
)

add_executable(coverage-tests test/math_test.cpp)
target_compile_features(coverage-tests PRIVATE cxx_std_20)
target_link_libraries(coverage-tests PRIVATE coverage_math)

enable_testing()
add_test(NAME coverage-tests COMMAND coverage-tests)
