add_executable(unit_tests existing_test.cpp)
target_link_libraries(unit_tests PRIVATE core CppUTest)
