package benchmark

import (
	"fmt"
	"time"
)

func getContainerName(count int, index int) string {
	nameFormat := "benchmark-%." + fmt.Sprintf("%d", len(fmt.Sprintf("%d", count))) + "d"
	return fmt.Sprintf(nameFormat, index+1)
}

func logf(format string, args ...interface{}) {
	fmt.Printf(fmt.Sprintf("[%s] %s\n", time.Now().Format(time.StampMilli), format), args...)
}

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
	fmt.Printf("Test variables:\n")
	fmt.Printf("  Container count: %d\n", count)
	fmt.Printf("  Container mode: %s\n", privilegedStr)
	fmt.Printf("  Startup mode: %s\n", mode)
	fmt.Printf("  Image: %s\n", image)
	fmt.Printf("  Batches: %d\n", batches)
	fmt.Printf("  Batch size: %d\n", batchSize)
	fmt.Printf("  Remainder: %d\n", remainder)
	fmt.Printf("\n")
}
