#include "classifier.h"

Classification classify(int value) {
  if (value < 0) return CLASSIFICATION_NEGATIVE;
  if (value == 0) return CLASSIFICATION_ZERO;
  return CLASSIFICATION_POSITIVE;
}
