package testrun

import (
	"sort"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

const (
	maxScheduleConcurrency = 256
	maxScheduledJobs       = 100_000
)

type ScheduledJob struct {
	ID          string
	ContainerID testdomain.ID
	Iteration   int64
	RunSerial   bool
}

type ScheduleWave struct {
	Jobs []ScheduledJob
}

func BuildSchedule(
	values []ScheduledJob,
	maxConcurrency int,
) ([]ScheduleWave, error) {
	if len(values) == 0 || len(values) > maxScheduledJobs ||
		maxConcurrency < 1 ||
		maxConcurrency > maxScheduleConcurrency {
		return nil, task.ErrInvalidArgument
	}
	jobs := append([]ScheduledJob(nil), values...)
	seen := make(map[string]struct{}, len(jobs))
	for _, job := range jobs {
		if !validScheduledJobID(job.ID) ||
			!testdomain.ValidID(job.ContainerID) ||
			job.Iteration < 1 ||
			job.Iteration > MaxRepeatCount {
			return nil, task.ErrInvalidArgument
		}
		if _, duplicate := seen[job.ID]; duplicate {
			return nil, task.ErrInvalidArgument
		}
		seen[job.ID] = struct{}{}
	}
	sort.Slice(jobs, func(left, right int) bool {
		if jobs[left].ContainerID != jobs[right].ContainerID {
			return jobs[left].ContainerID < jobs[right].ContainerID
		}
		if jobs[left].Iteration != jobs[right].Iteration {
			return jobs[left].Iteration < jobs[right].Iteration
		}
		return jobs[left].ID < jobs[right].ID
	})

	queues := make(map[testdomain.ID][]ScheduledJob)
	active := make([]testdomain.ID, 0)
	for _, job := range jobs {
		if len(queues[job.ContainerID]) == 0 {
			active = append(active, job.ContainerID)
		}
		queues[job.ContainerID] = append(
			queues[job.ContainerID],
			job,
		)
	}
	waves := make([]ScheduleWave, 0)
	for len(active) != 0 {
		sort.Slice(active, func(left, right int) bool {
			leftCount := len(queues[active[left]])
			rightCount := len(queues[active[right]])
			if leftCount != rightCount {
				return leftCount > rightCount
			}
			return active[left] < active[right]
		})
		if index := firstExclusiveQueue(active, queues); index >= 0 {
			containerID := active[index]
			job := queues[containerID][0]
			queues[containerID] = queues[containerID][1:]
			active = rotateConsumedQueue(active, index, len(queues[containerID]) != 0)
			waves = append(waves, ScheduleWave{
				Jobs: []ScheduledJob{job},
			})
			continue
		}

		count := min(maxConcurrency, len(active))
		wave := ScheduleWave{
			Jobs: make([]ScheduledJob, 0, count),
		}
		selected := append([]testdomain.ID(nil), active[:count]...)
		active = append([]testdomain.ID(nil), active[count:]...)
		for _, containerID := range selected {
			queue := queues[containerID]
			wave.Jobs = append(wave.Jobs, queue[0])
			queue = queue[1:]
			queues[containerID] = queue
			if len(queue) != 0 {
				active = append(active, containerID)
			}
		}
		waves = append(waves, wave)
	}
	return waves, nil
}

func firstExclusiveQueue(
	active []testdomain.ID,
	queues map[testdomain.ID][]ScheduledJob,
) int {
	for index, containerID := range active {
		if queues[containerID][0].RunSerial {
			return index
		}
	}
	return -1
}

func rotateConsumedQueue(
	active []testdomain.ID,
	index int,
	remaining bool,
) []testdomain.ID {
	containerID := active[index]
	result := append([]testdomain.ID(nil), active[:index]...)
	result = append(result, active[index+1:]...)
	if remaining {
		result = append(result, containerID)
	}
	return result
}

func validScheduledJobID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' ||
			index > 0 && character >= '0' && character <= '9' ||
			index > 0 && (character == '-' || character == '_') {
			continue
		}
		return false
	}
	return true
}
