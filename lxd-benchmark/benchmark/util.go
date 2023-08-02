package benchmark

import (
	"fmt"
	"time"
)

// Formats a container name using the provided count and index.
func getContainerName(count int, index int) string {
	nameFormat := "benchmark-%." + fmt.Sprintf("%d", len(fmt.Sprintf("%d", count))) + "d"
	return fmt.Sprintf(nameFormat, index+1)
}

// Logs a formatted message with the current timestamp.
func logf(format string, args ...any) {
	fmt.Printf(fmt.Sprintf("[%s] %s\n", time.Now().Format(time.StampMilli), format), args...)
}

// Displays the current test configuration in a human-readable format.
func printTestConfig(count int, batchSize int, image string, privileged bool, freeze bool) {
	privilegedStr := "unprivileged"
	if privileged {
		privilegedStr = "privileged"
	}

	mode := "normal startup"
	if freeze {
		mode = "start and freeze"
	}

	batches := count / batchSize
	remainder := count % batchSize
	fmt.Println("Test variables:")
	fmt.Println("  Container count:", count)
	fmt.Println("  Container mode:", privilegedStr)
	fmt.Println("  Startup mode:", mode)
	fmt.Println("  Image:", image)
	fmt.Println("  Batches:", batches)
	fmt.Println("  Batch size:", batchSize)
	fmt.Println("  Remainder:", remainder)
	fmt.Println("")
}
