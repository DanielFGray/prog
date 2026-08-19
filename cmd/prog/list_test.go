package main

import (
	"strings"
	"testing"
	"time"

	"github.com/baiirun/prog/internal/model"
)

func TestPrintItemsTable_IncludesProject(t *testing.T) {
	items := []model.Item{
		{
			ID: "ts-list01", Project: "prog", Type: model.ItemTypeTask,
			Title: "List Task 1", Status: model.StatusOpen, Priority: 1,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		},
		{
			ID: "ts-list02", Project: "gaia", Type: model.ItemTypeTask,
			Title: "List Task 2", Status: model.StatusDone, Priority: 2,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		},
	}

	output := captureOutput(func() {
		printItemsTable(items)
	})

	if !strings.Contains(output, "PROJECT") {
		t.Errorf("table header should contain PROJECT, got:\n%s", output)
	}
	if !strings.Contains(output, "prog") || !strings.Contains(output, "gaia") {
		t.Errorf("table rows should include each item's project, got:\n%s", output)
	}
}
