#include "classifier.h"

int classify(int value) {
  if (value < 0) return -1;
  if (value == 0) return 0;
  return 1;
}
