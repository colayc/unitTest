#if defined(_WIN32)
#include <windows.h>
#else
#include <unistd.h>
#endif

void phase9_prepare_crash(void)
{
#if defined(_WIN32)
    SetErrorMode(SEM_NOGPFAULTERRORBOX | SEM_FAILCRITICALERRORS);
#endif
}

void phase9_sleep_30_seconds(void)
{
#if defined(_WIN32)
    Sleep(30000);
#else
    sleep(30);
#endif
}
