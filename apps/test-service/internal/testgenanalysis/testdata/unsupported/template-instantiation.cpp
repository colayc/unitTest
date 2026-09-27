template <typename T> T identity(T value) { return value; }
int use_template() { return identity(3); }
