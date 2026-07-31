#ifndef UNIT_TEST_IDE_HELPER_SMOKE_UNITY_H
#define UNIT_TEST_IDE_HELPER_SMOKE_UNITY_H

typedef unsigned int UNITY_COUNTER_TYPE;
typedef unsigned int UNITY_LINE_TYPE;

typedef struct UNITY_STORAGE_T {
    UNITY_COUNTER_TYPE TestFailures;
    UNITY_COUNTER_TYPE TestIgnores;
} UNITY_STORAGE_T;

extern UNITY_STORAGE_T Unity;

void UnityBegin(const char *source);
void UnityDefaultTestRun(
    void (*test_function)(void),
    const char *name,
    UNITY_LINE_TYPE line);
int UnityEnd(void);

#endif
