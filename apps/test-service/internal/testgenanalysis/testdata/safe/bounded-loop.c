int sum_first_four(int value) {
    int values[4] = { value, value, value, value };
    int total = 0;
    for (int i = 0; i < 4; ++i) total += values[i];
    return total;
}
