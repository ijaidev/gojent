package task_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ijaidev/gojent/tools/task"
)

func TestTaskTracker_Lifecycle(t *testing.T) {
	tracker := task.NewTracker()
	tools, err := tracker.AllTools()
	if err != nil {
		t.Fatalf("failed to get all tools: %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}

	createTool := tools[0]
	listTool := tools[1]
	updateTool := tools[2]

	ctx := context.Background()

	// 1. Create first task
	create1, _ := json.Marshal(task.CreateTaskInput{
		Title:       "Research API",
		Description: "Find endpoint specs",
	})
	resCreate1Any, err := createTool.Execute(ctx, create1)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	task1 := resCreate1Any.Data.(task.Task)
	if task1.ID != "1" || task1.Status != task.StatusPending {
		t.Errorf("unexpected task1: %+v", task1)
	}

	// 2. Create second task
	create2, _ := json.Marshal(task.CreateTaskInput{
		Title: "Write Tests",
	})
	resCreate2Any, err := createTool.Execute(ctx, create2)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	task2 := resCreate2Any.Data.(task.Task)
	if task2.ID != "2" {
		t.Errorf("unexpected task2 ID: %s", task2.ID)
	}

	// 3. Update task 1 to in_progress
	update1, _ := json.Marshal(task.UpdateTaskInput{
		ID:     "1",
		Status: task.StatusInProgress,
	})
	resUpdate1Any, err := updateTool.Execute(ctx, update1)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	updated1 := resUpdate1Any.Data.(task.Task)
	if updated1.Status != task.StatusInProgress {
		t.Errorf("expected status in_progress, got %s", updated1.Status)
	}

	// 4. List filtered by status
	listPending, _ := json.Marshal(task.ListTasksInput{
		Status: task.StatusPending,
	})
	resListPendingAny, err := listTool.Execute(ctx, listPending)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	listPendingRes := resListPendingAny.Data.(task.ListTasksOutput)
	if listPendingRes.Total != 1 || listPendingRes.Tasks[0].ID != "2" {
		t.Errorf("expected only task 2 in pending, got: %+v", listPendingRes)
	}

	// 5. List all
	listAll, _ := json.Marshal(task.ListTasksInput{})
	resListAllAny, err := listTool.Execute(ctx, listAll)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	listAllRes := resListAllAny.Data.(task.ListTasksOutput)
	if listAllRes.Total != 2 {
		t.Errorf("expected 2 tasks, got %d", listAllRes.Total)
	}
}
