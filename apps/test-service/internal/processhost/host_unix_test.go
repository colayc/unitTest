//go:build linux

package processhost

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"unit-test-ide.local/test-service/internal/processcontrol"
)

const identityFailureHelperEnvironment = "UNIT_TEST_PROCESSHOST_IDENTITY_FAILURE_HELPER"

func TestLinuxIdentityFailureHelper(t *testing.T) {
	if os.Getenv(identityFailureHelperEnvironment) == "" {
		return
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, unix.SIGTERM, unix.SIGINT)
	defer signal.Stop(signals)
	select {
	case <-signals:
	case <-time.After(time.Minute):
	}
}

func TestLinuxTargetIdentityCaptureFailureKillsAndReapsTarget(t *testing.T) {
	startedPID := 0
	platform := newUnixPlatform(func(pid int) (string, error) {
		startedPID = pid
		return "", errors.New("injected identity failure")
	})
	platform.sessionID = func() (int, error) { return 456, nil }
	target, err := platform.Start(processcontrol.Spec{
		Executable: os.Args[0],
		Args:       []string{"-test.run=^TestLinuxIdentityFailureHelper$"},
		Env:        []string{identityFailureHelperEnvironment + "=1"},
	}, io.Discard, io.Discard)
	if err == nil || target != nil {
		t.Fatalf("Start = (%#v, %v), want nil target and error", target, err)
	}
	if startedPID <= 1 {
		t.Fatalf("started PID = %d", startedPID)
	}
	if err := unix.Kill(startedPID, 0); !errors.Is(err, unix.ESRCH) {
		t.Fatalf("target %d was not killed and reaped: %v", startedPID, err)
	}
}

func TestLinuxTargetGroupScanFailsClosedOnUnreadableOrMalformedStat(t *testing.T) {
	for _, test := range []struct {
		name string
		read func(string) ([]byte, error)
	}{
		{name: "unreadable", read: func(string) ([]byte, error) { return nil, fs.ErrPermission }},
		{name: "malformed", read: func(string) ([]byte, error) { return []byte("malformed"), nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := scanProcessGroupOwned([]string{"123"}, test.read, 123, 456, "identity")
			if err == nil {
				t.Fatal("scan accepted unverifiable process identity")
			}
		})
	}
}

func TestLinuxTargetGroupScanAcceptsUnrelatedKernelThreadStat(t *testing.T) {
	records := map[string][]byte{
		"2":   linuxTestStat(2, 0, 0, "10"),
		"123": linuxTestStat(123, 123, 456, "20"),
	}
	exists, owned, err := scanProcessGroupOwned([]string{"2", "123"}, func(path string) ([]byte, error) {
		return records[strings.Split(path, "/")[2]], nil
	}, 123, 456, "20")
	if err != nil || !exists || !owned {
		t.Fatalf("scan = (%t, %t, %v), want owned group", exists, owned, err)
	}
}

func TestLinuxTargetGroupScanRejectsChangedLeaderIdentity(t *testing.T) {
	stat := linuxTestStat(123, 123, 456, "20")
	exists, owned, err := scanProcessGroupOwned([]string{strconv.Itoa(123)}, func(string) ([]byte, error) {
		return stat, nil
	}, 123, 456, "10")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || owned {
		t.Fatalf("scan = (exists=%t, owned=%t), want changed leader rejected", exists, owned)
	}
}

func TestLinuxTargetLeaderIdentityIsRereadImmediatelyBeforeSignal(t *testing.T) {
	err := validateTargetLeaderIdentity(123, "10", func(string) ([]byte, error) {
		return linuxTestStat(123, 123, 456, "20"), nil
	})
	if err == nil {
		t.Fatal("changed target leader identity was accepted")
	}
}

func linuxTestStat(pid, group, session int, identity string) []byte {
	fields := []string{"S", "1", strconv.Itoa(group), strconv.Itoa(session)}
	for len(fields) < 19 {
		fields = append(fields, "0")
	}
	fields = append(fields, identity)
	return []byte(fmt.Sprintf("%d (target) %s\n", pid, strings.Join(fields, " ")))
}
