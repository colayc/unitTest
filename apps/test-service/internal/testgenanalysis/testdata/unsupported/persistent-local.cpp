int external_counter;

int static_counter() {
    static int counter = 0;
    counter = 1;
    return counter;
}

int thread_counter() {
    thread_local int counter = 0;
    counter = 1;
    return counter;
}

int external_counter_write() {
    extern int external_counter;
    external_counter = 1;
    return external_counter;
}
