#include "unity.h"
#include "include/choose.h"
#include <stdint.h>
#include <stdbool.h>

void setUp(void) {}
void tearDown(void) {}

void test_case_cccccccccccccccc(void) {
  const int32_t actual = choose(2);
  TEST_ASSERT_EQUAL_INT32(7, actual);
}

int main(void) {
  UNITY_BEGIN();
  RUN_TEST(test_case_cccccccccccccccc);
  return UNITY_END();
}
