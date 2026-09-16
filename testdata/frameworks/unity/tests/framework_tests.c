#include "MockDependency.h"
#include "unity.h"

void abort(void);
void phase9_prepare_crash(void);
void phase9_sleep_30_seconds(void);

void setUp(void)
{
    MockDependency_Init();
}

void tearDown(void)
{
    MockDependency_Verify();
    MockDependency_Destroy();
}

void test_pass(void)
{
    TEST_ASSERT_EQUAL_INT(4, 2 + 2);
}

void test_assertion_failure(void)
{
    TEST_ASSERT_EQUAL_INT(1, 2);
}

void test_skipped(void)
{
    TEST_IGNORE_MESSAGE("phase9 skip fixture");
}

void test_cmock_expectation_failure(void)
{
    Dependency_Read_ExpectAndReturn(7, 42);
    (void)Dependency_Read(8);
}

void test_crash(void)
{
    phase9_prepare_crash();
    abort();
}

void test_timeout(void)
{
    phase9_sleep_30_seconds();
}
