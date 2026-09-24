#if defined(_WIN32)
#include <windows.h>
#include <stdlib.h>
#else
#include <unistd.h>
#endif

void phase9_prepare_crash(void)
{
#if defined(_WIN32)
    SetErrorMode(SEM_NOGPFAULTERRORBOX | SEM_FAILCRITICALERRORS);
    _set_abort_behavior(0, _WRITE_ABORT_MSG | _CALL_REPORTFAULT);
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
