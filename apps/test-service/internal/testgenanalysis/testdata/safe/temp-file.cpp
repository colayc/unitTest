struct File;
extern "C" File *fopen(const char *, const char *);
extern "C" int fputs(const char *, File *);
extern "C" int fclose(File *);
int write_temporary(const char *path) {
    File *file = fopen(path, "w");
    if (!file) return -1;
    int written = fputs("ok", file);
    fclose(file);
    return written;
}
