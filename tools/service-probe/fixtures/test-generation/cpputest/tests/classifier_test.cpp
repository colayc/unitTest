#include "classifier.hpp"
#include <CppUTest/CommandLineTestRunner.h>
#include <CppUTest/TestHarness.h>

TEST_GROUP(Classifier) {};

TEST(Classifier, ExistingPositivePath) {
  CHECK_EQUAL(1, classify(1));
}

int main(int argc, char** argv) {
  return CommandLineTestRunner::RunAllTests(argc, argv);
}
