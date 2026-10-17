// Package task provides readymade planning and task-tracking tools for multi-step agent reasoning.
package task

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/ijaidev/gojent/core/tool"
)

// Status represents the lifecycle status of a task.
type Status string

const (
	// StatusPending indicates a task that has been planned but not yet started.
	StatusPending Status = "pending"
	// StatusInProgress indicates a task that is currently being executed.
	StatusInProgress Status = "in_progress"
	// StatusCompleted indicates a task that has finished successfully.
	StatusCompleted Status = "completed"
	// StatusCancelled indicates a task that was discarded or aborted.
	StatusCancelled Status = "cancelled"
)

// Task represents an individual task in a plan.
type Task struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Status      Status `json:"status"`
}

// Tracker provides thread-safe in-memory task management.
type Tracker struct {
	mu     sync.RWMutex
	tasks  map[string]*Task
	order  []string
	nextID int
}

// NewTracker creates an empty in-memory task tracker.
func NewTracker() *Tracker {
	return &Tracker{
		tasks: make(map[string]*Task),
	}
}

// CreateTaskInput defines parameters for creating a new task.
type CreateTaskInput struct {
	Title       string `json:"title" description:"Title or brief summary of the task"`
	Description string `json:"description,omitempty" description:"Detailed description or requirements for the task"`
}

// ListTasksInput defines parameters for listing tasks.
type ListTasksInput struct {
	Status Status `json:"status,omitempty" description:"Optional filter by status (pending, in_progress, completed, cancelled)"`
}

// ListTasksOutput represents the list of tasks.
type ListTasksOutput struct {
	Tasks []Task `json:"tasks"`
	Total int    `json:"total"`
}

// UpdateTaskInput defines parameters for updating a task.
type UpdateTaskInput struct {
	ID     string `json:"id" description:"The unique task ID to update"`
	Status Status `json:"status,omitempty" description:"New status (pending, in_progress, completed, cancelled)"`
	Title  string `json:"title,omitempty" description:"Updated task title"`
}

// NewCreateTaskTool returns a tool for adding a new task to the plan.
func (t *Tracker) NewCreateTaskTool() (tool.Tool, error) {
	return tool.New(tool.Config[CreateTaskInput, Task]{
		ID:          "task_create",
		Description: "Create a new task in the execution plan.",
		Execute: func(_ context.Context, in CreateTaskInput) (Task, error) {
			if in.Title == "" {
				return Task{}, fmt.Errorf("task_create: title cannot be empty")
			}

			t.mu.Lock()
			defer t.mu.Unlock()

			t.nextID++
			id := strconv.Itoa(t.nextID)

			task := &Task{
				ID:          id,
				Title:       in.Title,
				Description: in.Description,
				Status:      StatusPending,
			}

			t.tasks[id] = task
			t.order = append(t.order, id)

			return *task, nil
		},
	})
}

// NewListTasksTool returns a tool for listing current tasks.
func (t *Tracker) NewListTasksTool() (tool.Tool, error) {
	return tool.New(tool.Config[ListTasksInput, ListTasksOutput]{
		ID:          "task_list",
		Description: "List all tasks in the current plan, optionally filtered by status.",
		Execute: func(_ context.Context, in ListTasksInput) (ListTasksOutput, error) {
			t.mu.RLock()
			defer t.mu.RUnlock()

			var matched []Task
			for _, id := range t.order {
				task := t.tasks[id]
				if in.Status != "" && task.Status != in.Status {
					continue
				}
				matched = append(matched, *task)
			}

			return ListTasksOutput{
				Tasks: matched,
				Total: len(matched),
			}, nil
		},
	})
}

// NewUpdateTaskTool returns a tool for modifying a task's status or details.
func (t *Tracker) NewUpdateTaskTool() (tool.Tool, error) {
	return tool.New(tool.Config[UpdateTaskInput, Task]{
		ID:          "task_update",
		Description: "Update a task's status (pending, in_progress, completed, cancelled) or title.",
		Execute: func(_ context.Context, in UpdateTaskInput) (Task, error) {
			if in.ID == "" {
				return Task{}, fmt.Errorf("task_update: task id is required")
			}

			t.mu.Lock()
			defer t.mu.Unlock()

			task, exists := t.tasks[in.ID]
			if !exists {
				return Task{}, fmt.Errorf("task_update: task %q not found", in.ID)
			}

			if in.Status != "" {
				task.Status = in.Status
			}
			if in.Title != "" {
				task.Title = in.Title
			}

			return *task, nil
		},
	})
}

// AllTools returns all task management tools (task_create, task_list, task_update).
func (t *Tracker) AllTools() ([]tool.Tool, error) {
	createTool, err := t.NewCreateTaskTool()
	if err != nil {
		return nil, err
	}
	listTool, err := t.NewListTasksTool()
	if err != nil {
		return nil, err
	}
	updateTool, err := t.NewUpdateTaskTool()
	if err != nil {
		return nil, err
	}
	return []tool.Tool{createTool, listTool, updateTool}, nil
}
