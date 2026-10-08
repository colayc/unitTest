add_executable(classifier-tests classifier_test.c)
target_link_libraries(classifier-tests PRIVATE classifier unity)
add_test(NAME classifier-tests COMMAND classifier-tests)
