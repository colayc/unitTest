#if defined(_MSC_VER)
#pragma detect_mismatch("native_missing_symbol", "two")

int linker_mismatch_anchor() {
  return 0;
}
#endif
