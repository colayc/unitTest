#include <CppUTest/CommandLineTestRunner.h>
#include <CppUTest/TestRegistry.h>
#include <CppUTestExt/MockSupport.h>
#include <CppUTestExt/MockSupportPlugin.h>

#include <chrono>
#include <cstdlib>
#include <thread>
#if defined(_WIN32)
#include <windows.h>
#endif

TEST_GROUP(Phase9)
{
};

TEST(Phase9, Pass)
{
    CHECK_EQUAL(4, 2 + 2);
}

TEST(Phase9, AssertionFailure)
{
    CHECK_EQUAL(1, 2);
}

IGNORE_TEST(Phase9, Skipped)
{
    FAIL("ignored test executed");
}

TEST(Phase9, MockMissingCall)
{
    mock().expectOneCall("read");
}

TEST(Phase9, MockUnexpectedCall)
{
    mock().actualCall("unexpected");
}

TEST(Phase9, MockParameterMismatch)
{
    mock().expectOneCall("read").withIntParameter("channel", 1);
    mock().actualCall("read").withIntParameter("channel", 2);
}

TEST(Phase9, Crash)
{
#if defined(_WIN32)
    SetErrorMode(SEM_NOGPFAULTERRORBOX | SEM_FAILCRITICALERRORS);
    _set_abort_behavior(0, _WRITE_ABORT_MSG | _CALL_REPORTFAULT);
#endif
    std::abort();
}

TEST(Phase9, Timeout)
{
    std::this_thread::sleep_for(std::chrono::seconds(30));
}

int main(int argc, char** argv)
{
    MockSupportPlugin mockSupportPlugin;
    TestRegistry::getCurrentRegistry()->installPlugin(&mockSupportPlugin);
    const int result = CommandLineTestRunner::RunAllTests(argc, argv);
    TestRegistry::getCurrentRegistry()->removePluginByName(mockSupportPlugin.getName());
    return result;
}
