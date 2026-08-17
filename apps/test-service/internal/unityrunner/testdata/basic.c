#include "unity.h"

void setUp(void)
{
}

void tearDown(void)
{
}

static const char *not_a_test = "void test_from_string(void) {}";

/* void test_from_block_comment(void) {} */
// void test_from_line_comment(void) {}

void test_adds_numbers(void)
{
    TEST_ASSERT_EQUAL_INT(4, 2 + 2);
}

void test_handles_zero(void) {
    TEST_ASSERT_EQUAL_INT(0, 0);
}
