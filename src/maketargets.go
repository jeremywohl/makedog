// Make target menu display and selection.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// makeTargetMenuItem represents a single make target option in the menu.
type makeTargetMenuItem struct {
	key    string
	name   string
	target string
}

// GetKey implements MenuItem interface.
func (m makeTargetMenuItem) GetKey() string {
	return m.key
}

// GetDisplayName implements MenuItem interface.
func (m makeTargetMenuItem) GetDisplayName() string {
	return m.name
}

// parseMakeTargets reads the Makefile and extracts available targets.
// Prefers targets from .PHONY declarations. Falls back to self-discovery if no .PHONY found.
// Excludes 'all' and 'build' targets.
func parseMakeTargets(makefilePath string) ([]string, error) {
	file, err := os.Open(makefilePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	// First, try to parse .PHONY declarations
	phonyTargets := parsePhonyTargets(scanner)
	if len(phonyTargets) > 0 {
		return phonyTargets, nil
	}

	// If no .PHONY found, fall back to self-discovery
	// Reset file pointer for second pass
	file.Seek(0, 0)
	scanner = bufio.NewScanner(file)

	var targets []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines, comments, variable assignments, and .PHONY declarations
		if line == "" || strings.HasPrefix(line, "#") || strings.Contains(line, "=") || strings.HasPrefix(line, ".PHONY") {
			continue
		}

		// Check if line is a target definition (contains ':')
		if strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			target := strings.TrimSpace(parts[0])

			// Skip if target is empty or contains variables/patterns
			if target == "" || strings.Contains(target, "$") || strings.Contains(target, "%") || strings.Contains(target, "(") {
				continue
			}

			// Exclude 'all' and 'build' as they are default targets
			if target == "all" || target == "build" {
				continue
			}

			targets = append(targets, target)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return targets, nil
}

// parsePhonyTargets extracts targets from .PHONY declarations in a Makefile.
// Handles single-line, multi-line (with backslash continuation), and multiple .PHONY lines.
// Returns targets excluding 'all' and 'build'.
func parsePhonyTargets(scanner *bufio.Scanner) []string {
	var targets []string
	var continuedLine string

	for scanner.Scan() {
		line := scanner.Text()

		// Handle line continuation from previous line
		if continuedLine != "" {
			line = continuedLine + " " + strings.TrimSpace(line)
			continuedLine = ""
		}

		// Check if this is a .PHONY declaration
		trimmedLine := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmedLine, ".PHONY") {
			continue
		}

		// Check for backslash continuation
		if strings.HasSuffix(strings.TrimSpace(line), "\\") {
			continuedLine = strings.TrimSuffix(strings.TrimSpace(line), "\\")
			continue
		}

		// Extract targets from .PHONY line
		// Format: .PHONY: target1 target2 target3
		if strings.Contains(trimmedLine, ":") {
			parts := strings.SplitN(trimmedLine, ":", 2)
			if len(parts) == 2 {
				targetsPart := parts[1]

				// Strip comments (everything after #)
				if commentIdx := strings.Index(targetsPart, "#"); commentIdx != -1 {
					targetsPart = targetsPart[:commentIdx]
				}

				// Split by whitespace and collect targets
				for _, target := range strings.Fields(targetsPart) {
					target = strings.TrimSpace(target)
					if target != "" && target != "all" && target != "build" {
						targets = append(targets, target)
					}
				}
			}
		}
	}

	return targets
}

// buildMakeTargetMenu constructs the make target menu from parsed targets.
func buildMakeTargetMenu(targets []string) []makeTargetMenuItem {
	var items []makeTargetMenuItem
	keyIndex := 1

	for _, target := range targets {
		items = append(items, makeTargetMenuItem{
			key:    assignMenuKey(keyIndex),
			name:   target,
			target: target,
		})

		keyIndex++
	}

	return items
}

// printMakeTargetMenu displays the make target selection menu.
func printMakeTargetMenu(items []makeTargetMenuItem) {
	// Build keys, descriptions, and calculate max width
	keys := make([]string, len(items))
	descriptions := make([]string, len(items))
	maxNameWidth := 0

	for i, item := range items {
		keys[i] = item.key
		descriptions[i] = item.name
		if len(item.name) > maxNameWidth {
			maxNameWidth = len(item.name)
		}
	}

	printMenuRows(keys, descriptions, maxNameWidth)
}

// handleMakeTargetMenu displays the make target menu and handles user selection.
func (w *Makedog) handleMakeTargetMenu() step {
	// Parse Makefile targets
	targets, err := parseMakeTargets("Makefile")
	if err != nil {
		reportError("failed to read Makefile: %v", err)
		return step{}
	}

	if len(targets) == 0 {
		printf("no make targets found\n")
		return step{}
	}

	// Build and print make target menu
	reportEvent("Choose a make target (or ESC to cancel)")
	items := buildMakeTargetMenu(targets)
	printMakeTargetMenu(items)

	// Handle user selection
	selected, ok := handleMenuKeySelection(items, w.keyChan)
	if !ok {
		reportEvent("make target cancelled")
		return step{}
	}

	return w.runMakeTarget(selected.target)
}

// runMakeTarget runs a specific make target and optionally restarts the binary.
func (w *Makedog) runMakeTarget(target string) step {
	return step{
		stopBinary:  true,
		action:      func() { runCommand("make", target); println() },
		startBinary: true,
		stopReason:  fmt.Sprintf("make %s requested", target),
	}
}
