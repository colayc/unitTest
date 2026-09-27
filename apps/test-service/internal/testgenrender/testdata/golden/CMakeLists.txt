add_executable(unit_tests existing.cpp)
target_sources(unit_tests PRIVATE "generated/choose_test.cpp")
target_link_libraries(unit_tests PRIVATE core CppUTest)
