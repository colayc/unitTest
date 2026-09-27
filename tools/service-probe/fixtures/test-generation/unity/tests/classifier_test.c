#include "classifier.h"
#include "unity.h"

void setUp(void) {}
void tearDown(void) {}

void test_classifier_covers_all_states(void) {
  TEST_ASSERT_EQUAL(CLASSIFICATION_NEGATIVE, classify(-1));
  TEST_ASSERT_EQUAL(CLASSIFICATION_ZERO, classify(0));
  TEST_ASSERT_EQUAL(CLASSIFICATION_POSITIVE, classify(1));
}

int main(void) {
  UNITY_BEGIN();
  RUN_TEST(test_classifier_covers_all_states);
  return UNITY_END();
}
