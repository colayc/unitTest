int unity_coverage_branch(int value) {
  if (value > 0) {
    return value + 1;
  }
  return 0;
}

int unity_coverage_square(int value) {
  return value * value;
}
