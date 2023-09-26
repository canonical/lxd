package benchmark

import (
	"os"
	"sync"
	"time"
)

// Calculates batch size based on input parallelism or defaults to CPU count.
func getBatchSize(parallel int) (int, error) {
	batchSize := parallel
	if batchSize < 1 {
		// Detect the number of parallel actions
		cpus, err := os.ReadDir("/sys/bus/cpu/devices")
		if err != nil {
			return -1, err
		}

		batchSize = len(cpus)
	}

	return batchSize, nil
}

// Executes a provided function across batches of tasks and reports the processing time.
func processBatch(count int, batchSize int, process func(index int, wg *sync.WaitGroup)) time.Duration {
	batches := count / batchSize
	remainder := count % batchSize
	processed := 0
	wg := sync.WaitGroup{}
	nextStat := batchSize

	logf("Batch processing start")
	timeStart := time.Now()

	for i := 0; i < batches; i++ {
		for j := 0; j < batchSize; j++ {
			wg.Add(1)
			go process(processed, &wg)
			processed++
		}

		wg.Wait()

		if processed >= nextStat {
			interval := time.Since(timeStart).Seconds()
			logf("Processed %d containers in %.3fs (%.3f/s)", processed, interval, float64(processed)/interval)
			nextStat = nextStat * 2
		}
	}

	for k := 0; k < remainder; k++ {
		wg.Add(1)
		go process(processed, &wg)
		processed++
	}

	wg.Wait()

	timeEnd := time.Now()
	duration := timeEnd.Sub(timeStart)
	logf("Batch processing completed in %.3fs", duration.Seconds())
	return duration
}
