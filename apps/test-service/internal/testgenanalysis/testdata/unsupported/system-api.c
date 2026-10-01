extern int system(const char *);
extern int socket(int, int, int);
int unsafe_api(void) { return system("echo unsafe") + socket(1, 1, 0); }
