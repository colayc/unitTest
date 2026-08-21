cmake_minimum_required(VERSION 3.25)
project(configure_write_unknown LANGUAGES CXX)

file(WRITE "${CMAKE_CURRENT_SOURCE_DIR}/generated.cmake" "execute_process(COMMAND unknown.exe)\n")
include(generated.cmake)
