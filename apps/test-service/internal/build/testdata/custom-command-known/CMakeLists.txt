cmake_minimum_required(VERSION 3.25)
project(custom_command_known LANGUAGES CXX)

execute_process(
  COMMAND "${CMAKE_COMMAND}" -E touch "${CMAKE_CURRENT_BINARY_DIR}/configured.stamp"
)

add_subdirectory(nested)
include(generated/known-command.cmake)
