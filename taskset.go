package dstask

// main task data structures

import (
	"io/ioutil"
	"os"
	"path"
	"sort"
	"time"

	yaml "gopkg.in/yaml.v2"
)

type TaskSet struct {
	tasks []*Task

	// indices
	tasksByID   map[int]*Task
	tasksByUUID map[string]*Task

	// task count before filters
	tasksLoaded int

	// critical tasks
	tasksLoadedCritical int
}

type Project struct {
	Name          string
	Tasks         int
	TasksResolved int
	// if any task is in the active state
	Active bool
	// first task created
	Created time.Time
	// last task resolved
	Resolved time.Time

	// highest non-resolved priority within project
	Priority string
}

func (ts *TaskSet) SortByPriority() {
	sort.SliceStable(ts.tasks, func(i, j int) bool { return ts.tasks[i].Created.Before(ts.tasks[j].Created) })
	sort.SliceStable(ts.tasks, func(i, j int) bool { return ts.tasks[i].Priority < ts.tasks[j].Priority })
}

func (ts *TaskSet) SortByResolved() {
	sort.SliceStable(ts.tasks, func(i, j int) bool { return ts.tasks[i].Resolved.Before(ts.tasks[j].Resolved) })
}

// add a task, but only if it has a new uuid or no uuid. Return annotated task.
func (ts *TaskSet) AddTask(task Task) Task {
	task.Normalise()

	if task.UUID == "" {
		task.UUID = MustGetUUID4String()
	}

	if err := task.Validate(); err != nil {
		ExitFail("%s, task %s", err, task.UUID)
	}

	if ts.tasksByUUID[task.UUID] != nil {
		// load tasks, do not overwrite
		return Task{}
	}

	// check ID is unique if there is one
	if task.ID > 0 && ts.tasksByID[task.ID] != nil {
		task.ID = 0
	}

	// pick one if task isn't resolved and ID isn't there
	if task.ID == 0 && task.Status != STATUS_RESOLVED {
		for id := 1; id <= MAX_TASKS_OPEN; id++ {
			if ts.tasksByID[id] == nil {
				task.ID = id
				task.WritePending = true
				break
			}
		}
	}

	if task.Created.IsZero() {
		task.Created = time.Now()
		task.WritePending = true
	}

	ts.tasks = append(ts.tasks, &task)
	ts.tasksByUUID[task.UUID] = &task
	ts.tasksByID[task.ID] = &task
	ts.tasksLoaded += 1

	if task.Priority == PRIORITY_CRITICAL {
		ts.tasksLoadedCritical += 1
	}

	return task
}

// TODO maybe this is the place to check for invalid state transitions instead
// of the main switch statement. Though, a future 3rdparty sync system could
// need this to work regardless.
func (ts *TaskSet) MustUpdateTask(task Task) {
	task.Normalise()

	if err := task.Validate(); err != nil {
		ExitFail("%s, task %s", err, task.UUID)
	}

	if ts.tasksByUUID[task.UUID] == nil {
		ExitFail("Could not find given task to update by UUID")
	}

	if !IsValidPriority(task.Priority) {
		ExitFail("Invalid priority specified")
	}

	old := ts.tasksByUUID[task.UUID]

	if old.Status != task.Status && !IsValidStateTransition(old.Status, task.Status) {
		ExitFail("Invalid state transition: %s -> %s", old.Status, task.Status)
	}

	if task.Status == STATUS_RESOLVED {
		task.ID = 0
	}

	if task.Status == STATUS_RESOLVED && task.Resolved.IsZero() {
		task.Resolved = time.Now()
	}

	task.WritePending = true
	// existing pointer must point to address of new task copied
	*ts.tasksByUUID[task.UUID] = task
}

func (ts *TaskSet) Filter(cmdLine CmdLine) {
	var tasks []*Task

	for _, task := range ts.tasks {
		if task.MatchesFilter(cmdLine) {
			tasks = append(tasks, task)
		}
	}

	ts.tasks = tasks
}

func (ts *TaskSet) FilterByStatus(status string) {
	var tasks []*Task

	for _, task := range ts.tasks {
		if task.Status == status {
			tasks = append(tasks, task)
		}
	}

	ts.tasks = tasks
}

func (ts *TaskSet) FilterUnorganised() {
	var tasks []*Task

	for _, task := range ts.tasks {
		if len(task.Tags) == 0 && task.Project == "" {
			tasks = append(tasks, task)
		}
	}

	ts.tasks = tasks
}

func (ts *TaskSet) MustGetByID(id int) Task {
	if ts.tasksByID[id] == nil {
		ExitFail("No open task with ID %v exists.", id)
	}

	return *ts.tasksByID[id]
}

func (ts *TaskSet) Tasks() []Task {
	tasks := make([]Task, 0, len(ts.tasks))
	for _, task := range ts.tasks {
		tasks = append(tasks, *task)
	}
	return tasks
}

func (ts *TaskSet) GetTags() map[string]bool {
	tagset := make(map[string]bool)

	for _, task := range ts.tasks {
		for _, tag := range task.Tags {
			tagset[tag] = true
		}
	}

	return tagset
}

func (ts *TaskSet) GetProjects() map[string]*Project {
	projects := make(map[string]*Project)

	for _, task := range ts.tasks {
		name := task.Project

		if name == "" {
			continue
		}

		if projects[name] == nil {
			projects[name] = &Project{
				Name:     name,
				Priority: PRIORITY_LOW,
			}
		}

		project := projects[name]

		project.Tasks += 1

		if project.Created.IsZero() || task.Created.Before(project.Created) {
			project.Created = task.Created
		}

		if task.Resolved.After(project.Resolved) {
			project.Resolved = task.Resolved
		}

		if task.Status == STATUS_RESOLVED {
			project.TasksResolved += 1
		}

		if task.Status == STATUS_ACTIVE {
			project.Active = true
		}

		if task.Status != STATUS_RESOLVED && task.Priority < project.Priority {
			project.Priority = task.Priority
		}
	}

	return projects
}

// save pending changes to disk
// TODO return files that have been added/deleted/modified/renamed so they can
// be passed to git add for performance, instead of doing git add .
func (ts *TaskSet) SavePendingChanges() {
	for _, task := range ts.tasks {
		if task.WritePending {
			task.SaveToDisk()
		}
	}
}

func LoadTaskSetFromDisk(statuses []string) *TaskSet {
	ts := &TaskSet{
		tasksByID:   make(map[int]*Task),
		tasksByUUID: make(map[string]*Task),
	}

	gitDotGitLocation := MustExpandHome(path.Join(GIT_REPO, ".git"))

	if _, err := os.Stat(gitDotGitLocation); os.IsNotExist(err) {
		ExitFail("Could not find git repository at " + GIT_REPO + ", please clone or create. Try `dstask help` for more information.")
	}

	for _, status := range statuses {
		dir := MustGetRepoPath(status, "")

		files, err := ioutil.ReadDir(dir)
		if err != nil {
			ExitFail("Failed to read " + dir)
		}

		for _, file := range files {
			filepath := path.Join(dir, file.Name())

			if len(file.Name()) != 40 {
				// not <uuid4>.yml
				continue
			}

			uuid := file.Name()[0:36]

			if !IsValidUUID4String(uuid) {
				continue
			}

			t := Task{
				UUID:   uuid,
				Status: status,
			}

			data, err := ioutil.ReadFile(filepath)
			if err != nil {
				ExitFail("Failed to read %s", filepath)
			}
			err = yaml.Unmarshal(data, &t)
			if err != nil {
				// TODO present error to user, specific error message is important
				ExitFail("Failed to unmarshal %s", filepath)
			}

			ts.AddTask(t)
		}
	}

	return ts
}
