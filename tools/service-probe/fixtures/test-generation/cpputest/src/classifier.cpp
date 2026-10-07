#include "classifier.hpp"

int classify(int value) noexcept {
  if (value < 0) return -1;
  if (value == 0) return 0;
  return 1;
}
