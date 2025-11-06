// Generic menu display and selection utilities.
package main

import (
	"fmt"
	"strings"
)

// MenuItem represents an item in a selectable menu.
type MenuItem interface {
	GetKey() string
	GetDisplayName() string
}

// assignMenuKey generates a menu key based on the index.
// Keys are assigned as: 1-9, then a1-a9, b1-b9, etc.
func assignMenuKey(keyIndex int) string {
	if keyIndex <= 9 {
		return fmt.Sprintf("%d", keyIndex)
	}

	prefix := byte('a') + byte((keyIndex-10)/9)
	digit := ((keyIndex - 10) % 9) + 1
	return fmt.Sprintf("%c%d", prefix, digit)
}

// handleMenuKeySelection handles user key selection for a menu.
// Returns the selected item and true if successful, or zero value and false if cancelled.
func handleMenuKeySelection[T MenuItem](items []T, keyChan chan byte) (T, bool) {
	var zero T

	// Wait for user selection
	firstKey := <-keyChan

	// ESC key - cancel
	if firstKey == 27 {
		return zero, false
	}

	// Check for single-digit keys and partial multi-char matches
	foundPartialMatch := false
	for _, item := range items {
		if len(item.GetKey()) == 1 && firstKey == item.GetKey()[0] {
			return item, true
		}
		if len(item.GetKey()) > 1 && firstKey == item.GetKey()[0] {
			foundPartialMatch = true
		}
	}

	// Only wait for second key if first key matched a multi-char prefix
	if foundPartialMatch {
		secondKey := <-keyChan
		fullKey := string([]byte{firstKey, secondKey})

		for _, item := range items {
			if item.GetKey() == fullKey {
				return item, true
			}
		}
	}

	return zero, false
}

// printMenuRows prints menu items in a grid layout based on terminal width.
func printMenuRows(itemKeys []string, itemDescriptions []string, maxDescWidth int) {
	// Get terminal width to determine items per row
	cols := 80
	if width, _, err := getTermSize(); err == nil {
		cols = width
	}

	// Calculate how many items can fit per row (max 9)
	const marginLeft = 2                  // Leading "  "
	const maxItemsPerRow = 9              // Never exceed 9 items per row
	itemWidth := 2 + 2 + maxDescWidth + 1 // key(2) + ": "(2) + desc + space(1)

	availableWidth := cols - marginLeft
	itemsPerRow := availableWidth / itemWidth
	if itemsPerRow < 1 {
		itemsPerRow = 1
	}
	if itemsPerRow > maxItemsPerRow {
		itemsPerRow = maxItemsPerRow
	}

	// Print all rows
	for i := 0; i < len(itemKeys); i += itemsPerRow {
		end := i + itemsPerRow
		if end > len(itemKeys) {
			end = len(itemKeys)
		}

		var rowItems []string
		for j := i; j < end; j++ {
			rowItems = append(rowItems, fmt.Sprintf("%2s: %-*s", itemKeys[j], maxDescWidth, itemDescriptions[j]))
		}

		printf("  %s\n", strings.Join(rowItems, " "))
	}
}
