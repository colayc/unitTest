cmake_minimum_required(VERSION 3.25)
project(execute_process_unknown LANGUAGES CXX)

execute_process(COMMAND unknown.exe --escape-offline-boundary)
