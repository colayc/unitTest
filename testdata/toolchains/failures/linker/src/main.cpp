#if defined(_MSC_VER)
#pragma detect_mismatch("native_missing_symbol", "one")

int main() {
  return 0;
}
#else
int native_missing_symbol();

int main() {
  return native_missing_symbol();
}
#endif
