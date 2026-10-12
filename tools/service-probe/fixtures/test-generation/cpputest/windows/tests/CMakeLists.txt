add_executable(classifier-tests classifier_test.cpp)
target_link_libraries(classifier-tests PRIVATE classifier CppUTest)
add_test(NAME classifier-tests COMMAND classifier-tests)
