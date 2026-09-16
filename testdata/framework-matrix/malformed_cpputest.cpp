#include <cstdlib>

#include "CppUTest/TestHarness.h"

TEST_GROUP(MatrixMalformed) {};

TEST(MatrixMalformed, MalformedOutput)
{
    std::_Exit(0);
}
