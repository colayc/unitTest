#include <stdio.h>

static int branch(int value) {
  if (value > 0) {
    return value;
  }
  return -value;
}

int main(void) {
  printf("%d\n", branch(-2));
  return 0;
}
