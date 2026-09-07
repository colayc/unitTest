#include "CppUTest/CommandLineTestRunner.h"
#include "CppUTest/TestHarness.h"

int coverage_branch(int value);
int coverage_square(int value);

TEST_GROUP(CoverageFixture) {};

TEST(CoverageFixture, coversBranch) {
  CHECK_EQUAL(1, coverage_branch(5));
  CHECK_EQUAL(25, coverage_square(5));
}

TEST(CoverageFixture, failsAfterInstrumentedCode) {
  const int instrumented = coverage_branch(0);
  CHECK_EQUAL(1, instrumented);
}

int main(int argc, char** argv) {
  return CommandLineTestRunner::RunAllTests(argc, argv);
}
