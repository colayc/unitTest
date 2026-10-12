add_library(CppUTest STATIC
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/CommandLineArguments.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/MemoryLeakWarningPlugin.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/TestHarness_c.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/TestRegistry.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/CommandLineTestRunner.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/SimpleString.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/SimpleStringInternalCache.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/TestMemoryAllocator.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/TestResult.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/JUnitTestOutput.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/TeamCityTestOutput.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/TestFailure.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/TestOutput.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/MemoryLeakDetector.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/TestFilter.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/TestPlugin.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/TestTestingFixture.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/SimpleMutex.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/Utest.cpp"
  "${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/Platforms/VisualCpp/UtestPlatform.cpp"
)
target_include_directories(CppUTest PUBLIC "${CMAKE_CURRENT_LIST_DIR}/../cpputest/include")
target_compile_definitions(CppUTest PUBLIC CPPUTEST_USE_OWN_CONFIGURATION=1 CPPUTEST_MEM_LEAK_DETECTION_DISABLED=1)
target_link_libraries(CppUTest PUBLIC "${UNIT_TEST_IDE_WINMM_LIBRARY}")
