#include "CppUTest/TestHarness.h"
#include "include/choose.h"
#include <stdint.h>

TEST_GROUP(Generated_choose) {};

TEST(Generated_choose, case_cccccccccccccccc) {
  const auto actual = choose(2);
  CHECK_EQUAL(7, actual);
}
