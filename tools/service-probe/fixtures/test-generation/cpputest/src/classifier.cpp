#include "classifier.hpp"

Classification classify(int value) noexcept {
  if (value < 0) return Classification::negative;
  if (value == 0) return Classification::zero;
  return Classification::positive;
}
