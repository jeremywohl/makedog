// Simple test program for makedog testing.
// Outputs a message every second and runs until terminated.
package main

import (
	"fmt"
	"time"
)

func main() {
	fmt.Println("Test program started")

	counter := 0
	for {
		counter++
		fmt.Printf("Iteration %d at %s\n", counter, time.Now().Format("15:04:05"))
		time.Sleep(1 * time.Second)
	}
}
