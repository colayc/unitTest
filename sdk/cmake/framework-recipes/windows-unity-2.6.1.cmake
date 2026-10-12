add_library(unity STATIC "${CMAKE_CURRENT_LIST_DIR}/../unity/src/unity.c")
target_include_directories(unity PUBLIC "${CMAKE_CURRENT_LIST_DIR}/../unity/src")
