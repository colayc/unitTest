#include <unity.h>

int unity_coverage_branch(int value);
int unity_coverage_square(int value);

void setUp(void) {}
void tearDown(void) {}

void test_covers_positive_branch(void) {
  TEST_ASSERT_EQUAL_INT(6, unity_coverage_branch(5));
  TEST_ASSERT_EQUAL_INT(25, unity_coverage_square(5));
}

void test_covers_zero_branch(void) {
  TEST_ASSERT_EQUAL_INT(0, unity_coverage_branch(0));
}
