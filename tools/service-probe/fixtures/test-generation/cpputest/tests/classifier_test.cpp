#include "classifier.hpp"
#include <CppUTest/TestHarness.h>

TEST_GROUP(Classifier) {};

TEST(Classifier, ClassifiesNegativeZeroAndPositive) {
  CHECK_EQUAL(static_cast<int>(Classification::negative), static_cast<int>(classify(-1)));
  CHECK_EQUAL(static_cast<int>(Classification::zero), static_cast<int>(classify(0)));
  CHECK_EQUAL(static_cast<int>(Classification::positive), static_cast<int>(classify(1)));
}
