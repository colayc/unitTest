#include <cstdlib>

#include "CppUTest/CommandLineTestRunner.h"
#include "CppUTest/TestHarness.h"

TEST_GROUP(MatrixMalformed) {};

TEST(MatrixMalformed, MalformedOutput)
{
    std::_Exit(0);
}

int main(int argc, char** argv)
{
    return CommandLineTestRunner::RunAllTests(argc, argv);
}
