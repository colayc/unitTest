enum Mode { MODE_ZERO, MODE_ONE };
int choose(enum Mode mode, int x) {
    if (mode == MODE_ZERO && x < 4) return 1;
    switch (mode) {
    case MODE_ONE: return 2;
    default: return 0;
    }
}
