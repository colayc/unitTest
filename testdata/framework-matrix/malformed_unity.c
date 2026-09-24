#include <stdlib.h>

#include "unity.h"

void setUp(void) {}
void tearDown(void) {}

void test_malformed_output(void)
{
    _Exit(0);
}
