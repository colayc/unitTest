#include "CppUTest/TestHarness.h"

TEST_GROUP(Existing) {};
TEST(Existing, still_present) { CHECK_TRUE(true); }
