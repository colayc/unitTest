package testrun

import (
	"errors"
	"reflect"
	"testing"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestSchedulerBoundsConcurrencyAndSerializesEachContainer(t *testing.T) {
	containerA := scheduleID(t, "project", "container-a")
	containerB := scheduleID(t, "project", "container-b")
	containerC := scheduleID(t, "project", "container-c")
	jobs := []ScheduledJob{
		{ID: "a-2", ContainerID: containerA, Iteration: 2},
		{ID: "c-1", ContainerID: containerC, Iteration: 1},
		{ID: "a-1", ContainerID: containerA, Iteration: 1},
		{ID: "b-1", ContainerID: containerB, Iteration: 1},
	}

	waves, err := BuildSchedule(jobs, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertValidSchedule(t, waves, jobs, 2)
	if len(waves) != 2 ||
		len(waves[0].Jobs) != 2 ||
		len(waves[1].Jobs) != 2 {
		t.Fatalf("waves = %#v", waves)
	}
	var aIterations []int64
	for _, wave := range waves {
		for _, job := range wave.Jobs {
			if job.ContainerID == containerA {
				aIterations = append(aIterations, job.Iteration)
			}
		}
	}
	if !reflect.DeepEqual(aIterations, []int64{1, 2}) {
		t.Fatalf("container A iterations = %v", aIterations)
	}
}

func TestSchedulerGivesRunSerialJobAnExclusiveWave(t *testing.T) {
	containerA := scheduleID(t, "project", "container-a")
	containerB := scheduleID(t, "project", "container-b")
	containerC := scheduleID(t, "project", "container-c")
	jobs := []ScheduledJob{
		{ID: "a-1", ContainerID: containerA, Iteration: 1},
		{
			ID: "b-1", ContainerID: containerB, Iteration: 1,
			RunSerial: true,
		},
		{ID: "c-1", ContainerID: containerC, Iteration: 1},
	}

	waves, err := BuildSchedule(jobs, 3)
	if err != nil {
		t.Fatal(err)
	}
	assertValidSchedule(t, waves, jobs, 3)
	found := false
	for _, wave := range waves {
		for _, job := range wave.Jobs {
			if job.ID != "b-1" {
				continue
			}
			found = true
			if len(wave.Jobs) != 1 {
				t.Fatalf("RUN_SERIAL wave = %#v", wave)
			}
		}
	}
	if !found {
		t.Fatal("RUN_SERIAL job was not scheduled")
	}
}

func TestSchedulerIsDeterministicAcrossInputOrder(t *testing.T) {
	containerA := scheduleID(t, "project", "container-a")
	containerB := scheduleID(t, "project", "container-b")
	first := []ScheduledJob{
		{ID: "b-1", ContainerID: containerB, Iteration: 1},
		{ID: "a-2", ContainerID: containerA, Iteration: 2},
		{ID: "a-1", ContainerID: containerA, Iteration: 1},
	}
	second := []ScheduledJob{first[2], first[0], first[1]}

	firstWaves, err := BuildSchedule(first, 2)
	if err != nil {
		t.Fatal(err)
	}
	secondWaves, err := BuildSchedule(second, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstWaves, secondWaves) {
		t.Fatalf(
			"nondeterministic waves:\nfirst=%#v\nsecond=%#v",
			firstWaves,
			secondWaves,
		)
	}
}

func TestSchedulerRejectsInvalidOrDuplicateJobs(t *testing.T) {
	container := scheduleID(t, "project", "container")
	tests := map[string]struct {
		jobs        []ScheduledJob
		concurrency int
	}{
		"zero concurrency": {
			jobs:        []ScheduledJob{{ID: "job", ContainerID: container, Iteration: 1}},
			concurrency: 0,
		},
		"invalid container": {
			jobs:        []ScheduledJob{{ID: "job", ContainerID: "invalid", Iteration: 1}},
			concurrency: 1,
		},
		"invalid iteration": {
			jobs:        []ScheduledJob{{ID: "job", ContainerID: container}},
			concurrency: 1,
		},
		"duplicate ID": {
			jobs: []ScheduledJob{
				{ID: "job", ContainerID: container, Iteration: 1},
				{ID: "job", ContainerID: container, Iteration: 2},
			},
			concurrency: 1,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := BuildSchedule(
				test.jobs,
				test.concurrency,
			); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("BuildSchedule() error = %v", err)
			}
		})
	}
}

func assertValidSchedule(
	t *testing.T,
	waves []ScheduleWave,
	expected []ScheduledJob,
	maxConcurrency int,
) {
	t.Helper()
	seen := make(map[string]struct{}, len(expected))
	for _, wave := range waves {
		if len(wave.Jobs) == 0 || len(wave.Jobs) > maxConcurrency {
			t.Fatalf("invalid wave size: %#v", wave)
		}
		containers := make(map[testdomain.ID]struct{}, len(wave.Jobs))
		for _, job := range wave.Jobs {
			if _, duplicate := seen[job.ID]; duplicate {
				t.Fatalf("job %q scheduled more than once", job.ID)
			}
			seen[job.ID] = struct{}{}
			if _, duplicate := containers[job.ContainerID]; duplicate {
				t.Fatalf("container scheduled twice in one wave: %#v", wave)
			}
			containers[job.ContainerID] = struct{}{}
			if job.RunSerial && len(wave.Jobs) != 1 {
				t.Fatalf("RUN_SERIAL job shares a wave: %#v", wave)
			}
		}
	}
	if len(seen) != len(expected) {
		t.Fatalf("scheduled %d jobs, want %d", len(seen), len(expected))
	}
}

func scheduleID(t *testing.T, projectID, name string) testdomain.ID {
	t.Helper()
	id, err := testdomain.ContainerID(projectID, name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
