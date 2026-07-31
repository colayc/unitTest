#include "unity.h"

TEST_CASE(1, "one")
TEST_CASE(2, "two")
void test_named_case(int value, const char *label)
{
    (void)value;
    (void)label;
}

TEST_RANGE([1, 3, 1])
void test_inclusive_range(int value)
{
    (void)value;
}

TEST_RANGE(<0, 3, 1>)
void test_exclusive_range(int value)
{
    (void)value;
}

TEST_RANGE([1, 2, 1], [10, 20, 10])
void test_range_product(int left, int right)
{
    (void)left;
    (void)right;
}

TEST_RANGE([-1.5, -0.5, 0.5])
void test_decimal_range(double value)
{
    (void)value;
}

TEST_RANGE(<3, 0, -1>)
void test_descending_range(int value)
{
    (void)value;
}
