int coverage_branch(int value) {
  if (value > 10) {
    return 2;
  }
  if (value > 0) {
    return 1;
  }
  return 0;
}
