#define SIDE_EFFECT(x) system(x)
extern "C" int system(const char *);
int hidden_effect() { return SIDE_EFFECT("echo unsafe"); }
