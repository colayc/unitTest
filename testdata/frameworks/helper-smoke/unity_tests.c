#include "unity.h"

UNITY_STORAGE_T Unity;

void UnityBegin(const char *source)
{
    (void)source;
    Unity.TestFailures = 0;
    Unity.TestIgnores = 0;
}

void UnityDefaultTestRun(
    void (*test_function)(void),
    const char *name,
    UNITY_LINE_TYPE line)
{
    (void)name;
    (void)line;
    test_function();
}

int UnityEnd(void)
{
    return Unity.TestFailures == 0 ? 0 : 1;
}

void setUp(void)
{
}

void tearDown(void)
{
}

void test_unity_helper(void)
{
}
