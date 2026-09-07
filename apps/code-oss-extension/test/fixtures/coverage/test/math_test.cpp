#if defined(_WIN32)
#include <iostream>
#include <string>
#include <string_view>
#include <vector>
int coverage_branch(int value);
namespace {
constexpr std::string_view kGroup = "CoverageFixture";
constexpr std::string_view kCoveredCase = "coversBranch";
constexpr std::string_view kFailedCase = "failsAfterInstrumentedCode";
struct Selection { bool list = false; std::string group; std::string name; };
bool parse_arguments(int argc, char** argv, Selection& selection) {
  std::vector<std::string> arguments;
  for (int index = 1; index < argc; ++index) arguments.emplace_back(argv[index]);
  if (arguments == std::vector<std::string>{"-ln"}) { selection.list = true; return true; }
  if (arguments == std::vector<std::string>{"-v"}) return true;
  if (arguments.size() == 3 && arguments[0] == "-v" && arguments[1] == "-sg" && !arguments[2].empty()) { selection.group = arguments[2]; return true; }
  if (arguments.size() == 5 && arguments[0] == "-v" && arguments[1] == "-sg" && !arguments[2].empty() && arguments[3] == "-sn" && !arguments[4].empty()) { selection.group = arguments[2]; selection.name = arguments[4]; return true; }
  return false;
}
bool selected(const Selection& selection, std::string_view name) { return (selection.group.empty() || selection.group == kGroup) && (selection.name.empty() || selection.name == name); }
bool run_covered_case() { const int actual = coverage_branch(5); std::cout << "TEST(" << kGroup << ", " << kCoveredCase << ") - 1 ms\n"; return actual == 1; }
bool run_failed_case() { const int actual = coverage_branch(0); std::cout << "TEST(" << kGroup << ", " << kFailedCase << ")\n"; std::cout << "test/math_test.cpp:" << __LINE__ << ": error: Failure in TEST(" << kGroup << ", " << kFailedCase << ")\n\tExpected <1>\n\tbut was  <" << actual << ">\n\n - 2 ms\n"; return false; }
}  // namespace
int main(int argc, char** argv) {
  Selection selection;
  if (!parse_arguments(argc, argv, selection) || (!selection.group.empty() && selection.group != kGroup) || (!selection.name.empty() && selection.name != kCoveredCase && selection.name != kFailedCase)) return 2;
  if (selection.list) { std::cout << kGroup << '.' << kCoveredCase << ' ' << kGroup << '.' << kFailedCase << '\n'; return 0; }
  int tests = 0; int failures = 0;
  if (selected(selection, kCoveredCase)) { ++tests; if (!run_covered_case()) ++failures; }
  if (selected(selection, kFailedCase)) { ++tests; if (!run_failed_case()) ++failures; }
  std::cout << '\n';
  if (failures == 0) { std::cout << "OK (" << tests << " tests, " << tests << " ran, " << tests << " checks, 0 ignored, 0 filtered out, 1 ms)\n"; return 0; }
  std::cout << "Errors (" << failures << " failures, " << tests << " tests, " << tests << " ran, " << tests << " checks, 0 ignored, 0 filtered out, 3 ms)\n"; return 1;
}
#else
#include "CppUTest/CommandLineTestRunner.h"
#include "CppUTest/TestHarness.h"
int coverage_branch(int value);
int coverage_square(int value);
TEST_GROUP(CoverageFixture) {};
TEST(CoverageFixture, coversBranch) { CHECK_EQUAL(1, coverage_branch(5)); CHECK_EQUAL(25, coverage_square(5)); }
TEST(CoverageFixture, failsAfterInstrumentedCode) { CHECK_EQUAL(1, coverage_branch(0)); }
int main(int argc, char** argv) { return CommandLineTestRunner::RunAllTests(argc, argv); }
#endif
