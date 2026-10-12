#include "classifier.h"
#include "unity.h"

void setUp(void) {}
void tearDown(void) {}

void test_classifier_existing_positive_path(void) {
  TEST_ASSERT_EQUAL(1, classify(1));
}

int main(void) {
  UNITY_BEGIN();
  RUN_TEST(test_classifier_existing_positive_path);
  return UNITY_END();
}
