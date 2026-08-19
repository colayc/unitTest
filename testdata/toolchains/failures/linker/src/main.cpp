#if defined(_MSC_VER)
extern "C" int native_missing_symbol();

int main() {
  return native_missing_symbol();
}
#else
int native_missing_symbol();

int main() {
  return native_missing_symbol();
}
#endif
