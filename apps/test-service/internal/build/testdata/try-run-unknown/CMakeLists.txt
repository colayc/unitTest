cmake_minimum_required(VERSION 3.25)
project(try_run_unknown LANGUAGES CXX)

try_run(RUN_RESULT COMPILE_RESULT "${CMAKE_CURRENT_BINARY_DIR}" "${CMAKE_CURRENT_SOURCE_DIR}/probe.cpp")
