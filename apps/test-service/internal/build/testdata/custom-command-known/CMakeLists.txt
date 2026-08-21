cmake_minimum_required(VERSION 3.25)
project(custom_command_known LANGUAGES CXX)

add_subdirectory(nested)
include(generated/known-command.cmake)
